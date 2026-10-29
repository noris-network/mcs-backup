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

// restic itself refuses to store a snapshot without any content, so
// ALLOW_EMPTY_SNAPSHOT can only keep the run from failing.
func TestAllowEmptySnapshotDoesNotFail(t *testing.T) {
	i := newInst(t, "ALLOW_EMPTY_SNAPSHOT=true")
	i.write("data.txt", "x")
	i.start()
	i.mustBackup("", false)
	i.remove("data.txt")
	if res := i.backup("", false); res.Result != service.ResultSkipped {
		t.Fatalf("result = %s (%s %s), want skipped", res.Result, res.Message, res.Error)
	}
	if n := len(i.snapshots()); n != 1 {
		t.Errorf("snapshots = %d, want 1", n)
	}
}

func TestShrunkBackupIsSuspiciousUntilAcknowledged(t *testing.T) {
	i := newInst(t, `RETENTION_POLICY={"last":1}`)
	i.write("big.bin", strings.Repeat("x", 300_000))
	i.start()
	good := i.mustBackup("good", false)

	i.remove("big.bin") // the data collapses to a fraction
	i.write("small.txt", "tiny")
	res := i.backup("shrunk", false)
	if res.Result != service.ResultSuspicious {
		t.Fatalf("result = %s (%s %s), want suspicious", res.Result, res.Message, res.Error)
	}
	if !i.status().Suspicious {
		t.Error("the status does not show the suspicious backup")
	}
	snaps := i.snapshots()
	if len(snaps) != 2 {
		t.Fatalf("snapshots = %d, want both kept (retention last=1 must not delete the good one)", len(snaps))
	}
	if got := latestRegular(snaps).ID; got != good.Snapshot {
		t.Errorf("latest regular = %s, want the good snapshot %s", got, good.Snapshot)
	}

	// the comparison stays with the last regular snapshot
	if res := i.backup("still small", false); res.Result != service.ResultSuspicious {
		t.Errorf("second small backup: %s", res.Result)
	}
	snaps = i.snapshots()
	if len(snaps) != 3 {
		t.Fatalf("snapshots = %d, want 3: nothing is forgotten while suspicious", len(snaps))
	}

	var bad service.Snapshot
	for _, s := range snaps {
		if s.Suspicious && (bad.ID == "" || s.Time.Before(bad.Time)) {
			bad = s
		}
	}
	if bad.ID == "" {
		t.Fatalf("no snapshot is tagged suspicious: %+v", snaps)
	}
	if _, err := i.d.Acknowledge(i.ctx(), bad.ID, "yes"); err == nil {
		t.Error("acknowledged without the confirmation word")
	}
	ack, err := i.d.Acknowledge(i.ctx(), bad.ID, service.ConfirmAcknowledge)
	if err != nil {
		t.Fatalf("Acknowledge: %v", err)
	}
	suspicious := 0
	for _, s := range i.snapshots() {
		if s.ID == ack.ID && s.Suspicious {
			t.Error("the acknowledged snapshot is still suspicious")
		}
		if s.Suspicious {
			suspicious++
		}
	}
	if suspicious != 1 {
		t.Errorf("suspicious snapshots left = %d, want 1", suspicious)
	}
}
