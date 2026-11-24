//go:build integration

package integration

import (
	"testing"
	"time"

	"github.com/noris-network/mcs-backup/v2/internal/config"
	"github.com/noris-network/mcs-backup/v2/internal/service"
)

// lockedForget sets the scene: a frozen restic holds a lock that is never
// renewed, and the next backup needs an exclusive lock to forget the old one.
func lockedForget(t *testing.T, unlockAfter time.Duration) (*inst, service.RunResult) {
	t.Helper()
	i := newInst(t, `RETENTION_POLICY={"last":1}`)
	// the configuration allows no lock age the test could wait for
	i.tweak = func(c *config.Config) { c.AutoUnlockAfter = unlockAfter }
	i.write("a.txt", "1")
	i.start()
	i.mustBackup("one", false)
	i.holdLock()()
	time.Sleep(3 * time.Second)
	i.write("a.txt", "22")
	return i, i.backup("two", false)
}

func TestStaleLockIsRemovedAutomatically(t *testing.T) {
	i, res := lockedForget(t, 2*time.Second)
	if res.Result != service.ResultSuccess {
		t.Fatalf("result = %s (%s %s)\n%s", res.Result, res.Message, res.Error, i.log)
	}
	if n := len(i.snapshots()); n != 1 {
		t.Errorf("snapshots = %d, want 1: the forget should have run after the unlock", n)
	}
	if locks, _ := i.d.Locks(i.ctx()); len(locks) != 0 {
		t.Errorf("locks left: %v", locks)
	}
}

func TestYoungLockIsLeftAlone(t *testing.T) {
	i, res := lockedForget(t, 10*time.Minute)
	if res.Result == service.ResultSuccess {
		t.Fatalf("the backup claims success although it could not forget: %+v", res)
	}
	if n := len(i.snapshots()); n != 2 {
		t.Errorf("snapshots = %d, want 2", n)
	}
	locks, err := i.d.Locks(i.ctx())
	if err != nil || len(locks) != 1 {
		t.Errorf("Locks() = %v, %v; the lock of the live process must stay", locks, err)
	}
}

func TestWithoutAutoUnlockTheLockStays(t *testing.T) {
	i, res := lockedForget(t, 0)
	if res.Result == service.ResultSuccess {
		t.Fatalf("result = %s, want a warning about the forget", res.Result)
	}
	locks, err := i.d.Locks(i.ctx())
	if err != nil || len(locks) != 1 {
		t.Fatalf("Locks() = %v, %v", locks, err)
	}

	// the operator removes it by hand: all locks need the confirmation word
	if err := i.d.Unlock(i.ctx(), true, "yes"); err == nil {
		t.Error("removed all locks without the confirmation word")
	}
	if err := i.d.Unlock(i.ctx(), true, service.ConfirmRemoveAll); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	if locks, _ := i.d.Locks(i.ctx()); len(locks) != 0 {
		t.Errorf("locks left: %v", locks)
	}
}
