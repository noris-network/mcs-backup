//go:build integration

package integration

import (
	"bytes"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// bin runs the compiled mcs-backup with the environment of the instance.
func (i *inst) bin(args ...string) (stdout, stderr string, code int) {
	i.t.Helper()
	cmd := exec.CommandContext(i.t.Context(), mcsBin, args...)
	cmd.Env = i.env
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	if ee, ok := errors.AsType[*exec.ExitError](err); ok {
		code = ee.ExitCode()
	} else if err != nil {
		i.t.Fatalf("mcs-backup %v: %v", args, err)
	}
	return out.String(), errb.String(), code
}

// seed makes a repository with one snapshot of the root, then empties the root.
func (i *inst) seed(files map[string]string) {
	i.t.Helper()
	i.restic("init")
	for name, content := range files {
		i.write(name, content)
	}
	// like the service: from inside the root, so that the snapshot is relative to it
	if out, err := i.resticIn(i.root, "backup", "--host", "mcs-backup", "--tag", "mcs-backup,manual", "."); err != nil {
		i.t.Fatalf("restic backup: %v\n%s", err, out)
	}
	entries, err := os.ReadDir(i.root)
	if err != nil {
		i.t.Fatal(err)
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(i.root, e.Name())); err != nil {
			i.t.Fatal(err)
		}
	}
}

func TestRestoreIfEmptyRestoresAnEmptyRoot(t *testing.T) {
	i := newInst(t)
	i.seed(map[string]string{"app/state.db": "state", "top.txt": "top"})
	out, errOut, code := i.bin("restore-if-empty")
	if code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, out, errOut)
	}
	if !i.exists("app/state.db") || !i.exists("top.txt") {
		t.Fatalf("the data was not restored:\n%s\n%s", out, errOut)
	}
	if i.read("app/state.db") != "state" || i.read("top.txt") != "top" {
		t.Errorf("wrong content restored:\n%s", out)
	}
}

func TestRestoreIfEmptyLeavesDataAloneWithoutTouchingTheRepository(t *testing.T) {
	i := newInst(t, "RESTIC_REPOSITORY=/nonexistent/repository")
	i.write("keep.txt", "mine")
	out, errOut, code := i.bin("restore-if-empty")
	if code != 0 {
		t.Fatalf("exit %d: a filled root must not need the repository\n%s\n%s", code, out, errOut)
	}
	if !strings.Contains(out, "not empty") {
		t.Errorf("output = %q", out)
	}
}

func TestRestoreIfEmptyOnAFreshInstallationIsNotAnError(t *testing.T) {
	t.Run("no repository", func(t *testing.T) {
		i := newInst(t)
		out, errOut, code := i.bin("restore-if-empty")
		if code != 0 {
			t.Fatalf("exit %d\n%s\n%s", code, out, errOut)
		}
		if _, err := os.Stat(i.repo); err == nil {
			t.Error("restore-if-empty created the repository")
		}
	})
	t.Run("no snapshot", func(t *testing.T) {
		i := newInst(t)
		i.restic("init")
		out, errOut, code := i.bin("restore-if-empty")
		if code != 0 {
			t.Fatalf("exit %d\n%s\n%s", code, out, errOut)
		}
	})
}

func TestRestoreIfEmptyFailsWhenTheRepositoryCannotBeRead(t *testing.T) {
	i := newInst(t)
	i.seed(map[string]string{"a.txt": "a"})
	i.set("RESTIC_PASSWORD=wrong")
	if out, errOut, code := i.bin("restore-if-empty"); code != 1 {
		t.Fatalf("exit %d, want 1 so that an init container is run again\n%s\n%s", code, out, errOut)
	}
}

func TestRestoreIfEmptyRefusesPipeMode(t *testing.T) {
	i := newInst(t)
	in := i.script("in.sh", "echo x")
	i.set("PIPE_IN_SCRIPT="+in, "PIPE_OUT_SCRIPT="+in)
	if _, _, code := i.bin("restore-if-empty"); code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
}

