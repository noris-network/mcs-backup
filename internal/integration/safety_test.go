//go:build integration

package integration

import (
	"strings"
	"testing"

	"github.com/noris-network/mcs-backup/v2/internal/service"
)

func TestEmptyRootOnAFreshInstallationIsSkipped(t *testing.T) {
	i := newInst(t)
	i.start()
	res := i.backup("", false)
	if res.Result != service.ResultSkipped {
		t.Fatalf("result = %s (%s %s), want skipped\n%s", res.Result, res.Message, res.Error, i.log)
	}
	if n := len(i.snapshots()); n != 0 {
		t.Errorf("a skipped backup left %d snapshots", n)
	}
	// an empty snapshot would also be the first one: nothing was written to the repository
	if out := i.restic("snapshots", "--json"); strings.TrimSpace(out) != "[]" && strings.TrimSpace(out) != "null" {
		t.Errorf("restic snapshots = %s", out)
	}
}

func TestEmptyRootAfterARealBackupFails(t *testing.T) {
	i := newInst(t)
	i.write("data.txt", "precious")
	i.start()
	i.mustBackup("", false)

	i.remove("data.txt") // the volume came up empty
	res := i.backup("", false)
	if res.Result != service.ResultFailure {
		t.Fatalf("result = %s (%s %s), want failure", res.Result, res.Message, res.Error)
	}
	if n := len(i.snapshots()); n != 1 {
		t.Errorf("snapshots = %d: an empty backup must not become a snapshot", n)
	}
	if i.status().LastRuns[service.KindBackup].Result != service.ResultFailure {
		t.Error("the failure is not in the status")
	}
}

func TestAllowEmptySnapshot(t *testing.T) {
	i := newInst(t, "ALLOW_EMPTY_SNAPSHOT=true")
	i.write("data.txt", "x")
	i.start()
	i.mustBackup("", false)
	i.remove("data.txt")
	if res := i.backup("", false); res.Result != service.ResultSuccess {
		t.Fatalf("result = %s (%s %s)", res.Result, res.Message, res.Error)
	}
	if n := len(i.snapshots()); n != 2 {
		t.Errorf("snapshots = %d, want 2", n)
	}
}

func TestShrunkBackupIsSuspiciousUntilAcknowledged(t *testing.T) {
	i := newInst(t, `RETENTION_POLICY={"last":1}`)
	i.write("big.bin", strings.Repeat("x", 300_000))
	i.start()
	good := i.mustBackup("good", false)

	// the data collapses to a fraction
	i.remove("big.bin")
	i.write("small.txt", "tiny")
	res := i.backup("shrunk", false)
	if res.Result != service.ResultSuspicious {
		t.Fatalf("result = %s (%s %s), want suspicious", res.Result, res.Message, res.Error)
	}
	st := i.status()
	if !st.Suspicious {
		t.Error("the status does not show the suspicious backup")
	}
	snaps := i.snapshots()
	if len(snaps) != 2 {
		t.Fatalf("snapshots = %d, want both kept (retention last=1 must not delete the good one)", len(snaps))
	}
	var bad service.Snapshot
	for _, s := range snaps {
		if s.Suspicious {
			bad = s
		}
	}
	if bad.ID == "" || !strings.Contains(tags(bad), "suspicious") {
		t.Fatalf("no snapshot is tagged suspicious: %+v", snaps)
	}
	if latestRegular(snaps).ID != good.Snapshot {
		t.Errorf("latest regular = %s, want the good snapshot %s", latestRegular(snaps).ID, good.Snapshot)
	}

	// the state holds: another small backup is compared with the last regular snapshot
	if res := i.backup("still small", false); res.Result != service.ResultSuspicious {
		t.Errorf("second small backup: %s", res.Result)
	}
	if n := len(i.snapshots()); n != 3 {
		t.Errorf("snapshots = %d, want 3: nothing is forgotten while suspicious", n)
	}

	// without the word nothing happens
	if _, err := i.d.Acknowledge(i.ctx(), bad.ID, "yes"); err == nil {
		t.Error("acknowledged without the confirmation word")
	}
	if _, err := i.d.Acknowledge(i.ctx(), bad.ID, service.ConfirmAcknowledge); err != nil {
		t.Fatalf("Acknowledge: %v", err)
	}
	for _, s := range i.snapshots() {
		if s.ID == bad.ID {
			t.Errorf("the old ID of the acknowledged snapshot still exists")
		}
	}

	// the size is the new normal for the snapshot that was acknowledged; the one
	// that was not stays suspicious
	left := 0
	for _, s := range i.snapshots() {
		if s.Suspicious {
			left++
		}
	}
	if left != 1 {
		t.Errorf("suspicious snapshots left = %d, want 1", left)
	}
}

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
