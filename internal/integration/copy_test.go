//go:build integration

package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noris-network/mcs-backup/v2/internal/service"
)

// copyInst prepares an instance with a second local repository.
func copyInst(t *testing.T, extra ...string) (*inst, string) {
	t.Helper()
	home := t.TempDir()
	second := filepath.Join(home, "second")
	env := append([]string{"COPY_REPOSITORY=" + second, "COPY_PASSWORD=copy-password"}, extra...)
	return newInst(t, env...), second
}

// secondSnapshots lists the snapshots of the second repository as seen by restic.
func secondSnapshots(t *testing.T, repo string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), resticBin, "snapshots", "--json", "--no-lock")
	cmd.Env = append(os.Environ(), "RESTIC_REPOSITORY="+repo, "RESTIC_PASSWORD=copy-password")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("restic snapshots on the second repository: %v\n%s", err, out)
	}
	return string(out)
}

func TestBackupIsCopiedToTheSecondRepository(t *testing.T) {
	i, second := copyInst(t)
	i.write("a.txt", "alpha")
	i.start()

	res := i.mustBackup("with copy", false)
	if res.Snapshot == "" {
		t.Fatal("no snapshot in the result")
	}
	out := secondSnapshots(t, second)
	if got := strings.Count(out, `"hostname":"mcs-backup"`); got != 1 {
		t.Fatalf("the second repository has %d snapshots, want 1:\n%s", got, out)
	}
	for _, tag := range []string{`"mcs-backup"`, `"manual"`, `"note:with copy"`} {
		if !strings.Contains(out, tag) {
			t.Errorf("tag %s was not copied:\n%s", tag, out)
		}
	}
	st := i.status()
	if st.Copy == nil || !st.Copy.Up || st.Copy.Snapshots != 1 {
		t.Errorf("status.Copy = %+v", st.Copy)
	}
}

func TestKeepAndAcknowledgeAreMirroredToTheSecondRepository(t *testing.T) {
	i, second := copyInst(t, `RETENTION_POLICY={"last":1}`)
	i.write("a.txt", "1")
	i.start()
	i.mustBackup("one", false)

	snaps := i.snapshots()
	if _, err := i.d.Keep(i.ctx(), snaps[0].ID); err != nil {
		t.Fatalf("Keep: %v", err)
	}
	if out := secondSnapshots(t, second); !strings.Contains(out, `"keep"`) {
		t.Errorf("the second repository does not know the keep tag:\n%s", out)
	}
	// the next copy must not duplicate the snapshot whose tags changed
	i.write("a.txt", "22")
	i.mustBackup("two", false)
	if got := strings.Count(secondSnapshots(t, second), `"hostname":"mcs-backup"`); got != 2 {
		t.Errorf("the second repository has %d snapshots, want 2 (the kept one and the new one)", got)
	}
}

func TestSecondRepositoryFollowsTheRetention(t *testing.T) {
	i, second := copyInst(t, `RETENTION_POLICY={"last":1}`)
	i.write("a.txt", "1")
	i.start()
	for _, n := range []string{"1", "22", "333"} {
		i.write("a.txt", n)
		i.mustBackup(n, false)
	}
	if got := strings.Count(secondSnapshots(t, second), `"hostname":"mcs-backup"`); got != 1 {
		t.Errorf("the second repository has %d snapshots, want 1", got)
	}
}

func TestACopyFailureIsOnlyAWarning(t *testing.T) {
	i, second := copyInst(t)
	i.write("a.txt", "1")
	i.start()
	i.mustBackup("fine", false)

	// the second repository breaks
	if err := os.RemoveAll(filepath.Join(second, "keys")); err != nil {
		t.Fatal(err)
	}
	i.write("a.txt", "22")
	res := i.backup("copy fails", false)
	if res.Result != service.ResultWarning {
		t.Fatalf("result = %s (%s %s), want warning", res.Result, res.Message, res.Error)
	}
	if n := len(i.snapshots()); n != 2 {
		t.Errorf("snapshots in the main repository = %d, want 2: the backup itself succeeded", n)
	}
}
