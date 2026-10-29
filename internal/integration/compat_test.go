//go:build integration

package integration

import (
	"encoding/json/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noris-network/mcs-backup/v2/internal/service"
)

// oneX takes a snapshot the way mcs-backup 1.x did: host "dummy", no tags,
// paths relative to the current directory.
func (i *inst) oneX(paths ...string) {
	i.t.Helper()
	args := append([]string{"backup", "--json", "--host=dummy"}, paths...)
	if out, err := i.resticIn(i.root, args...); err != nil {
		i.t.Fatalf("restic %v: %v\n%s", args, err, out)
	}
}

func TestRestoreFromAOneXRepository(t *testing.T) {
	i := newInst(t)
	i.restic("init")
	i.write("app/config.yml", "v1")
	i.write("sub/file.txt", "sub")
	i.oneX(".")
	i.remove("app/config.yml")
	i.start()

	snaps := i.snapshots()
	if len(snaps) != 1 || snaps[0].Host != "dummy" || snaps[0].HasSummary && snaps[0].Files == 0 {
		t.Fatalf("snapshots = %+v", snaps)
	}
	if res := i.restore(service.RestoreRequest{Snapshot: "latest", Confirm: "latest"}); res.Result != service.ResultSuccess {
		t.Fatalf("restore: %s %s %s\n%s", res.Result, res.Message, res.Error, i.log)
	}
	if got := i.read("app/config.yml"); got != "v1" {
		t.Errorf("config.yml = %q", got)
	}
}

func TestOneXSnapshotsAreCleanedUpWithTheOwnOnes(t *testing.T) {
	i := newInst(t, `RETENTION_POLICY={"last":1}`)
	i.restic("init")
	i.write("a.txt", "1")
	i.write("sub/b.txt", "1")
	i.oneX(".")
	i.write("a.txt", "22")
	i.oneX(".")
	i.oneX("sub") // a second path group of the old instance
	i.start()

	i.write("a.txt", "333")
	i.mustBackup("", false)

	var own, sub int
	for _, s := range i.snapshots() {
		switch {
		case s.Host == "dummy" && len(s.Paths) == 1 && strings.HasSuffix(s.Paths[0], "/sub"):
			sub++
		case s.Host == "dummy":
			t.Errorf("a 1.x snapshot of the root group survived: %+v", s)
		default:
			own++
		}
	}
	if own != 1 {
		t.Errorf("own snapshots = %d, want 1", own)
	}
	if sub != 1 {
		t.Errorf("1.x snapshots of the other path group = %d, want 1 (each path group keeps its own latest)", sub)
	}
}

func TestOneXBackupIsTheParentOfTheFirstBackup(t *testing.T) {
	i := newInst(t)
	i.restic("init")
	i.write("a.txt", "x")
	i.oneX(".")
	// restic itself would only look for a parent of the same host name
	var old []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(i.restic("snapshots", "--json")), &old); err != nil || len(old) != 1 {
		t.Fatalf("snapshots: %v %v", old, err)
	}
	i.start()
	res := i.mustBackup("", false)
	out := i.restic("cat", "snapshot", res.Snapshot)
	if !strings.Contains(out, `"parent": "`+old[0].ID+`"`) && !strings.Contains(out, `"parent":"`+old[0].ID+`"`) {
		t.Errorf("the 1.x snapshot %s is not the parent of the first backup:\n%s", old[0].ID[:8], out)
	}
}

func TestOneXPipeSnapshotRestores(t *testing.T) {
	i := newInst(t)
	i.restic("init")
	cmd := exec.CommandContext(t.Context(), resticBin, "backup", "--stdin", "--host=dummy")
	cmd.Env = append(os.Environ(), "RESTIC_REPOSITORY="+i.repo, "RESTIC_PASSWORD=it-password", "RESTIC_CACHE_DIR="+filepath.Join(i.home, "cache"))
	cmd.Stdin = strings.NewReader("-- old dump\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("restic backup --stdin: %v\n%s", err, out)
	}
	restored := filepath.Join(i.home, "restored.sql")
	load := i.script("load.sh", "cat > "+restored)
	dump := i.script("dump.sh", "echo new dump")
	i.set("PIPE_IN_SCRIPT="+dump, "PIPE_OUT_SCRIPT="+load)
	i.start()

	if r := i.restore(service.RestoreRequest{Snapshot: "latest", Confirm: "latest", NoSafetySnapshot: true}); r.Result != service.ResultSuccess {
		t.Fatalf("restore: %s %s %s\n%s", r.Result, r.Message, r.Error, i.log)
	}
	if got := fileContent(t, restored); got != "-- old dump\n" {
		t.Errorf("the load script got %q", got)
	}
}
