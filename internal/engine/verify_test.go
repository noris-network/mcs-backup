package engine

import (
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/noris-network/mcs-backup/v2/internal/restic"
	"github.com/noris-network/mcs-backup/v2/internal/resticfake"
	"github.com/noris-network/mcs-backup/v2/internal/service"
)

// verifyRig prepares a rig with a regular snapshot of ten files.
func verifyRig(t *testing.T, withSecond bool, env ...string) (*rig, string) {
	t.Helper()
	sandboxBase := t.TempDir()
	r := newRig(t, withSecond, append([]string{"VERIFY_DIR=" + sandboxBase}, env...)...)
	s := snap("snap1", "me", time.Hour, 10, 1000, rootPaths(r), "mcs-backup")
	var nodes []restic.Node
	nodes = append(nodes, restic.Node{Type: "dir", Path: "/d"})
	for i := range 10 {
		nodes = append(nodes, file(fmt.Sprintf("/d/f%d", i), 100))
	}
	for _, f := range []*resticfake.Fake{r.main, r.second} {
		if f == nil {
			continue
		}
		f.On(t, "snapshots", resticfake.Response{Stdout: snapsJSON(t, s)})
		f.On(t, "ls", resticfake.Response{Stdout: lsOutput(nodes...)})
		f.On(t, "stats", resticfake.Response{Stdout: statsJSON(1000)})
		f.On(t, "restore", resticfake.Response{Stdout: restoreSummary(11)})
	}
	return r, sandboxBase
}

func entries(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range es {
		names = append(names, e.Name())
	}
	return names
}

func TestVerifySample(t *testing.T) {
	r, base := verifyRig(t, false, "VERIFY_SAMPLE_FILES=5")
	res := r.e.Verify(t.Context(), service.VerifyRequest{}, r.sink())
	if res.Result != service.ResultSuccess || len(res.Repos) != 1 {
		t.Fatalf("result = %+v", res)
	}
	rr := res.Repos[0]
	if rr.Files != 5 || rr.Bytes != 500 || rr.Snapshot != "snap1" || rr.Repo != "main" {
		t.Errorf("repo result = %+v", rr)
	}
	call := r.main.CallsTo(t, "restore")[0]
	if !slices.Contains(call, "--verify") || !slices.Contains(call, "--include-file") || !hasArgs(call, "snap1") {
		t.Errorf("restore args = %q", call)
	}
	target := call[slices.Index(call, "--target")+1]
	if filepath.Dir(target) != base {
		t.Errorf("the sandbox %q must be created in VERIFY_DIR %q", target, base)
	}
	if left := entries(t, base); len(left) != 0 {
		t.Errorf("sandbox not cleaned up: %q", left)
	}
	if !slices.Contains(r.phases, "verify") {
		t.Errorf("phases = %q", r.phases)
	}
}

func TestVerifyFull(t *testing.T) {
	r, base := verifyRig(t, false)
	res := r.e.Verify(t.Context(), service.VerifyRequest{Mode: VerifyFull}, r.sink())
	if res.Result != service.ResultSuccess {
		t.Fatalf("result = %+v", res)
	}
	call := r.main.CallsTo(t, "restore")[0]
	if slices.Contains(call, "--include-file") || !slices.Contains(call, "--verify") {
		t.Errorf("a full test restores everything: %q", call)
	}
	if len(r.main.CallsTo(t, "ls")) != 0 {
		t.Error("a full test needs no file list")
	}
	if left := entries(t, base); len(left) != 0 {
		t.Errorf("sandbox not cleaned up: %q", left)
	}
	// restic counts the directories with the files, and the log ends with the result
	if !strings.Contains(r.logText(), "files and directories restored and verified") {
		t.Errorf("log:\n%s", r.logText())
	}
}

func TestVerifyConfiguredFullMode(t *testing.T) {
	r, _ := verifyRig(t, false, "VERIFY_MODE=full")
	if res := r.e.Verify(t.Context(), service.VerifyRequest{}, r.sink()); res.Result != service.ResultSuccess || len(r.main.CallsTo(t, "ls")) != 0 {
		t.Errorf("result = %+v", res)
	}
}

