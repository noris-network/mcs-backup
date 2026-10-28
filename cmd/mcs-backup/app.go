package main

import (
	"context"
	"errors"
	"fmt"
	"io"
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
}

func (e *exitError) Error() string { return e.err.Error() }
func (e *exitError) Unwrap() error { return e.err }

// usageError marks err as a usage or configuration error (exit code 2).
func usageError(err error) error {
	return &exitError{code: exitCodeUsage, err: err}
}

// exitCode maps an error returned by a command to a process exit code.
func exitCode(err error) int {
	if err == nil {
		return exitCodeOK
	}
	if ee, ok := errors.AsType[*exitError](err); ok {
		return ee.code
	}
	return exitCodeError
}

// app holds the process environment so that commands can be tested without
// touching global state.
type app struct {
	stdout io.Writer
	stderr io.Writer
	getenv func(string) string
}

// run executes the CLI with args (without the program name) and returns the
// process exit code.
func (a *app) run(ctx context.Context, args []string) int {
	cmd := a.newRootCmd()
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(ctx)
	if err != nil {
		fmt.Fprintf(a.stderr, "mcs-backup: %v\n", err)
	}
	return exitCode(err)
}
