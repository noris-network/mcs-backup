// Package hooks runs the user-provided scripts around backups and restores.
package hooks

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// killGrace is how long a script gets to react to SIGTERM before it is killed.
const killGrace = 10 * time.Second

// outputTail is how much of a script's output is kept for error messages.
const outputTail = 4 << 10

// Runner runs scripts in Dir with the environment Env.
type Runner struct {
	Dir string
	Env []string
	// Output receives every line a script prints (stdout and stderr merged).
	// It may be nil.
	Output func(name, line string)
}

// ExitError is a script that failed.
type ExitError struct {
	Name   string
	Code   int
	Output string // the last lines of output
}

func (e *ExitError) Error() string {
	msg := fmt.Sprintf("%s: exit code %d", e.Name, e.Code)
	if e.Output != "" {
		msg += ": " + lastLine(e.Output)
	}
	return msg
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}

// Run runs script with args and extraEnv (KEY=VALUE) and waits for it. name
// identifies the hook in messages. When ctx ends, the script and its child
// processes receive SIGTERM and, after a grace period, SIGKILL.
func (r *Runner) Run(ctx context.Context, name, script string, args []string, extraEnv ...string) error {
	cmd, tail, done := r.command(ctx, name, script, args, extraEnv)
	defer done()
	if err := cmd.Run(); err != nil {
		return r.wrap(ctx, name, err, tail)
	}
	return nil
}

// Pipe runs script with the data written by produce on its standard input.
// A failing producer is an error even if the script succeeded, because the
// script then received incomplete data.
func (r *Runner) Pipe(ctx context.Context, name, script string, args []string, produce func(io.Writer) error, extraEnv ...string) error {
	cmd, tail, done := r.command(ctx, name, script, args, extraEnv)
	defer done()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	produceErr := produce(stdin)
	stdin.Close() // lets the script see EOF
	waitErr := cmd.Wait()

	var errs []error
	if waitErr != nil {
		errs = append(errs, r.wrap(ctx, name, waitErr, tail))
	}
	// A script that exits early closes the pipe: the broken pipe is a
	// consequence of its failure, not a separate problem.
	if produceErr != nil && !(waitErr != nil && errors.Is(produceErr, syscall.EPIPE)) {
		errs = append(errs, produceErr)
	}
	return errors.Join(errs...)
}

func (r *Runner) command(ctx context.Context, name, script string, args, extraEnv []string) (*exec.Cmd, *tailBuffer, func()) {
	cmd := exec.CommandContext(ctx, script, args...)
	cmd.Dir = r.Dir
	cmd.Env = append(append([]string(nil), r.Env...), extraEnv...)
	// own process group so that a timeout also stops what the script started
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) }
	cmd.WaitDelay = killGrace

	tail := &tailBuffer{max: outputTail}
	lw := &lineWriter{fn: func(line string) {
		if r.Output != nil {
			r.Output(name, line)
		}
	}}
	w := io.MultiWriter(lw, tail)
	cmd.Stdout, cmd.Stderr = w, w
	return cmd, tail, lw.flush
}

func (r *Runner) wrap(ctx context.Context, name string, err error, tail *tailBuffer) error {
	var out error
	if ee, ok := errors.AsType[*exec.ExitError](err); ok {
		out = &ExitError{Name: name, Code: ee.ExitCode(), Output: tail.String()}
	} else {
		out = fmt.Errorf("%s: %w", name, err)
	}
	if cause := context.Cause(ctx); cause != nil {
		return errors.Join(cause, out)
	}
	return out
}

type tailBuffer struct {
	max int
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string { return strings.TrimSpace(string(t.buf)) }

type lineWriter struct {
	fn  func(string)
	buf []byte
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			return len(p), nil
		}
		w.fn(strings.TrimRight(string(w.buf[:i]), "\r"))
		w.buf = w.buf[i+1:]
	}
}

func (w *lineWriter) flush() {
	if len(w.buf) > 0 {
		w.fn(strings.TrimRight(string(w.buf), "\r"))
		w.buf = nil
	}
}