func TestVerifyFailures(t *testing.T) {
	t.Run("restore fails and the sandbox is still removed", func(t *testing.T) {
		r, base := verifyRig(t, false)
		r.main.On(t, "restore", resticfake.Response{Stderr: "Fatal: There were 1 errors\n", Exit: 1})
		res := r.e.Verify(t.Context(), service.VerifyRequest{}, r.sink())
		if !strings.Contains(r.logText(), "verify main: the restore test failed: ") {
			t.Errorf("the log does not end with the failure:\n%s", r.logText())
		}
		if res.Result != service.ResultFailure || !strings.Contains(res.Error, "There were 1 errors") {
			t.Fatalf("result = %+v", res)
		}
		if left := entries(t, base); len(left) != 0 {
			t.Errorf("sandbox not cleaned up: %q", left)
		}
	})

	t.Run("fewer files restored than sampled", func(t *testing.T) {
		r, _ := verifyRig(t, false, "VERIFY_SAMPLE_FILES=5")
		r.main.On(t, "restore", resticfake.Response{Stdout: restoreSummary(2)})
		res := r.e.Verify(t.Context(), service.VerifyRequest{}, r.sink())
		if res.Result != service.ResultFailure || !strings.Contains(res.Error, "restored 2 entries for 5 sampled files") {
			t.Errorf("result = %+v", res)
		}
	})

	t.Run("no snapshot yet", func(t *testing.T) {
		r, _ := verifyRig(t, false)
		r.main.On(t, "snapshots", resticfake.Response{Stdout: snapsJSON(t)})
		res := r.e.Verify(t.Context(), service.VerifyRequest{}, r.sink())
		if res.Result != service.ResultSkipped || len(r.main.CallsTo(t, "restore")) != 0 {
			t.Errorf("result = %+v", res)
		}
	})

	t.Run("only suspicious snapshots", func(t *testing.T) {
		r, _ := verifyRig(t, false)
		r.main.On(t, "snapshots", resticfake.Response{Stdout: snapsJSON(t, snap("bad", "me", time.Hour, 1, 1, nil, "suspicious"))})
		if res := r.e.Verify(t.Context(), service.VerifyRequest{}, r.sink()); res.Result != service.ResultSkipped {
			t.Errorf("result = %+v: only regular snapshots are tested", res)
		}
	})

	t.Run("snapshot without files", func(t *testing.T) {
		r, _ := verifyRig(t, false)
		r.main.On(t, "ls", resticfake.Response{Stdout: lsOutput(restic.Node{Type: "dir", Path: "/d"})})
		if res := r.e.Verify(t.Context(), service.VerifyRequest{}, r.sink()); res.Result != service.ResultSkipped {
			t.Errorf("result = %+v", res)
		}
	})

	t.Run("not enough space skips the test", func(t *testing.T) {
		r, base := verifyRig(t, false)
		r.main.On(t, "stats", resticfake.Response{Stdout: statsJSON(1 << 60)})
		res := r.e.Verify(t.Context(), service.VerifyRequest{Mode: VerifyFull}, r.sink())
		if res.Result != service.ResultSkipped || !strings.Contains(res.Message, "not enough space") || len(r.main.CallsTo(t, "restore")) != 0 {
			t.Errorf("result = %+v", res)
		}
		if left := entries(t, base); len(left) != 0 {
			t.Errorf("sandbox not cleaned up: %q", left)
		}
	})

	t.Run("a sandbox that cannot be removed is a warning", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root can remove anything")
		}
		lock := script(t, `chmod 500 "$(dirname "$MCS_BACKUP_VERIFY_DIR")"`)
		r, base := verifyRig(t, false, "VERIFY_SCRIPT="+lock)
		t.Cleanup(func() { _ = os.Chmod(base, 0o700) }) //nolint:gosec // let t.TempDir clean up
		res := r.e.Verify(t.Context(), service.VerifyRequest{}, r.sink())
		if res.Result != service.ResultWarning || !strings.Contains(res.Message, "files restored and verified; the sandbox "+base) {
			t.Errorf("result = %+v", res)
		}
		if !slices.ContainsFunc(r.lines, func(l string) bool { return strings.Contains(l, "cannot remove the sandbox") }) {
			t.Errorf("the run log does not say why: %q", r.lines)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		r, base := verifyRig(t, false, "VERIFY_TIMEOUT=400ms")
		r.main.On(t, "restore", resticfake.Response{Sleep: 30 * time.Second})
		res := r.e.Verify(t.Context(), service.VerifyRequest{}, r.sink())
		if res.Result != service.ResultTimeout {
			t.Errorf("result = %+v", res)
		}
		if left := entries(t, base); len(left) != 0 {
			t.Errorf("sandbox not cleaned up: %q", left)
		}
	})
}

