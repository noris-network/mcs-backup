//go:build integration

package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noris-network/mcs-backup/v2/internal/service"
)

func TestBackupHooksRunInTheRootAndSeeTheResult(t *testing.T) {
	i := newInst(t)
	out := filepath.Join(i.home, "hooks.log")
	pre := i.script("pre.sh", `echo "pre cwd=$(pwd)" >> `+out+`; echo prepared > prepared.txt`)
	post := i.script("post.sh", `echo "post result=$MCS_BACKUP_RESULT snapshot=$MCS_BACKUP_SNAPSHOT error=$MCS_BACKUP_ERROR" >> `+out)
	i.set("PRE_BACKUP_SCRIPT="+pre, "POST_BACKUP_SCRIPT="+post)
	i.write("a.txt", "a")
	i.start()

	res := i.mustBackup("", false)
	log := fileContent(t, out)
	if !strings.Contains(log, "pre cwd="+i.root) {
		t.Errorf("the pre hook did not run in BACKUP_ROOT:\n%s", log)
	}
	if !strings.Contains(log, "post result=success snapshot="+res.Snapshot) {
		t.Errorf("the post hook did not get the result:\n%s", log)
	}
	// what the pre hook wrote is part of the snapshot
	if out := i.restic("ls", res.Snapshot); !strings.Contains(out, "prepared.txt") {
		t.Errorf("prepared.txt is not in the snapshot:\n%s", out)
	}
}

func TestPostHookRunsAfterAFailedBackup(t *testing.T) {
	i := newInst(t)
	out := filepath.Join(i.home, "post.log")
	pre := i.script("pre.sh", `echo "database is down" >&2; exit 1`)
	post := i.script("post.sh", `echo "result=$MCS_BACKUP_RESULT error=$MCS_BACKUP_ERROR" > `+out)
	i.set("PRE_BACKUP_SCRIPT="+pre, "POST_BACKUP_SCRIPT="+post)
	i.write("a.txt", "a")
	i.start()

	res := i.backup("", false)
	if res.Result != service.ResultFailure {
		t.Fatalf("result = %s, want failure", res.Result)
	}
	got := fileContent(t, out)
	if !strings.Contains(got, "result=failure") || !strings.Contains(got, "pre-backup hook") {
		t.Errorf("the post hook ran with %q", got)
	}
	if n := len(i.snapshots()); n != 0 {
		t.Errorf("a failed pre hook still made %d snapshots", n)
	}
}

func TestRestoreHooksGetTheirArguments(t *testing.T) {
	i := newInst(t)
	out := filepath.Join(i.home, "restore.log")
	pre := i.script("pre-restore.sh", `echo "pre args=$*" >> `+out)
	post := i.script("post-restore.sh", `echo "post args=$*" >> `+out)
	i.set("PRE_RESTORE_SCRIPT="+pre, "POST_RESTORE_SCRIPT="+post)
	i.write("a.txt", "a")
	i.start()
	i.mustBackup("", false)

	target := filepath.Join(i.home, "elsewhere")
	req := service.RestoreRequest{Snapshot: "latest", Target: target, HookArgs: []string{"--maintenance", "on"}}
	if r := i.restore(req); r.Result != service.ResultSuccess {
		t.Fatalf("restore: %s %s %s", r.Result, r.Message, r.Error)
	}
	log := fileContent(t, out)
	for _, want := range []string{"pre args=--maintenance on", "post args=--maintenance on"} {
		if !strings.Contains(log, want) {
			t.Errorf("hook log lacks %q:\n%s", want, log)
		}
	}
}

func TestPipeBackupAndRestore(t *testing.T) {
	i := newInst(t)
	dump := filepath.Join(i.home, "database")
	in := i.script("dump.sh", `cat `+dump)
	restored := filepath.Join(i.home, "restored.sql")
	outScript := i.script("load.sh", `cat > `+restored)
	i.set("PIPE_IN_SCRIPT="+in, "PIPE_OUT_SCRIPT="+outScript, "PIPE_IN_FILENAME=dump.sql")
	writeFile(t, dump, "CREATE TABLE a;\n-- Dump completed\n")
	i.start()

	res := i.mustBackup("", false)
	snaps := i.snapshots()
	if len(snaps) != 1 || len(snaps[0].Paths) != 1 || snaps[0].Paths[0] != "/dump.sql" {
		t.Fatalf("snapshots = %+v", snaps)
	}
	if res.Snapshot != snaps[0].ID {
		t.Errorf("result names %s, snapshot is %s", res.Snapshot, snaps[0].ID)
	}

	// the dump changes, the restore feeds the old one to the load script
	writeFile(t, dump, "DROP TABLE a;\n")
	writeFile(t, restored, "")
	if r := i.restore(service.RestoreRequest{Snapshot: "latest", Confirm: "latest", NoSafetySnapshot: true}); r.Result != service.ResultSuccess {
		t.Fatalf("restore: %s %s %s\n%s", r.Result, r.Message, r.Error, i.log)
	}
	if got := fileContent(t, restored); got != "CREATE TABLE a;\n-- Dump completed\n" {
		t.Errorf("the load script got %q", got)
	}
}

func TestPipeRestoreTakesASafetySnapshotThroughThePipe(t *testing.T) {
	i := newInst(t)
	dump := filepath.Join(i.home, "database")
	in := i.script("dump.sh", `cat `+dump)
	outScript := i.script("load.sh", `cat > `+filepath.Join(i.home, "restored.sql"))
	i.set("PIPE_IN_SCRIPT="+in, "PIPE_OUT_SCRIPT="+outScript)
	writeFile(t, dump, "state 1\n")
	i.start()
	i.mustBackup("", false)

	writeFile(t, dump, "state 2, about to be overwritten\n")
	if r := i.restore(service.RestoreRequest{Snapshot: "latest", Confirm: "latest"}); r.Result != service.ResultSuccess {
		t.Fatalf("restore: %s %s %s", r.Result, r.Message, r.Error)
	}
	var safety int
	for _, s := range i.snapshots() {
		if s.PreRestore {
			safety++
		}
	}
	if safety != 1 {
		t.Errorf("safety snapshots = %d, want 1", safety)
	}
}

func TestSafetySnapshotFailureStopsTheRestore(t *testing.T) {
	i := newInst(t)
	dump := filepath.Join(i.home, "database")
	flag := filepath.Join(i.home, "database-is-dead")
	in := i.script("dump.sh", `test ! -e `+flag+` && cat `+dump)
	restored := filepath.Join(i.home, "restored.sql")
	outScript := i.script("load.sh", `cat > `+restored)
	i.set("PIPE_IN_SCRIPT="+in, "PIPE_OUT_SCRIPT="+outScript)
	writeFile(t, dump, "good data\n")
	i.start()
	i.mustBackup("", false)

	writeFile(t, flag, "") // the database is dead: its dump fails now
	r := i.restore(service.RestoreRequest{Snapshot: "latest", Confirm: "latest"})
	if r.Result != service.ResultFailure {
		t.Fatalf("restore result = %s (%s), want failure", r.Result, r.Message)
	}
	if _, err := os.Stat(restored); err == nil {
		t.Error("the restore went on although the safety snapshot failed")
	}

	// explicitly without the safety snapshot it goes through
	r = i.restore(service.RestoreRequest{Snapshot: "latest", Confirm: "latest", NoSafetySnapshot: true})
	if r.Result != service.ResultSuccess {
		t.Fatalf("restore without safety snapshot: %s %s %s", r.Result, r.Message, r.Error)
	}
	if got := fileContent(t, restored); got != "good data\n" {
		t.Errorf("the load script got %q", got)
	}
}
