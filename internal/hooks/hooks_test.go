package hooks

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func script(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hook.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

type lines struct {
	mu sync.Mutex
	v  []string
}

func (l *lines) add(name, line string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.v = append(l.v, name+": "+line)
}

func TestRun(t *testing.T) {
	var out lines
	dir := t.TempDir()
	r := &Runner{Dir: dir, Env: append(os.Environ(), "BASE=1"), Output: out.add}
	s := script(t, `echo "args: $*"; echo "cwd: $(pwd)"; echo "env: $BASE $EXTRA"; echo oops >&2`)

	err := r.Run(t.Context(), "pre-backup", s, []string{"a", "b c"}, "EXTRA=2")
	if err != nil {
		t.Fatal(err)
	}
	resolved, _ := filepath.EvalSymlinks(dir)
	want := []string{"pre-backup: args: a b c", "pre-backup: cwd: " + resolved, "pre-backup: env: 1 2", "pre-backup: oops"}
	if !slices.Equal(out.v, want) {
		t.Errorf("output = %q, want %q", out.v, want)
	}
}

func TestRunFailure(t *testing.T) {
	r := &Runner{Dir: t.TempDir()}
	err := r.Run(t.Context(), "post-backup", script(t, "echo first; echo disk full >&2; exit 3"), nil)
	ee, ok := errors.AsType[*ExitError](err)
	if !ok || ee.Code != 3 || ee.Name != "post-backup" {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "disk full") || !strings.Contains(ee.Output, "first") {
		t.Errorf("error %q / output %q should carry the script output", err, ee.Output)
	}
}

func TestRunMissingScript(t *testing.T) {
	r := &Runner{Dir: t.TempDir()}
	err := r.Run(t.Context(), "x", filepath.Join(t.TempDir(), "nope"), nil)
	if err == nil {
		t.Fatal("expected an error")
	}
	if _, ok := errors.AsType[*ExitError](err); ok {
		t.Error("a script that cannot start is not an ExitError")
	}
}

func TestRunTimeoutStopsChildren(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "child-survived")
	// the child outlives the shell unless the whole process group is stopped
	s := script(t, `(sleep 2; touch `+marker+`) & sleep 30`)
	r := &Runner{Dir: t.TempDir()}

	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := r.Run(ctx, "slow", s, nil)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want the context error", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("took %v", d)
	}
	time.Sleep(2500 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Error("the child process was not stopped")
	}
}

func TestPipe(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "received")
	r := &Runner{Dir: t.TempDir()}
	err := r.Pipe(t.Context(), "pipe-out", script(t, `cat > `+dest), nil, func(w io.Writer) error {
		_, err := io.WriteString(w, "SELECT 1;\n")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(dest); string(got) != "SELECT 1;\n" {
		t.Errorf("received %q", got)
	}
}

func TestPipeProducerFails(t *testing.T) {
	// the script accepts the truncated data and succeeds: that must still fail
	dest := filepath.Join(t.TempDir(), "received")
	r := &Runner{Dir: t.TempDir()}
	boom := errors.New("dump failed")
	err := r.Pipe(t.Context(), "pipe-out", script(t, `cat > `+dest), nil, func(w io.Writer) error {
		_, _ = io.WriteString(w, "partial")
		return boom
	})
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want the producer error", err)
	}
}

func TestPipeScriptFailsEarly(t *testing.T) {
	r := &Runner{Dir: t.TempDir()}
	err := r.Pipe(t.Context(), "pipe-out", script(t, `echo cannot connect >&2; exit 2`), nil, func(w io.Writer) error {
		chunk := make([]byte, 1<<16)
		for range 64 { // more than the pipe buffer holds
			if _, err := w.Write(chunk); err != nil {
				return err
			}
		}
		return nil
	})
	ee, ok := errors.AsType[*ExitError](err)
	if !ok || ee.Code != 2 {
		t.Fatalf("err = %v, want the script's exit error", err)
	}
	if strings.Contains(err.Error(), "broken pipe") {
		t.Errorf("the broken pipe is only a consequence and should not be reported: %v", err)
	}
}