func TestVerifyScript(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out")
	ok := script(t, `echo "dir=$MCS_BACKUP_VERIFY_DIR cwd=$(pwd)" > "$OUT"`)
	r, _ := verifyRig(t, false, "VERIFY_SCRIPT="+ok, "OUT="+out)

	res := r.e.Verify(t.Context(), service.VerifyRequest{}, r.sink())
	if res.Result != service.ResultSuccess {
		t.Fatalf("result = %+v", res)
	}
	got, _ := os.ReadFile(out)
	target := r.main.CallsTo(t, "restore")[0]
	sandbox := target[slices.Index(target, "--target")+1]
	resolved, _ := filepath.EvalSymlinks(sandbox)
	if !strings.Contains(string(got), "dir="+sandbox+" ") || !strings.Contains(string(got), "cwd="+resolved) {
		t.Errorf("the script must run in the sandbox: %q (sandbox %q)", got, sandbox)
	}

	bad := script(t, `echo "checksum mismatch" >&2; exit 1`)
	r, base := verifyRig(t, false, "VERIFY_SCRIPT="+bad)
	res = r.e.Verify(t.Context(), service.VerifyRequest{}, r.sink())
	if res.Result != service.ResultFailure || !strings.Contains(res.Error, "checksum mismatch") {
		t.Fatalf("a failing script fails the test: %+v", res)
	}
	if left := entries(t, base); len(left) != 0 {
		t.Errorf("sandbox not cleaned up: %q", left)
	}
}

func TestVerifyCopy(t *testing.T) {
	r, _ := verifyRig(t, true, "VERIFY_COPY=true")
	res := r.e.Verify(t.Context(), service.VerifyRequest{}, r.sink())
	if res.Result != service.ResultSuccess || len(res.Repos) != 2 || res.Repos[1].Repo != "copy" {
		t.Fatalf("result = %+v", res)
	}
	if len(r.second.CallsTo(t, "restore")) != 1 {
		t.Error("the secondary repository is tested too")
	}

	r.second.On(t, "restore", resticfake.Response{Stderr: "Fatal: pack missing\n", Exit: 1})
	res = r.e.Verify(t.Context(), service.VerifyRequest{}, r.sink())
	if res.Result != service.ResultFailure || res.Repos[0].Result != service.ResultSuccess || res.Repos[1].Result != service.ResultFailure ||
		!strings.Contains(res.Error, "copy repository") {
		t.Errorf("result = %+v", res)
	}

	r, _ = verifyRig(t, true)
	if res := r.e.Verify(t.Context(), service.VerifyRequest{}, r.sink()); len(res.Repos) != 1 || len(r.second.Calls(t)) != 0 {
		t.Errorf("without VERIFY_COPY only the main repository is tested: %+v", res)
	}
}

