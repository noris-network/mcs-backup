// Package restic runs the restic binary and parses its output.
package restic

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"
)

// interruptGrace is how long restic gets to release its lock after SIGINT
// before it is killed.
const interruptGrace = 15 * time.Second

// stderrTail is how much of restic's stderr is kept for error messages.
const stderrTail = 8 << 10

// Options configures a Repo.
type Options struct {
	// Bin is the restic executable, "restic" if empty.
	Bin string
	// Env is the complete environment of restic processes (KEY=VALUE).
	Env []string
	// Dir is the working directory of backups and restores.
	Dir string
	// GlobalArgs are added to every invocation, e.g. -o s3.list-objects-v1=true.
	GlobalArgs []string
	Logger     *slog.Logger
}

// Repo runs restic commands against one repository.
type Repo struct {
	bin    string
	env    []string
	dir    string
	global []string
	log    *slog.Logger
}

// New returns a Repo for the repository selected by o.Env.
func New(o Options) *Repo {
	r := &Repo{bin: o.Bin, env: o.Env, dir: o.Dir, global: o.GlobalArgs, log: o.Logger}
	if r.bin == "" {
		r.bin = "restic"
	}
	if r.log == nil {
		r.log = slog.New(slog.DiscardHandler)
	}
	return r
}

// cmd builds a restic process. Global arguments go right after the subcommand
// so that they cannot end up behind a "--" terminator.
func (r *Repo) cmd(ctx context.Context, args []string, extraEnv ...string) *exec.Cmd {
	full := make([]string, 0, len(args)+len(r.global))
	full = append(full, args[0])
	full = append(full, r.global...)
	full = append(full, args[1:]...)

	cmd := exec.CommandContext(ctx, r.bin, full...) //nolint:gosec // running the configured restic binary is the purpose
	cmd.Env = append(slices.Clone(r.env), extraEnv...)
	cmd.Dir = r.dir
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = interruptGrace
	return cmd
}

// run options
type runOpts struct {
	env        []string
	stdin      io.Reader
	stdout     io.Writer // raw stdout; captured if nil and no stdoutLine
	stdoutLine func([]byte)
	stderrLine func([]byte)
	// combined receives stdout and stderr (for text output commands).
	combined io.Writer
}

// run executes restic and returns captured stdout.
func (r *Repo) run(ctx context.Context, args []string, o runOpts) ([]byte, error) {
	cmd := r.cmd(ctx, args, o.env...)
	cmd.Stdin = o.stdin

	var captured bytes.Buffer
	tail := &tailBuffer{max: stderrTail}
	var closers []func()

	switch {
	case o.combined != nil:
		w := io.MultiWriter(o.combined, tail)
		cmd.Stdout, cmd.Stderr = w, w
	default:
		switch {
		case o.stdoutLine != nil:
			lw := &lineWriter{fn: o.stdoutLine}
			closers = append(closers, lw.flush)
			cmd.Stdout = lw
		case o.stdout != nil:
			cmd.Stdout = o.stdout
		default:
			cmd.Stdout = &captured
		}
		if o.stderrLine != nil {
			lw := &lineWriter{fn: o.stderrLine}
			closers = append(closers, lw.flush)
			cmd.Stderr = io.MultiWriter(lw, tail)
		} else {
			cmd.Stderr = tail
		}
	}

	r.log.DebugContext(ctx, "run restic", "args", args)
	err := cmd.Run()
	for _, c := range closers {
		c()
	}
	if err != nil {
		return captured.Bytes(), r.wrapError(ctx, args[0], tail.String(), err)
	}
	return captured.Bytes(), nil
}

// wrapError turns a process failure into an ExitError, keeping the context
// error visible when the run was cancelled or timed out.
func (r *Repo) wrapError(ctx context.Context, command, stderr string, err error) error {
	var out error
	if ee, ok := errors.AsType[*exec.ExitError](err); ok {
		out = &ExitError{Command: command, Code: ee.ExitCode(), Message: errorMessage(stderr)}
	} else {
		out = fmt.Errorf("restic %s: %w", command, err)
	}
	if cause := context.Cause(ctx); cause != nil {
		return errors.Join(cause, out)
	}
	return out
}

// tailBuffer keeps the last max bytes written to it.
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

// lineWriter calls fn for every complete line written to it.
type lineWriter struct {
	fn  func([]byte)
	buf []byte
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			return len(p), nil
		}
		if line := bytes.TrimSpace(w.buf[:i]); len(line) > 0 {
			w.fn(line)
		}
		w.buf = w.buf[i+1:]
	}
}

// flush handles a final line without newline.
func (w *lineWriter) flush() {
	if line := bytes.TrimSpace(w.buf); len(line) > 0 {
		w.fn(line)
	}
	w.buf = nil
}
