//go:build integration

package integration

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/noris-network/mcs-backup/v2/internal/service"
)

func TestBackupAndRestoreInPlace(t *testing.T) {
	i := newInst(t)
	i.write("app/config.yml", "version: 1\n")
	i.write("app/data/a.txt", "alpha\n")
	i.start()

	res := i.mustBackup("first", false)
	snaps := i.snapshots()
	if len(snaps) != 1 || snaps[0].Host != "mcs-backup" || snaps[0].Origin != service.OriginManual || snaps[0].Note != "first" {
		t.Fatalf("snapshots = %+v", snaps)
	}
	if !slices.Contains(snaps[0].Tags, "mcs-backup") {
		t.Errorf("tags = %v", snaps[0].Tags)
	}
	if res.Snapshot == "" {
		t.Error("the result names no snapshot")
	}

	// the data changes: one file edited, one removed, one added
	i.write("app/config.yml", "version: 2\n")
	i.remove("app/data/a.txt")
	i.write("app/extra.txt", "not in the snapshot\n")

	// an in-place restore needs the snapshot argument as confirmation
	op, err := i.d.StartRestore(i.ctx(), service.RestoreRequest{Snapshot: "latest"})
	if err == nil {
		i.finish(op)
		t.Fatal("an in-place restore without confirmation started")
	}

	if r := i.restore(service.RestoreRequest{Snapshot: "latest", Confirm: "latest"}); r.Result != service.ResultSuccess {
		t.Fatalf("restore: %s %s %s\n%s", r.Result, r.Message, r.Error, i.log)
	}
	if got := i.read("app/config.yml"); got != "version: 1\n" {
		t.Errorf("config.yml = %q", got)
	}
	if got := i.read("app/data/a.txt"); got != "alpha\n" {
		t.Errorf("a.txt = %q", got)
	}
	if i.exists("app/extra.txt") {
		t.Error("exact mode left a file that is not in the snapshot")
	}

	// the safety snapshot holds the state before the restore
	var safety *service.Snapshot
	for _, s := range i.snapshots() {
		if s.PreRestore {
			safety = &s
		}
	}
	if safety == nil {
		t.Fatalf("no pre-restore snapshot: %+v", i.snapshots())
	}
	if out := i.restic("ls", safety.ID); !strings.Contains(out, "extra.txt") {
		t.Errorf("the safety snapshot lacks the file that the restore removed:\n%s", out)
	}
	// "latest" still means the regular snapshot, not the safety snapshot
	out := i.restic("snapshots", "latest", "--json")
	if strings.Contains(out, "pre-restore") && !strings.Contains(out, "mcs-backup") {
		t.Errorf("latest is the safety snapshot: %s", out)
	}
	if got := latestRegular(i.snapshots()); got.PreRestore || got.ID == "" {
		t.Errorf("latest regular = %+v", got)
	}
}

func TestRestoreMergeKeepsOtherFiles(t *testing.T) {
	i := newInst(t)
	i.write("a.txt", "one\n")
	i.start()
	i.mustBackup("", false)

	i.write("a.txt", "changed\n")
	i.write("b.txt", "mine\n")
	if r := i.restore(service.RestoreRequest{Snapshot: "latest", Mode: service.ModeMerge, Confirm: "latest"}); r.Result != service.ResultSuccess {
		t.Fatalf("restore: %s %s", r.Result, r.Error)
	}
	if i.read("a.txt") != "one\n" || i.read("b.txt") != "mine\n" {
		t.Errorf("merge: a=%q b=%q", i.read("a.txt"), i.read("b.txt"))
	}
}

func TestPartialRestoreToAnotherDirectory(t *testing.T) {
	i := newInst(t)
	i.write("etc/app.conf", "conf\n")
	i.write("var/lib/data.db", "data\n")
	i.start()
	i.mustBackup("", false)

	target := filepath.Join(i.home, "out")
	pre, err := i.d.RestorePreflight(i.ctx(), service.RestoreRequest{Snapshot: "latest", Target: target, Includes: []string{"/etc"}})
	if err != nil || pre.InPlace || !pre.TargetEmpty {
		t.Fatalf("preflight = %+v, %v", pre, err)
	}
	if r := i.restore(service.RestoreRequest{Snapshot: "latest", Target: target, Includes: []string{"/etc"}}); r.Result != service.ResultSuccess {
		t.Fatalf("restore: %s %s\n%s", r.Result, r.Error, i.log)
	}
	if b, err := os.ReadFile(filepath.Join(target, "etc/app.conf")); err != nil || string(b) != "conf\n" {
		t.Errorf("etc/app.conf: %q, %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(target, "var")); err == nil {
		t.Error("a path that was not included was restored")
	}
	// no safety snapshot for a restore elsewhere, and the live data is untouched
	for _, s := range i.snapshots() {
		if s.PreRestore {
			t.Error("a restore to another directory took a safety snapshot")
		}
	}
	if i.read("etc/app.conf") != "conf\n" {
		t.Error("the live data changed")
	}
}

// restic reads flags anywhere on the command line: an include path that looks
// like --password-command must stay a path and not run a command.
func TestRestoreIncludeIsNoFlag(t *testing.T) {
	i := newInst(t)
	i.write("app/a.txt", "alpha\n")
	i.start()
	i.mustBackup("", false)

	marker := filepath.Join(i.home, "pwned")
	req := service.RestoreRequest{Snapshot: "latest", Includes: []string{"--password-command=touch " + marker}}
	if _, err := i.d.RestorePreflight(i.ctx(), req); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("an include path ran a command")
	}
}

func TestBackupPathsAndExcludes(t *testing.T) {
	i := newInst(t, "BACKUP_PATHS=keep:also", "EXCLUDE_PATHS=keep/tmp")
	i.write("keep/a.txt", "a")
	i.write("keep/tmp/junk", "junk")
	i.write("also/b.txt", "b")
	i.write("skip/c.txt", "c")
	i.start()
	i.mustBackup("", false)

	out := i.restic("ls", "latest")
	for _, want := range []string{"keep/a.txt", "also/b.txt"} {
		if !strings.Contains(out, want) {
			t.Errorf("the snapshot lacks %s:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"skip/c.txt", "keep/tmp/junk"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("the snapshot contains %s:\n%s", unwanted, out)
		}
	}
}

func TestUnreadableFileGivesAWarningAndKeepsTheSnapshot(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read every file")
	}
	i := newInst(t)
	i.write("ok.txt", "fine")
	i.write("secret.txt", "hidden")
	if err := os.Chmod(filepath.Join(i.root, "secret.txt"), 0); err != nil {
		t.Fatal(err)
	}
	i.start()

	res := i.backup("", false)
	if res.Result != service.ResultWarning {
		t.Fatalf("result = %s (%s %s), want warning\n%s", res.Result, res.Message, res.Error, i.log)
	}
	snaps := i.snapshots()
	if len(snaps) != 1 {
		t.Fatalf("a backup with an unreadable file must keep its snapshot: %+v", snaps)
	}
	if out := i.restic("ls", snaps[0].ID); !strings.Contains(out, "ok.txt") {
		t.Errorf("the readable file is missing:\n%s", out)
	}
}
