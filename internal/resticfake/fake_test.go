package resticfake

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"slices"
	"testing"
	"time"
)

func TestFake(t *testing.T) {
	f := New(t)
	f.On(t, "snapshots", Response{Stdout: "[]\n"})
	f.On(t, "cat config", Response{Stdout: "config\n"})
	f.On(t, "backup", Response{Stderr: "boom\n", Exit: 1}, Response{Stdout: "ok\n"})

	run := func(args ...string) (string, string, int) {
		cmd := exec.CommandContext(t.Context(), f.Bin, args...)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		cmd.Env = append(cmd.Environ(), "FAKE_MARKER=yes")
		cmd.Dir = t.TempDir()
		err := cmd.Run()
		code := 0
		if ee, ok := errors.AsType[*exec.ExitError](err); ok {
			code = ee.ExitCode()
		}
		return stdout.String(), stderr.String(), code
	}

	if out, _, code := run("snapshots", "--json"); out != "[]\n" || code != 0 {
		t.Errorf("snapshots: %q, %d", out, code)
	}
	if out, _, _ := run("cat", "config"); out != "config\n" {
		t.Errorf("cat config: %q", out)
	}
	if _, errOut, code := run("backup", "a b", "c"); errOut != "boom\n" || code != 1 {
		t.Errorf("first backup: %q, %d", errOut, code)
	}
	for range 2 { // the last response repeats
		if out, _, code := run("backup"); out != "ok\n" || code != 0 {
			t.Errorf("later backup: %q, %d", out, code)
		}
	}
	if _, errOut, code := run("prune"); code != 99 || errOut == "" {
		t.Errorf("unregistered command: %q, %d", errOut, code)
	}

	wantCalls := [][]string{{"snapshots", "--json"}, {"cat", "config"}, {"backup", "a b", "c"}, {"backup"}, {"backup"}, {"prune"}}
	if got := f.Calls(t); !slices.EqualFunc(got, wantCalls, slices.Equal) {
		t.Errorf("calls = %q, want %q", got, wantCalls)
	}
	if got := f.CallsTo(t, "backup"); len(got) != 3 {
		t.Errorf("CallsTo(backup) = %q", got)
	}
	if f.LastEnv(t)["FAKE_MARKER"] != "yes" || f.LastDir(t) == "" {
		t.Errorf("env/dir not recorded: %v %q", f.LastEnv(t)["FAKE_MARKER"], f.LastDir(t))
	}
}

func TestFakeInterrupt(t *testing.T) {
	f := New(t)
	f.On(t, "backup", Response{Sleep: 30 * time.Second})

	ctx, cancel := context.WithCancel(t.Context())
	cmd := exec.CommandContext(ctx, f.Bin, "backup")
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 5 * time.Second
	time.AfterFunc(300*time.Millisecond, cancel)

	start := time.Now()
	err := cmd.Run()
	ee, ok := errors.AsType[*exec.ExitError](err)
	if !ok || ee.ExitCode() != 130 {
		t.Fatalf("err = %v, want exit code 130", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("took %v, the interrupt was not delivered", d)
	}
}