func TestVerifyPipeMode(t *testing.T) {
	in, out := script(t, "echo dump"), script(t, "cat > /dev/null")
	received := filepath.Join(t.TempDir(), "received")
	pipeEnv := []string{"PIPE_IN_SCRIPT=" + in, "PIPE_OUT_SCRIPT=" + out, "PIPE_IN_FILENAME=dump.sql"}
	setup := func(t *testing.T, env ...string) *rig {
		r := newRig(t, false, append(pipeEnv, env...)...)
		r.main.On(t, "snapshots", resticfake.Response{Stdout: snapsJSON(t, snap("snap1", "me", time.Hour, 1, 100, []string{"/dump.sql"}, "mcs-backup"))})
		r.main.On(t, "dump", resticfake.Response{Stdout: "CREATE TABLE t (a int);\n-- Dump completed\n"})
		return r
	}

	t.Run("not configured without a script", func(t *testing.T) {
		r := setup(t)
		if r.e.VerifyConfigured() {
			t.Error("VerifyConfigured() in pipe mode without VERIFY_SCRIPT")
		}
		res := r.e.Verify(t.Context(), service.VerifyRequest{}, r.sink())
		if res.Result != service.ResultSkipped || !strings.Contains(res.Message, "not configured") || len(r.main.Calls(t)) != 0 {
			t.Errorf("result = %+v", res)
		}
	})

	t.Run("streams the dump to the script", func(t *testing.T) {
		check := script(t, `grep -q "Dump completed" && echo ok > "$RECEIVED"`)
		r := setup(t, "VERIFY_SCRIPT="+check, "RECEIVED="+received)
		res := r.e.Verify(t.Context(), service.VerifyRequest{}, r.sink())
		if res.Result != service.ResultSuccess {
			t.Fatalf("result = %+v", res)
		}
		if got, _ := os.ReadFile(received); string(got) != "ok\n" {
			t.Errorf("script output = %q", got)
		}
		if call := r.main.CallsTo(t, "dump")[0]; !slices.Equal(call, []string{"dump", "--", "snap1", "/dump.sql"}) {
			t.Errorf("dump args = %q", call)
		}
		if len(r.main.CallsTo(t, "restore")) != 0 {
			t.Error("pipe mode never restores files")
		}
	})

	t.Run("the script rejects the dump", func(t *testing.T) {
		check := script(t, `cat > /dev/null; echo "truncated dump" >&2; exit 1`)
		r := setup(t, "VERIFY_SCRIPT="+check)
		res := r.e.Verify(t.Context(), service.VerifyRequest{}, r.sink())
		if res.Result != service.ResultFailure || !strings.Contains(res.Error, "truncated dump") {
			t.Errorf("result = %+v", res)
		}
	})

	t.Run("restic cannot read the snapshot", func(t *testing.T) {
		check := script(t, `cat > /dev/null`)
		r := setup(t, "VERIFY_SCRIPT="+check)
		r.main.On(t, "dump", resticfake.Response{Stderr: "Fatal: pack missing\n", Exit: 1})
		res := r.e.Verify(t.Context(), service.VerifyRequest{}, r.sink())
		if res.Result != service.ResultFailure || !strings.Contains(res.Error, "pack missing") {
			t.Errorf("result = %+v", res)
		}
	})
}

