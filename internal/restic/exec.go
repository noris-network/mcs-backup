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
	"time"

	"github.com/noris-network/mcs-backup/v2/internal/lineio"
)

// interruptGrace is how long restic gets to release its lock after SIGINT
// before it is killed.
const interruptGrace = 15 * time.Second

// stderrTail is how much of restic's stderr is kept for error messages.
const stderrTail = 8 << 10

// maxLine is the longest line of restic output handled in one piece. restic
// writes one JSON message per line, far shorter than this; the limit only
// keeps output without line breaks from filling the memory. A longer line is
// handled in pieces, which the JSON parsers skip as unreadable.
const maxLine = 1 << 20

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

// positional appends positional arguments behind a "--" terminator. restic
// takes flags anywhere on the command line, so a path or snapshot that starts
// with "-" would otherwise be read as a flag, such as --password-command.
func positional(args []string, pos ...string) []string {
	return append(append(args, "--"), pos...)
}

// run options
type runOpts struct {
	env        []string
	stdin      io.Reader
	stdout     io.Writer // raw stdout; captured if nil and no stdoutLine
	stdoutLine func([]byte)
	stderrLine func([]byte)
	// kill is asked for every line of stderr; when it says true the process is
	// killed at once. Output that a dead end produces must not wait for the
	// process, which would retry for minutes or run out its grace after the
	// interrupt. What restic has written up to then is in the tail.
	kill func(line []byte) bool
	// combined receives stdout and stderr (for text output commands).
	combined io.Writer
}

// run executes restic and returns captured stdout.
func (r *Repo) run(ctx context.Context, args []string, o runOpts) ([]byte, error) {
	cmd := r.cmd(ctx, args, o.env...)
	cmd.Stdin = o.stdin

	var captured bytes.Buffer
	tail := &lineio.Tail{Max: stderrTail}
	var closers []func()

	switch {
	case o.combined != nil:
		w := io.MultiWriter(o.combined, tail)
		cmd.Stdout, cmd.Stderr = w, w
	default:
		switch {
		case o.stdoutLine != nil:
			lw := jsonLines(o.stdoutLine)
			closers = append(closers, lw.Flush)
			cmd.Stdout = lw
		case o.stdout != nil:
			cmd.Stdout = o.stdout
		default:
			cmd.Stdout = &captured
		}
		if o.stderrLine != nil || o.kill != nil {
			lw := jsonLines(func(line []byte) {
				if o.kill != nil && o.kill(line) {
					_ = cmd.Process.Kill() // idempotent: killing a dead process again is not an error
				}
				if o.stderrLine != nil {
					o.stderrLine(line)
				}
			})
			closers = append(closers, lw.Flush)
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

// jsonLines returns a writer for output that has one message per line: fn
// receives every line that is not empty, without the white space around it.
func jsonLines(fn func([]byte)) *lineio.Writer {
	return &lineio.Writer{Max: maxLine, Fn: func(line []byte) {
		if line = bytes.TrimSpace(line); len(line) > 0 {
			fn(line)
		}
	}}
}
