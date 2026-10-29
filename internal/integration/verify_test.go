//go:build integration

package integration

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noris-network/mcs-backup/v2/internal/service"
)

func (i *inst) verify(mode string) service.RunResult {
	i.t.Helper()
	op, err := i.d.StartVerify(i.ctx(), service.VerifyRequest{Mode: mode})
	if err != nil {
		i.t.Fatalf("StartVerify: %v", err)
	}
	return i.finish(op)
}

// damagePacks overwrites the contents of every pack file of the repository.
func damagePacks(t *testing.T, repo string) {
	t.Helper()
	n := 0
	err := filepath.WalkDir(filepath.Join(repo, "data"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if err := os.Chmod(path, 0o644); err != nil { //nolint:gosec // test data
			return err
		}
		n++
		return os.WriteFile(path, []byte(strings.Repeat("X", int(info.Size()))), 0o644) //nolint:gosec // test data
	})
	if err != nil || n == 0 {
		t.Fatalf("damaging %d packs: %v", n, err)
	}
}

func TestVerifyRestoresASampleAndCleansUp(t *testing.T) {
	i := newInst(t)
	for n := range 5 {
		i.write("d/"+strings.Repeat("f", n+1)+".txt", strings.Repeat("content ", 100*(n+1)))
	}
	i.start()
	i.mustBackup("", false)

	res := i.verify("")
	if res.Result != service.ResultSuccess {
		t.Fatalf("verify: %s %s %s\n%s", res.Result, res.Message, res.Error, i.log)
	}
	if entries, err := os.ReadDir(i.cfg.Verify.Dir); err == nil && len(entries) != 0 {
		t.Errorf("the sandbox was not cleaned up: %v", entries)
	}
	if i.status().LastRuns[service.KindVerify].Result != service.ResultSuccess {
		t.Error("the result is not in the status")
	}
}

func TestVerifyFullRestore(t *testing.T) {
	i := newInst(t, "VERIFY_MODE=full")
	i.write("a/b.txt", "bbb")
	i.write("c.txt", "ccc")
	i.start()
	i.mustBackup("", false)
	if res := i.verify(""); res.Result != service.ResultSuccess {
		t.Fatalf("verify: %s %s %s\n%s", res.Result, res.Message, res.Error, i.log)
	}
}

func TestVerifyFailsOnADamagedRepository(t *testing.T) {
	i := newInst(t)
	i.write("a.txt", strings.Repeat("payload ", 1000))
	i.start()
	i.mustBackup("", false)
	damagePacks(t, i.repo)

	res := i.verify("full")
	if res.Result != service.ResultFailure {
		t.Fatalf("verify = %s (%s), want failure for damaged data", res.Result, res.Message)
	}
	if entries, err := os.ReadDir(i.cfg.Verify.Dir); err == nil && len(entries) != 0 {
		t.Errorf("the sandbox was not cleaned up after the failure: %v", entries)
	}
}

func TestVerifyScriptDecides(t *testing.T) {
	i := newInst(t)
	seen := filepath.Join(i.home, "seen")
	flag := filepath.Join(i.home, "fail")
	script := i.script("check.sh", `ls "$MCS_BACKUP_VERIFY_DIR" > `+seen+`; test ! -e `+flag)
	i.set("VERIFY_SCRIPT=" + script)
	i.write("only.txt", "only")
	i.start()
	i.mustBackup("", false)

	if res := i.verify("full"); res.Result != service.ResultSuccess {
		t.Fatalf("verify: %s %s %s\n%s", res.Result, res.Message, res.Error, i.log)
	}
	if got := fileContent(t, seen); !strings.Contains(got, "only.txt") {
		t.Errorf("the script saw %q in MCS_BACKUP_VERIFY_DIR", got)
	}

	writeFile(t, flag, "")
	if res := i.verify("full"); res.Result != service.ResultFailure {
		t.Fatalf("a failing VERIFY_SCRIPT gave %s", res.Result)
	}
}

func TestVerifyPipeModeStreamsTheDump(t *testing.T) {
	i := newInst(t)
	dump := filepath.Join(i.home, "dump.sql")
	writeFile(t, dump, "CREATE TABLE t;\n-- Dump completed\n")
	in := i.script("dump.sh", "cat "+dump)
	out := i.script("load.sh", "cat > /dev/null")
	got := filepath.Join(i.home, "verified")
	check := i.script("verify.sh", `cat > `+got+`; tail -n 1 `+got+` | grep -q "Dump completed"`)
	i.set("PIPE_IN_SCRIPT="+in, "PIPE_OUT_SCRIPT="+out, "VERIFY_SCRIPT="+check)
	i.start()
	i.mustBackup("", false)
	if res := i.verify(""); res.Result != service.ResultSuccess {
		t.Fatalf("verify: %s %s %s\n%s", res.Result, res.Message, res.Error, i.log)
	}
	if fileContent(t, got) != "CREATE TABLE t;\n-- Dump completed\n" {
		t.Errorf("the script got %q", fileContent(t, got))
	}
}

func TestVerifyWithoutASnapshotIsSkipped(t *testing.T) {
	i := newInst(t)
	i.start()
	if res := i.verify(""); res.Result != service.ResultSkipped {
		t.Fatalf("verify = %s (%s)", res.Result, res.Message)
	}
}
