package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// In a directory where everybody may create files, the state directory is
// only used if it is a directory of the service that nobody else can write
// to; on a volume the administrator decides.
func TestPrivateDir(t *testing.T) {
	shared := filepath.Join(t.TempDir(), "tmp")
	if err := os.Mkdir(shared, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(shared, 0o777|os.ModeSticky); err != nil { //nolint:gosec // stands for /tmp
		t.Fatal(err)
	}
	mkdir := func(t *testing.T, dir string, mode os.FileMode) string {
		t.Helper()
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, mode); err != nil {
			t.Fatal(err)
		}
		return dir
	}

	t.Run("a missing directory is created for the service alone", func(t *testing.T) {
		dir := filepath.Join(shared, "new")
		if err := privateDir(dir); err != nil {
			t.Fatal(err)
		}
		if fi, err := os.Stat(dir); err != nil || fi.Mode().Perm() != 0o700 {
			t.Errorf("mode = %v, %v", fi.Mode(), err)
		}
	})
	t.Run("a directory others may write to is refused", func(t *testing.T) {
		err := privateDir(mkdir(t, filepath.Join(shared, "open"), 0o777))
		if err == nil || !strings.Contains(err.Error(), "others may write") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("a link to a directory is refused", func(t *testing.T) {
		link := filepath.Join(shared, "link")
		if err := os.Symlink(t.TempDir(), link); err != nil {
			t.Fatal(err)
		}
		if err := privateDir(link); err == nil || !strings.Contains(err.Error(), "not a directory") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("on a volume a directory of a group is fine", func(t *testing.T) {
		if err := privateDir(mkdir(t, filepath.Join(t.TempDir(), "volume"), 0o770|os.ModeSetgid)); err != nil {
			t.Error(err)
		}
	})
}

func TestARefusedStateDirectoryIsNotUsed(t *testing.T) {
	shared := filepath.Join(t.TempDir(), "tmp")
	planted := filepath.Join(shared, "mcs-backup")
	if err := os.MkdirAll(planted, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{shared, planted} {
		if err := os.Chmod(dir, 0o777); err != nil { //nolint:gosec // somebody else made it
			t.Fatal(err)
		}
	}
	// a maintenance window that would stop the backups for years
	window := `{"maintenance_until":"2099-01-01T00:00:00Z"}`
	if err := os.WriteFile(filepath.Join(planted, stateFile), []byte(window), 0o600); err != nil {
		t.Fatal(err)
	}

	d := newTestDaemon(t, nil, "MCS_BACKUP_STATE_DIR="+planted)
	d.loadState()
	d.mu.Lock()
	until := d.maintUntil
	d.mu.Unlock()
	if !until.IsZero() {
		t.Errorf("the planted maintenance window was taken over: %v", until)
	}
	d.saveRuns()
	if _, err := os.Stat(filepath.Join(planted, runsFile)); !os.IsNotExist(err) {
		t.Errorf("the last runs were written to the refused directory: %v", err)
	}
}
