package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/noris-network/mcs-backup/v2/internal/api"
	"github.com/noris-network/mcs-backup/v2/internal/service"
)

// Process exit codes, part of the CLI contract (see docs).
const (
	exitCodeOK          = 0 // success
	exitCodeError       = 1 // operation failed
	exitCodeUsage       = 2 // usage or configuration error
	exitCodeUnreachable = 3 // daemon not reachable
	exitCodeBusy        = 4 // another operation is already running
)

// exitError carries the process exit code for an error.
type exitError struct {
	code int
	err  error
	// silent: the message was already shown, do not print it again.
	silent bool
}

func (e *exitError) Error() string { return e.err.Error() }
func (e *exitError) Unwrap() error { return e.err }

// usageError marks err as a usage or configuration error (exit code 2).
func usageError(err error) error {
	return &exitError{code: exitCodeUsage, err: err}
}

// isSilent reports whether the message of err was shown already.
func isSilent(err error) bool {
	ee, ok := errors.AsType[*exitError](err)
	return ok && ee.silent
}

// exitCode maps an error returned by a command to a process exit code.
func exitCode(err error) int {
	if err == nil {
		return exitCodeOK
	}
	if ee, ok := errors.AsType[*exitError](err); ok {
		return ee.code
	}
	switch {
	case errors.Is(err, api.ErrUnreachable):
		return exitCodeUnreachable
	case errors.Is(err, service.ErrBusy):
		return exitCodeBusy
	case errors.Is(err, service.ErrConfirm):
		return exitCodeUsage
	}
	return exitCodeError
}

// app holds the process environment so that commands can be tested without
// touching global state.
type app struct {
	stdout io.Writer
	stderr io.Writer
	stdin  io.Reader
	getenv func(string) string
	// environ returns the process environment (KEY=VALUE).
	environ func() []string
	// interactive reports whether questions can be asked on stdin.
	interactive bool
	// dial connects to the service on the socket; api.NewClient by default.
	dial func(socket string) service.Service
	now  func() time.Time
	// poll is how often a waiting command asks for the result.
	poll time.Duration
}

// newApp returns the app for the real process.
func newApp() *app {
	return &app{
		stdout: os.Stdout, stderr: os.Stderr, stdin: os.Stdin, getenv: os.Getenv, environ: os.Environ,
		interactive: isTerminal(os.Stdin), now: time.Now, poll: 2 * time.Second,
		dial: func(socket string) service.Service {
			return api.NewClient(socket, service.SourceCLI)
		},
	}
}

// isTerminal reports whether f is a character device, such as a terminal.
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// run executes the CLI with args (without the program name) and returns the
// process exit code.
func (a *app) run(ctx context.Context, args []string) int {
	cmd := a.newRootCmd()
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(ctx)
	if err != nil && !isSilent(err) {
		fmt.Fprintf(a.stderr, "mcs-backup: %v\n", err)
	}
	return exitCode(err)
}
