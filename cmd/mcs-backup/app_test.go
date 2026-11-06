package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func newTestApp() (*app, *bytes.Buffer, *bytes.Buffer) {
	var stdout, stderr bytes.Buffer
	return &app{
		stdout: &stdout, stderr: &stderr, stdin: strings.NewReader(""),
		getenv: func(string) string { return "" }, environ: func() []string { return nil },
		now: func() time.Time { return time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC) },
	}, &stdout, &stderr
}

func TestExitCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, exitCodeOK},
		{"plain", errors.New("boom"), exitCodeError},
		{"usage", usageError(errors.New("bad flag")), exitCodeUsage},
		{"wrapped", fmt.Errorf("ctx: %w", &exitError{code: exitCodeBusy, err: errors.New("busy")}), exitCodeBusy},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := exitCode(tt.err); got != tt.want {
				t.Errorf("exitCode() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestIsTerminal(t *testing.T) {
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if isTerminal(f) {
		t.Error("/dev/null must not count as a terminal: a kubectl exec without -t leaves it as stdin, and the TUI cannot read keys from it")
	}
}

func TestRun(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{"version", []string{"version"}, exitCodeOK, "mcs-backup dev", ""},
		{"help", nil, exitCodeOK, "Backup service around restic", ""},
		{"unknown flag", []string{"--nope"}, exitCodeUsage, "", "unknown flag"},
		{"unexpected argument", []string{"version", "x"}, exitCodeUsage, "", "unknown command"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, stdout, stderr := newTestApp()
			if got := a.run(t.Context(), tt.args); got != tt.wantCode {
				t.Errorf("exit code = %d, want %d (stderr: %s)", got, tt.wantCode, stderr)
			}
			if !strings.Contains(stdout.String(), tt.wantStdout) {
				t.Errorf("stdout = %q, want substring %q", stdout, tt.wantStdout)
			}
			if !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, want substring %q", stderr, tt.wantStderr)
			}
		})
	}
}