func TestConfigCheckReportsMissingSettings(t *testing.T) {
	i := newInst(t)
	i.set("RESTIC_PASSWORD=")
	_, errOut, code := i.bin("config", "check")
	if code != 2 || !strings.Contains(errOut, "RESTIC_PASSWORD") {
		t.Errorf("exit %d, stderr %q", code, errOut)
	}
}

// serve starts the binary as a service and waits until its API answers.
func (i *inst) serve() *exec.Cmd {
	i.t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0") //nolint:noctx // only to find a free port
	if err != nil {
		i.t.Fatal(err)
	}
	port := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	_ = ln.Close()
	i.set("BACKUP_HTTP_PORT="+port, "MCS_BACKUP_LOG_FORMAT=text")
	cmd := exec.Command(mcsBin, "serve") //nolint:gosec,noctx // stopped by the test with SIGTERM
	cmd.Env = i.env
	logw := &syncBuf{}
	cmd.Stdout, cmd.Stderr = logw, logw
	if err := cmd.Start(); err != nil {
		i.t.Fatal(err)
	}
	i.t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if os.Getenv("IT_LOG") != "" {
			i.t.Logf("serve log:\n%s", logw)
		}
	})
	i.waitFor("mcs-backup serve", 30*time.Second, func() bool {
		out, _, code := i.bin("status", "--json")
		return code == 0 && strings.Contains(out, `"ready": true`) || code == 0 && strings.Contains(out, `"ready":true`)
	})
	return cmd
}

func TestSIGTERMStopsTheOperationAndReleasesTheLock(t *testing.T) {
	i := newInst(t, "SHUTDOWN_TIMEOUT=20s")
	started := filepath.Join(i.home, "started")
	in := i.script("dump.sh", "touch "+started+"; sleep 300; echo never")
	i.set("PIPE_IN_SCRIPT="+in, "PIPE_OUT_SCRIPT="+in)
	cmd := i.serve()

	if _, errOut, code := i.bin("backup", "--no-wait"); code != 0 {
		t.Fatalf("backup: exit %d %s", code, errOut)
	}
	i.waitFor("restic to hold its lock", 30*time.Second, func() bool {
		out, err := i.resticErr("list", "locks", "--no-lock")
		return err == nil && strings.TrimSpace(out) != ""
	})

	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done: //nolint:errcheck // the exit status after SIGTERM does not matter here
	case <-time.After(30 * time.Second):
		t.Fatal("the service did not stop on SIGTERM")
	}
	if out := i.restic("list", "locks", "--no-lock"); strings.TrimSpace(out) != "" {
		t.Errorf("the lock is still there after the shutdown:\n%s", out)
	}
	if n := strings.Count(i.restic("snapshots", "--json"), `"hostname"`); n != 0 {
		t.Errorf("the interrupted backup left %d snapshots", n)
	}
}

func TestCLIAgainstARunningService(t *testing.T) {
	i := newInst(t)
	i.write("a.txt", "a")
	i.serve()

	if out, errOut, code := i.bin("backup", "--note", "from the cli"); code != 0 {
		t.Fatalf("backup: exit %d\n%s\n%s", code, out, errOut)
	}
	out, _, code := i.bin("snapshots")
	if code != 0 || !strings.Contains(out, "from the cli") {
		t.Errorf("snapshots: exit %d\n%s", code, out)
	}
	i.remove("a.txt")
	if _, _, code := i.bin("restore", "latest"); code != 2 {
		t.Errorf("an in-place restore without confirmation: exit %d, want 2", code)
	}
	if out, errOut, code := i.bin("restore", "--confirm", "latest"); code != 0 {
		t.Fatalf("restore: exit %d\n%s\n%s", code, out, errOut)
	}
	if i.read("a.txt") != "a" {
		t.Error("the file was not restored")
	}
	// a second service on the same socket is refused
	if _, errOut, code := i.bin("serve"); code == 0 || !strings.Contains(errOut, "already") {
		t.Errorf("second serve: exit %d, stderr %q", code, errOut)
	}
}

func TestCLIWithoutAServiceExitsWithThree(t *testing.T) {
	i := newInst(t)
	if _, _, code := i.bin("status"); code != 3 {
		t.Errorf("exit %d, want 3", code)
	}
}
