package engine

import (
	"encoding/json/v2"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/noris-network/mcs-backup/v2/internal/config"
	"github.com/noris-network/mcs-backup/v2/internal/restic"
	"github.com/noris-network/mcs-backup/v2/internal/resticfake"
)

// testNow is the engine's clock in tests.
var testNow = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

// rig is an Engine wired to fake restic binaries.
type rig struct {
	t      *testing.T
	e      *Engine
	cfg    *config.Config
	main   *resticfake.Fake
	second *resticfake.Fake
	root   string

	mu     sync.Mutex
	lines  []string
	phases []string
}

func newRig(t *testing.T, withSecond bool, env ...string) *rig {
	t.Helper()
	root := t.TempDir()
	environ := []string{
		"PATH=" + os.Getenv("PATH"), "TZ=UTC",
		"RESTIC_REPOSITORY=/main", "RESTIC_PASSWORD=pw", `RETENTION_POLICY={"last":10}`,
		"BACKUP_ROOT=" + root, "MCS_BACKUP_HOST=me",
	}
	if withSecond {
		environ = append(environ, "COPY_REPOSITORY=/second", "COPY_PASSWORD=pw2")
	}
	cfg, err := config.Load(append(environ, env...))
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	r := &rig{t: t, cfg: cfg, root: root, main: resticfake.New(t)}
	o := Options{
		Config: cfg,
		Main:   restic.New(restic.Options{Bin: r.main.Bin, Env: cfg.ResticEnviron(), Dir: root}),
		Now:    func() time.Time { return testNow },
		Rand:   rand.New(rand.NewPCG(1, 2)),
	}
	if withSecond {
		r.second = resticfake.New(t)
		o.Second = restic.New(restic.Options{Bin: r.second.Bin, Env: cfg.ResticEnviron(), Dir: root})
	}
	r.e = New(o)
	return r
}

func (r *rig) sink() Sink {
	return Sink{
		Phase: func(name string) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.phases = append(r.phases, name)
		},
		Log: func(line string) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.lines = append(r.lines, line)
		},
	}
}

func (r *rig) logText() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.lines, "\n")
}

// script writes an executable shell script and returns its path.
func script(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "script.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// snap builds a snapshot taken age before testNow.
func snap(id, host string, age time.Duration, files, bytes int64, paths []string, tags ...string) restic.Snapshot {
	return restic.Snapshot{
		ID: id, ShortID: id, Hostname: host, Time: testNow.Add(-age), Paths: paths, Tags: tags,
		Tree:    "tree-" + id,
		Summary: &restic.SnapshotSummary{TotalFilesProcessed: files, TotalBytesProcessed: bytes},
	}
}

func snapsJSON(t *testing.T, snaps ...restic.Snapshot) string {
	t.Helper()
	b, err := json.Marshal(snaps)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func summaryLine(id string, files, bytes int64) string {
	return fmt.Sprintf(`{"message_type":"status","percent_done":0.5,"total_files":%d,"files_done":1,"current_files":["/x"]}`+"\n"+
		`{"message_type":"summary","snapshot_id":%q,"total_files_processed":%d,"total_bytes_processed":%d}`+"\n", files, id, files, bytes)
}

// forgetJSON is the output of forget with n kept and m removed snapshots.
func forgetJSON(t *testing.T, keep, remove int) string {
	t.Helper()
	g := restic.ForgetGroup{Host: "", Paths: []string{"/x"}}
	for i := range keep {
		g.Keep = append(g.Keep, restic.Snapshot{ID: fmt.Sprintf("k%d", i)})
	}
	for i := range remove {
		g.Remove = append(g.Remove, restic.Snapshot{ID: fmt.Sprintf("r%d", i), Time: testNow.Add(-time.Duration(i) * time.Hour)})
	}
	b, err := json.Marshal([]restic.ForgetGroup{g})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func lockJSON(age time.Duration) string {
	return fmt.Sprintf(`{"time":%q,"exclusive":false,"hostname":"h","username":"u","pid":42,"uid":1,"gid":1}`,
		testNow.Add(-age).Format(time.RFC3339))
}

func hasArgs(call []string, want ...string) bool {
	return slices.ContainsFunc(subslices(call, len(want)), func(s []string) bool { return slices.Equal(s, want) })
}

func subslices(s []string, n int) [][]string {
	var out [][]string
	for i := 0; i+n <= len(s); i++ {
		out = append(out, s[i:i+n])
	}
	return out
}

func rootPaths(r *rig) []string { return []string{r.root} }

// lsOutput is the output of restic ls --json for the given nodes.
func lsOutput(nodes ...restic.Node) string {
	var b strings.Builder
	b.WriteString(`{"message_type":"snapshot","struct_type":"snapshot","id":"x","paths":["/data"]}` + "\n")
	for _, n := range nodes {
		line, _ := json.Marshal(n)
		b.Write(line)
		b.WriteString("\n")
	}
	return b.String()
}

func file(path string, size int64) restic.Node {
	return restic.Node{Name: filepath.Base(path), Type: "file", Path: path, Size: size}
}

func statsJSON(size int64) string {
	return fmt.Sprintf(`{"total_size":%d,"total_file_count":3,"snapshots_count":1}`, size)
}

func restoreSummary(files int64) string {
	return fmt.Sprintf(`{"message_type":"summary","total_files":%d,"files_restored":%d,"total_bytes":10,"bytes_restored":10}`+"\n", files, files)
}

func writeFile(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
}

// readFile returns the content of a file, empty if it does not exist.
func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(data)
}