func TestSampleFiles(t *testing.T) {
	manyFiles := func(n int, size int64) string {
		nodes := []restic.Node{{Type: "dir", Path: "/d"}, {Type: "symlink", Path: "/d/link"}}
		for i := range n {
			nodes = append(nodes, file(fmt.Sprintf("/d/f%d", i), size))
		}
		return lsOutput(nodes...)
	}
	sample := func(t *testing.T, env []string, ls string) []restic.Node {
		r := newRig(t, false, env...)
		r.main.On(t, "ls", resticfake.Response{Stdout: ls})
		nodes, err := r.e.sampleFiles(t.Context(), r.e.main, restic.Snapshot{ID: "snap1"})
		if err != nil {
			t.Fatal(err)
		}
		return nodes
	}

	if got := sample(t, []string{"VERIFY_SAMPLE_FILES=7"}, manyFiles(100, 10)); len(got) != 7 {
		t.Errorf("sampled %d files, want 7", len(got))
	}
	for _, n := range sample(t, nil, manyFiles(100, 10)) {
		if n.Type != "file" {
			t.Errorf("sampled a %s", n.Type)
		}
	}
	if got := sample(t, []string{"VERIFY_SAMPLE_FILES=50"}, manyFiles(8, 10)); len(got) != 8 {
		t.Errorf("a snapshot with fewer files than the sample is tested completely: %d", len(got))
	}
	if got := sample(t, []string{"VERIFY_SAMPLE_FILES=50", "VERIFY_SAMPLE_BYTES=350"}, manyFiles(100, 100)); len(got) != 3 {
		t.Errorf("the byte limit allows 3 files of 100 bytes, got %d", len(got))
	}
	if got := sample(t, []string{"VERIFY_SAMPLE_BYTES=10"}, manyFiles(5, 1000)); len(got) != 1 {
		t.Errorf("at least one file is tested even if it exceeds the limit: %d", len(got))
	}
	if got := sample(t, nil, lsOutput(restic.Node{Type: "dir", Path: "/d"})); len(got) != 0 {
		t.Errorf("sampled %d files from a snapshot without files", len(got))
	}
}

func TestSampleFilesIsUniform(t *testing.T) {
	const files, runs = 10, 400
	r := newRig(t, false, "VERIFY_SAMPLE_FILES=1")
	var nodes []restic.Node
	for i := range files {
		nodes = append(nodes, file(fmt.Sprintf("/f%d", i), 1))
	}
	r.main.On(t, "ls", resticfake.Response{Stdout: lsOutput(nodes...)})

	counts := make([]int, files)
	for seed := range uint64(runs) {
		r.e.rnd = rand.New(rand.NewPCG(seed, 7))
		got, err := r.e.sampleFiles(t.Context(), r.e.main, restic.Snapshot{ID: "x"})
		if err != nil || len(got) != 1 {
			t.Fatal(got, err)
		}
		var i int
		if _, err := fmt.Sscanf(got[0].Path, "/f%d", &i); err != nil {
			t.Fatal(err)
		}
		counts[i]++
	}
	for i, c := range counts {
		if c < 15 || c > 70 { // expected: 40
			t.Errorf("file %d was picked %d times of %d: the sample is not uniform (%v)", i, c, runs, counts)
			break
		}
	}
}

func TestEscapePattern(t *testing.T) {
	tests := map[string]string{
		"/plain/file.txt": "/plain/file.txt",
		"/a[1]/b*?":       `/a\[1]/b\*\?`,
		`/back\slash`:     `/back\\slash`,
		"/ümläut/日本語":     "/ümläut/日本語",
		"/with space/x y": "/with space/x y",
		"/price$5/$HOME":  "/price$$5/$$HOME",
		"/trailing \t":    "/trailing[ ][\t]",
		"/ leading/x":     "/ leading/x",
		"/new\nline\n":    "/new?line?",
		"/caf\ufffd":      "/caf?",
	}
	for in, want := range tests {
		if got := escapePattern(in); got != want {
			t.Errorf("escapePattern(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestForceRemove(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sandbox")
	deep := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(deep, "f"), 1)
	for _, d := range []string{deep, filepath.Join(dir, "a"), dir} {
		if err := os.Chmod(d, 0o500); err != nil {
			t.Fatal(err)
		}
	}
	if err := forceRemove(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("sandbox with read-only directories still exists: %v", err)
	}
}

func TestWithin(t *testing.T) {
	for _, tt := range []struct {
		dir, path string
		want      bool
	}{
		{"/mnt", "/mnt", true}, {"/mnt", "/mnt/x/y", true}, {"/mnt", "/mnt-other", false},
		{"/mnt", "/tmp", false}, {"/mnt/", "/mnt/a", true}, {"/mnt/a", "/mnt", false},
	} {
		if got := within(tt.dir, tt.path); got != tt.want {
			t.Errorf("within(%q, %q) = %v, want %v", tt.dir, tt.path, got, tt.want)
		}
	}
}
