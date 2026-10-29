//go:build integration

package integration

import (
	"strings"
	"testing"

	"github.com/noris-network/mcs-backup/v2/internal/service"
)

func TestKeepSurvivesTheRetention(t *testing.T) {
	i := newInst(t, `RETENTION_POLICY={"last":1}`)
	i.write("a.txt", "1")
	i.start()
	i.mustBackup("one", false)
	i.write("a.txt", "22")
	i.mustBackup("two", false)

	// retention last=1 removed the first; keep the current one, then make newer ones
	var kept service.Snapshot
	for _, s := range i.snapshots() {
		kept = s
	}
	res, err := i.d.Keep(i.ctx(), kept.ID)
	if err != nil {
		t.Fatalf("Keep: %v", err)
	}
	if res.ID == "" || res.ID == kept.ID {
		t.Errorf("restic gives a snapshot a new ID when its tags change: %+v", res)
	}
	for _, n := range []string{"333", "4444", "55555"} {
		i.write("a.txt", n)
		i.mustBackup(n, false)
	}
	snaps := i.snapshots()
	if len(snaps) != 2 {
		t.Fatalf("snapshots = %d (%v), want the kept one and the latest", len(snaps), snaps)
	}
	var sawKeep bool
	for _, s := range snaps {
		if s.Keep && s.Note == "two" {
			sawKeep = true
		}
	}
	if !sawKeep {
		t.Errorf("the kept snapshot is gone: %+v", snaps)
	}

	// unkeep: the next forget takes it
	if _, err := i.d.Unkeep(i.ctx(), res.ID); err != nil {
		t.Fatalf("Unkeep: %v", err)
	}
	i.write("a.txt", "666666")
	i.mustBackup("six", false)
	if n := len(i.snapshots()); n != 1 {
		t.Errorf("snapshots = %d, want 1 after unkeep", n)
	}
}

func TestBackupWithKeepFlag(t *testing.T) {
	i := newInst(t, `RETENTION_POLICY={"last":1}`)
	i.write("a.txt", "1")
	i.start()
	i.mustBackup("precious", true)
	for _, n := range []string{"22", "333"} {
		i.write("a.txt", n)
		i.mustBackup(n, false)
	}
	var kept bool
	for _, s := range i.snapshots() {
		kept = kept || (s.Keep && s.Note == "precious")
	}
	if !kept || len(i.snapshots()) != 2 {
		t.Errorf("snapshots = %+v", i.snapshots())
	}
}

func TestRetentionGuardHoldsALargeForget(t *testing.T) {
	i := newInst(t, `RETENTION_POLICY={"last":1}`)
	i.write("a.txt", "x")
	// six snapshots exist before the service starts, as after a policy typo
	i.restic("init")
	for n := range 6 {
		i.write("a.txt", strings.Repeat("x", n+1))
		i.restic("backup", "--host", "mcs-backup", "--tag", "mcs-backup", i.root)
	}
	i.start()
	i.write("a.txt", "newest")
	res := i.backup("", false)
	if res.Result != service.ResultWarning {
		t.Fatalf("result = %s (%s %s), want warning because the forget is held", res.Result, res.Message, res.Error)
	}
	st := i.status()
	if !st.ForgetHeld {
		t.Error("the status does not show the held forget")
	}
	if n := len(i.snapshots()); n != 7 {
		t.Fatalf("snapshots = %d, want 7: nothing may be deleted while the guard holds", n)
	}
	ret, err := i.d.Retention(i.ctx())
	if err != nil || !ret.Held || len(ret.Remove) != 6 {
		t.Fatalf("Retention() = %+v, %v", ret, err)
	}

	if _, err := i.d.ApproveForget(i.ctx(), "yes"); err == nil {
		t.Error("approved without the confirmation word")
	}
	op, err := i.d.ApproveForget(i.ctx(), service.ConfirmForget)
	if err != nil {
		t.Fatal(err)
	}
	if r := i.finish(op); r.Result != service.ResultSuccess {
		t.Fatalf("approved forget: %s %s", r.Result, r.Error)
	}
	if n := len(i.snapshots()); n != 1 {
		t.Errorf("snapshots = %d, want 1 after the approved forget", n)
	}
	if i.status().ForgetHeld {
		t.Error("the hold remains after the approval")
	}
}

func TestForeignSnapshotsInASharedRepositoryAreNeverTouched(t *testing.T) {
	i := newInst(t, `RETENTION_POLICY={"last":1}`, "MCS_BACKUP_HOST=app-a")
	i.write("a.txt", "x")
	i.restic("init")
	for n := range 3 {
		i.write("a.txt", strings.Repeat("o", n+1))
		i.restic("backup", "--host", "app-b", "--tag", "other-tool", i.root)
	}
	i.start()
	for _, n := range []string{"1", "22", "333"} {
		i.write("a.txt", n)
		i.mustBackup(n, false)
	}
	out := i.restic("snapshots", "--json")
	if got := strings.Count(out, `"hostname":"app-b"`); got != 3 {
		t.Errorf("snapshots of the other host: %d, want 3 untouched\n%s", got, out)
	}
	if got := strings.Count(out, `"hostname":"app-a"`); got != 1 {
		t.Errorf("snapshots of this host: %d, want 1 (retention last=1)", got)
	}
	ret, err := i.d.Retention(i.ctx())
	if err != nil || ret.Foreign != 3 {
		t.Errorf("Retention() = %+v, %v; want 3 foreign snapshots", ret, err)
	}
}
