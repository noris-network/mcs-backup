package main

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"iter"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/noris-network/mcs-backup/v2/internal/api"
	"github.com/noris-network/mcs-backup/v2/internal/service"
	"github.com/noris-network/mcs-backup/v2/internal/servicetest"
)

var cliNow = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

// cli runs the command line against a fake service.
type cli struct {
	t      *testing.T
	fake   *servicetest.Fake
	stdin  string
	tty    bool
	env    []string
	stdout bytes.Buffer
	stderr bytes.Buffer
}

func newCLI(t *testing.T) *cli {
	t.Helper()
	return &cli{t: t, fake: &servicetest.Fake{}}
}

func (c *cli) run(args ...string) int {
	return c.runCtx(c.t.Context(), args...)
}

func (c *cli) runCtx(ctx context.Context, args ...string) int {
	c.t.Helper()
	c.stdout.Reset()
	c.stderr.Reset()
	a := &app{
		stdout: &c.stdout, stderr: &c.stderr, stdin: strings.NewReader(c.stdin),
		getenv: func(string) string { return "" }, environ: func() []string { return c.env },
		interactive: c.tty, dial: func(string) service.Service { return c.fake },
		now: func() time.Time { return cliNow }, poll: 10 * time.Millisecond,
	}
	return a.run(ctx, args)
}

func (c *cli) out() string { return c.stdout.String() }
func (c *cli) err() string { return c.stderr.String() }

// finishing makes the fake report the operation as ended with result.
func (c *cli) finishing(kind service.Kind, result service.Result, mod ...func(*service.RunResult)) {
	run := service.RunResult{ID: "op1", Kind: kind, Result: result, Duration: service.Duration(65 * time.Second), Started: cliNow}
	for _, m := range mod {
		m(&run)
	}
	c.fake.StatusFn = func(context.Context) (service.Status, error) {
		return service.Status{LastRuns: map[service.Kind]service.RunResult{kind: run}}, nil
	}
}

func TestStatus(t *testing.T) {
	c := newCLI(t)
	c.fake.StatusFn = func(context.Context) (service.Status, error) {
		return service.Status{
			Version: "v2.0.0", Host: "db1", StartedAt: cliNow.Add(-3 * time.Hour),
			Repository: service.RepoStatus{Up: true, ID: "0123456789abcdef", Snapshots: 12, SizeBytes: 4 << 30, LastSuccess: cliNow.Add(-5 * time.Hour)},
			Copy:       &service.RepoStatus{Up: false, Error: "connection refused", Snapshots: -1, SizeBytes: -1},
			Schedule:   service.ScheduleStatus{Enabled: true, Backup: "0 2 * * *", Next: map[service.Kind]time.Time{service.KindBackup: cliNow.Add(14 * time.Hour)}},
			Retention:  "the last 10 snapshots", Operation: &service.Operation{Kind: service.KindBackup, Source: "schedule", Started: cliNow.Add(-2 * time.Minute), Phase: "backup", Progress: &service.Progress{Percent: 0.42}},
			LastRuns: map[service.Kind]service.RunResult{
				service.KindBackup: {Kind: service.KindBackup, Result: service.ResultSuccess, Started: cliNow.Add(-5 * time.Hour), Duration: service.Duration(90 * time.Second), Snapshot: "abcdef0123"},
				service.KindVerify: {Kind: service.KindVerify, Result: service.ResultFailure, Started: cliNow.Add(-26 * time.Hour)},
			},
			Warnings: []service.Warning{{Code: "copy_down", Severity: service.SeverityWarning, Message: "the secondary repository is not reachable"}},
		}, nil
	}
	if code := c.run("status"); code != 0 {
		t.Fatalf("exit %d: %s", code, c.err())
	}
	for _, want := range []string{
		"v2.0.0, host db1, up 3h", "reachable, id 01234567, 12 snapshots, 4.0 GiB", "5h ago",
		"NOT REACHABLE", "backups enabled (0 2 * * *)", "next backup", "in 14h", "keeps the last 10 snapshots",
		"backup running for 2m (started by schedule), phase backup, 42%",
		"success", "snapshot abcdef01", "failure", "the secondary repository is not reachable",
	} {
		if !strings.Contains(c.out(), want) {
			t.Errorf("status output lacks %q:\n%s", want, c.out())
		}
	}

	if code := c.run("--json", "status"); code != 0 {
		t.Fatal(code)
	}
	var st service.Status
	if err := json.Unmarshal(c.stdout.Bytes(), &st); err != nil || st.Version != "v2.0.0" || st.Repository.Snapshots != 12 {
		t.Errorf("JSON status: %v\n%s", err, c.out())
	}
}

func TestExitCodes(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
		msg  string
	}{
		{"unreachable", fmt.Errorf("%w at /tmp/x.sock: no such file", api.ErrUnreachable), exitCodeUnreachable, "cannot reach"},
		{"busy", &service.BusyError{Current: service.Operation{Kind: service.KindRestore, Started: cliNow}}, exitCodeBusy, "another operation is running: restore"},
		{"confirm", service.ErrConfirm, exitCodeUsage, "confirmation"},
		{"other", errors.New("boom"), exitCodeError, "boom"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newCLI(t)
			c.fake.StatusFn = func(context.Context) (service.Status, error) { return service.Status{}, tt.err }
			if code := c.run("status"); code != tt.want {
				t.Errorf("exit code = %d, want %d", code, tt.want)
			}
			if !strings.Contains(c.err(), tt.msg) {
				t.Errorf("stderr = %q, want %q", c.err(), tt.msg)
			}
		})
	}

	c := newCLI(t)
	for _, args := range [][]string{{"status", "extra"}, {"nonsense"}, {"--wat"}, {"keep"}, {"log", "bananas"}, {"snapshots", "-x"}} {
		if code := c.run(args...); code != exitCodeUsage {
			t.Errorf("%v: exit code = %d, want %d (%s)", args, code, exitCodeUsage, c.err())
		}
	}
	if len(c.fake.Calls()) != 0 {
		t.Errorf("the service was called for invalid usage: %v", c.fake)
	}
}

func TestSnapshots(t *testing.T) {
	c := newCLI(t)
	c.fake.SnapshotsFn = func(context.Context) ([]service.Snapshot, error) {
		return []service.Snapshot{
			{ID: "aaaaaaaaaaaa", ShortID: "aaaaaaaa", Time: cliNow.Add(-time.Hour), Origin: service.OriginScheduled, HasSummary: true, Files: 1200, Bytes: 5 << 20, Keep: true, Note: "before upgrade"},
			{ID: "bbbbbbbbbbbb", ShortID: "bbbbbbbb", Time: cliNow.Add(-48 * time.Hour), Suspicious: true},
		}, nil
	}
	if code := c.run("snapshots"); code != 0 {
		t.Fatal(code, c.err())
	}
	for _, want := range []string{"aaaaaaaa", "scheduled", "1200", "5.0 MiB", "keep", "before upgrade", "SUSPICIOUS", "1h", "2d"} {
		if !strings.Contains(c.out(), want) {
			t.Errorf("output lacks %q:\n%s", want, c.out())
		}
	}
	c.fake.SnapshotsFn = nil
	if c.run("snapshots"); !strings.Contains(c.out(), "no snapshots yet") {
		t.Errorf("output = %q", c.out())
	}
	c.fake.SnapshotsFn = func(context.Context) ([]service.Snapshot, error) { return []service.Snapshot{{ID: "x"}}, nil }
	c.run("snapshots", "--json")
	var got []service.Snapshot
	if err := json.Unmarshal(c.stdout.Bytes(), &got); err != nil || len(got) != 1 {
		t.Errorf("JSON: %v %s", err, c.out())
	}
}

func TestBackupWaitsAndReportsTheResult(t *testing.T) {
	c := newCLI(t)
	c.fake.StartBackupFn = func(context.Context, service.BackupRequest) (service.Operation, error) {
		return service.Operation{ID: "op1", Kind: service.KindBackup}, nil
	}
	c.fake.EventsFn = func(context.Context) iter.Seq2[service.Event, error] {
		return func(yield func(service.Event, error) bool) {
			op := &service.Operation{ID: "op1", Kind: service.KindBackup, Phase: "backup"}
			for _, ev := range []service.Event{
				{Type: service.EventPhase, Operation: op},
				{Type: service.EventLog, Line: "using snapshot 1234 as parent"},
				{Type: service.EventPhase, Operation: &service.Operation{ID: "other", Phase: "ignored"}},
				{Type: service.EventFinished, Result: &service.RunResult{ID: "other", Kind: service.KindBackup, Result: service.ResultFailure}},
				{Type: service.EventFinished, Result: &service.RunResult{ID: "op1", Kind: service.KindBackup, Result: service.ResultSuccess, Snapshot: "abcdef0123", Message: "snapshot abcdef01 created", Duration: service.Duration(83 * time.Second)}},
			} {
				if !yield(ev, nil) {
					return
				}
			}
		}
	}
	if code := c.run("backup", "--note", "before upgrade", "--keep"); code != 0 {
		t.Fatalf("exit %d: %s", code, c.err())
	}
	if req := c.fake.CallsTo("StartBackup")[0].Args[0].(service.BackupRequest); req.Note != "before upgrade" || !req.Keep {
		t.Errorf("request = %+v", req)
	}
	if !strings.Contains(c.err(), "▸ Backing up") || !strings.Contains(c.err(), "    incremental to snapshot 1234") || strings.Contains(c.err(), "ignored") {
		t.Errorf("stderr should carry the log of this operation only:\n%s", c.err())
	}
	for _, want := range []string{"✓ backup: success (1m)", "snapshot abcdef01 created"} {
		if !strings.Contains(c.out(), want) {
			t.Errorf("stdout lacks %q:\n%s", want, c.out())
		}
	}
}

func TestFollowShowsThePhaseTheStreamStartsIn(t *testing.T) {
	c := newCLI(t)
	c.fake.StartBackupFn = func(context.Context, service.BackupRequest) (service.Operation, error) {
		return service.Operation{ID: "op1", Kind: service.KindBackup}, nil
	}
	c.fake.EventsFn = func(context.Context) iter.Seq2[service.Event, error] {
		return func(yield func(service.Event, error) bool) {
			op := &service.Operation{ID: "op1", Kind: service.KindBackup, Phase: "copy"}
			for _, ev := range []service.Event{
				{Type: service.EventStarted, Operation: op},
				{Type: service.EventLog, Line: "snapshot 63565d3f saved"},
				{Type: service.EventLog, Line: "[0:00]          0 packs copied"},
				{Type: service.EventFinished, Result: &service.RunResult{ID: "op1", Kind: service.KindBackup, Result: service.ResultWarning, Warnings: []string{"copy was slow"}}},
			} {
				if !yield(ev, nil) {
					return
				}
			}
		}
	}
	if code := c.run("backup"); code != 0 {
		t.Fatalf("exit %d: %s", code, c.err())
	}
	if want := "▸ Copying to the secondary repository\n    saved as snapshot 63565d3f\n"; c.err() != want {
		t.Errorf("stderr = %q, want %q", c.err(), want)
	}
	if !strings.Contains(c.out(), "! backup: warning") || !strings.Contains(c.out(), "warning: copy was slow") {
		t.Errorf("stdout = %q", c.out())
	}
}

func TestBackupFailureExitCodes(t *testing.T) {
	for result, want := range map[service.Result]int{
		service.ResultSuccess: 0, service.ResultWarning: 0, service.ResultSkipped: 0,
		service.ResultFailure: 1, service.ResultTimeout: 1, service.ResultSuspicious: 1, service.ResultCancelled: 1,
	} {
		t.Run(string(result), func(t *testing.T) {
			c := newCLI(t)
			c.finishing(service.KindBackup, result, func(r *service.RunResult) { r.Error = "disk full"; r.Warnings = []string{"copy failed"} })
			if code := c.run("backup"); code != want {
				t.Errorf("exit code = %d, want %d", code, want)
			}
			if !strings.Contains(c.out(), "backup: "+string(result)) || !strings.Contains(c.out(), "warning: copy failed") {
				t.Errorf("stdout = %q", c.out())
			}
			if strings.Contains(c.err(), "mcs-backup:") {
				t.Errorf("the failure was shown already, stderr = %q", c.err())
			}
		})
	}
}

func TestBackupFollowsViaStatusWhenTheStreamFails(t *testing.T) {
	c := newCLI(t)
	c.fake.EventsFn = func(context.Context) iter.Seq2[service.Event, error] {
		return func(yield func(service.Event, error) bool) { yield(service.Event{}, errors.New("stream dropped")) }
	}
	calls := 0
	c.fake.StatusFn = func(context.Context) (service.Status, error) {
		calls++
		if calls < 3 {
			return service.Status{}, nil
		}
		return service.Status{LastRuns: map[service.Kind]service.RunResult{service.KindBackup: {ID: "op1", Kind: service.KindBackup, Result: service.ResultSuccess}}}, nil
	}
	c.fake.LogFn = func(context.Context, service.Kind) (service.Log, error) {
		return service.Log{Lines: []string{"line from the log"}}, nil
	}
	if code := c.run("backup"); code != 0 {
		t.Fatalf("exit %d: %s", code, c.err())
	}
	if calls < 3 || !strings.Contains(c.err(), "line from the log") {
		t.Errorf("status polls = %d, stderr = %q: the log of an operation that ended unseen is shown afterwards", calls, c.err())
	}
}

func TestBackupJSONAndNoWait(t *testing.T) {
	c := newCLI(t)
	c.finishing(service.KindBackup, service.ResultWarning, func(r *service.RunResult) { r.Snapshot = "abc" })
	if code := c.run("--json", "backup"); code != 0 {
		t.Fatal(code, c.err())
	}
	var res service.RunResult
	if err := json.Unmarshal(c.stdout.Bytes(), &res); err != nil || res.Result != service.ResultWarning || res.Snapshot != "abc" {
		t.Errorf("JSON result: %v\n%s", err, c.out())
	}
	if strings.Contains(c.err(), "--") {
		t.Errorf("--json prints no progress: %q", c.err())
	}

	c = newCLI(t)
	if code := c.run("backup", "--no-wait"); code != 0 || !strings.Contains(c.out(), "backup started (operation op1)") {
		t.Errorf("--no-wait: exit %d, stdout %q", code, c.out())
	}
	if len(c.fake.CallsTo("Events")) != 0 || len(c.fake.CallsTo("Status")) != 0 {
		t.Error("--no-wait must not wait")
	}
}

func TestBackupInterrupted(t *testing.T) {
	c := newCLI(t)
	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(50*time.Millisecond, cancel)
	if code := c.runCtx(ctx, "backup"); code != exitCodeError {
		t.Errorf("exit code = %d", code)
	}
	if !strings.Contains(c.err(), "keeps running in the service") {
		t.Errorf("stderr = %q", c.err())
	}
}

func TestBackupBusy(t *testing.T) {
	c := newCLI(t)
	c.fake.StartBackupFn = func(context.Context, service.BackupRequest) (service.Operation, error) {
		return service.Operation{}, &service.BusyError{Current: service.Operation{Kind: service.KindPrune, Started: cliNow}}
	}
	if code := c.run("backup"); code != exitCodeBusy || !strings.Contains(c.err(), "prune") {
		t.Errorf("exit %d, stderr %q", code, c.err())
	}
}

func TestScheduleOptions(t *testing.T) {
	tests := []struct {
		args   []string
		want   service.ScheduleRequest
		output string
	}{
		{[]string{"backup", "--disable"}, service.ScheduleRequest{Action: service.ScheduleDisable}, "disabled"},
		{[]string{"backup", "--enable"}, service.ScheduleRequest{Action: service.ScheduleEnable}, "enabled"},
		{[]string{"backup", "--maintenance", "90m"}, service.ScheduleRequest{Action: service.ScheduleMaintenance, For: service.Duration(90 * time.Minute)}, "paused for maintenance"},
	}
	for _, tt := range tests {
		c := newCLI(t)
		c.fake.SetScheduleFn = func(_ context.Context, req service.ScheduleRequest) (service.Status, error) {
			st := service.Status{Schedule: service.ScheduleStatus{Enabled: req.Action != service.ScheduleDisable}}
			if req.Action == service.ScheduleMaintenance {
				st.Schedule.MaintenanceUntil = cliNow.Add(req.For.Std())
			}
			return st, nil
		}
		if code := c.run(tt.args...); code != 0 {
			t.Fatalf("%v: exit %d: %s", tt.args, code, c.err())
		}
		if got := c.fake.CallsTo("SetSchedule")[0].Args[0].(service.ScheduleRequest); got != tt.want {
			t.Errorf("%v: request = %+v, want %+v", tt.args, got, tt.want)
		}
		if !strings.Contains(c.out(), tt.output) || len(c.fake.CallsTo("StartBackup")) != 0 {
			t.Errorf("%v: stdout %q, calls %v", tt.args, c.out(), c.fake)
		}
	}

	c := newCLI(t)
	c.fake.StatusFn = func(context.Context) (service.Status, error) {
		return service.Status{Schedule: service.ScheduleStatus{Enabled: false}}, nil
	}
	if code := c.run("backup", "--status"); code != 0 || !strings.Contains(c.out(), "disabled") || len(c.fake.CallsTo("SetSchedule")) != 0 {
		t.Errorf("--status: exit %d, stdout %q", code, c.out())
	}

	for _, args := range [][]string{
		{"backup", "--enable", "--disable"}, {"backup", "--maintenance", "soon"}, {"backup", "--maintenance", "-1h"},
		{"backup", "--disable", "--note", "x"}, {"backup", "--status", "--keep"},
	} {
		c := newCLI(t)
		if code := c.run(args...); code != exitCodeUsage {
			t.Errorf("%v: exit %d, want %d", args, code, exitCodeUsage)
		}
		if len(c.fake.Calls()) != 0 {
			t.Errorf("%v: the service was called", args)
		}
	}
}

func preflight(inPlace bool) func(context.Context, service.RestoreRequest) (service.RestorePreflight, error) {
	return func(context.Context, service.RestoreRequest) (service.RestorePreflight, error) {
		return service.RestorePreflight{
			Snapshot: service.Snapshot{ShortID: "abcdef01", Time: cliNow.Add(-time.Hour), Note: "nightly"},
			Target:   "/mnt", InPlace: inPlace, Mode: service.ModeExact, SafetySnapshot: inPlace,
			NeedBytes: 5 << 20, FreeBytes: 1 << 30, Hooks: []string{"pre-restore"},
		}, nil
	}
}

func TestRestoreInPlaceNeedsConfirmation(t *testing.T) {
	t.Run("without a terminal and without --confirm", func(t *testing.T) {
		c := newCLI(t)
		c.fake.RestorePreflightFn = preflight(true)
		if code := c.run("restore"); code != exitCodeUsage || !strings.Contains(c.err(), "--confirm") {
			t.Errorf("exit %d, stderr %q", code, c.err())
		}
		if len(c.fake.CallsTo("StartRestore")) != 0 {
			t.Error("the restore was started without confirmation")
		}
		for _, want := range []string{"abcdef01", "in place", "replaces the current data", "a snapshot of the current data is taken first", "pre-restore", "note: nightly"} {
			if !strings.Contains(c.out(), want) {
				t.Errorf("the preflight lacks %q:\n%s", want, c.out())
			}
		}
	})

	t.Run("with --confirm", func(t *testing.T) {
		c := newCLI(t)
		c.fake.RestorePreflightFn = preflight(true)
		c.finishing(service.KindRestore, service.ResultSuccess)
		if code := c.run("restore", "--confirm", "abcdef01", "db1", "db2"); code != 0 {
			t.Fatalf("exit %d: %s", code, c.err())
		}
		req := c.fake.CallsTo("StartRestore")[0].Args[0].(service.RestoreRequest)
		if req.Snapshot != "abcdef01" || req.Confirm != "abcdef01" || len(req.HookArgs) != 2 || req.HookArgs[1] != "db2" || req.Target != "" {
			t.Errorf("request = %+v", req)
		}
	})

	t.Run("interactive", func(t *testing.T) {
		c := newCLI(t)
		c.tty, c.stdin = true, "abcdef01\n"
		c.fake.RestorePreflightFn = preflight(true)
		c.finishing(service.KindRestore, service.ResultSuccess)
		if code := c.run("restore"); code != 0 {
			t.Fatalf("exit %d: %s", code, c.err())
		}
		// latest is the snapshot that was checked: a backup that comes in meanwhile changes nothing
		if req := c.fake.CallsTo("StartRestore")[0].Args[0].(service.RestoreRequest); req.Confirm != "abcdef01" || req.Snapshot != "abcdef01" {
			t.Errorf("request = %+v", req)
		}
		if !strings.Contains(c.err(), `Type "abcdef01" to confirm`) {
			t.Errorf("prompt = %q", c.err())
		}
	})

	t.Run("interactive, wrong answer", func(t *testing.T) {
		for _, answer := range []string{"yes\n", "\n", ""} {
			c := newCLI(t)
			c.tty, c.stdin = true, answer
			c.fake.RestorePreflightFn = preflight(true)
			if code := c.run("restore"); code != exitCodeUsage || len(c.fake.CallsTo("StartRestore")) != 0 {
				t.Errorf("answer %q: exit %d, calls %v", answer, code, c.fake)
			}
		}
	})
}

func TestRestoreToAnotherTarget(t *testing.T) {
	c := newCLI(t)
	c.fake.RestorePreflightFn = preflight(false)
	c.finishing(service.KindRestore, service.ResultSuccess)
	if code := c.run("restore", "--target", "restored", "--include", "/a", "--include", "/b", "--merge", "--no-safety-snapshot", "--force"); code != 0 {
		t.Fatalf("exit %d: %s", code, c.err())
	}
	req := c.fake.CallsTo("StartRestore")[0].Args[0].(service.RestoreRequest)
	abs, _ := filepath.Abs("restored")
	if req.Target != abs || len(req.Includes) != 2 || req.Mode != service.ModeMerge || !req.NoSafetySnapshot || !req.IgnoreSpaceWarning || req.Confirm != "" {
		t.Errorf("request = %+v: a restore into another directory needs no confirmation", req)
	}
}

func TestRestoreSpaceWarning(t *testing.T) {
	c := newCLI(t)
	c.fake.RestorePreflightFn = func(context.Context, service.RestoreRequest) (service.RestorePreflight, error) {
		return service.RestorePreflight{InPlace: false, Target: "/x", SpaceWarning: true, NeedBytes: 10 << 30, FreeBytes: 1 << 30,
			Warnings: []string{"the restore needs about 10.0 GiB more space but only 1.0 GiB are free"}}, nil
	}
	if code := c.run("restore", "--target", "/x"); code != exitCodeError || !strings.Contains(c.err(), "--force") {
		t.Errorf("exit %d, stderr %q", code, c.err())
	}
	if len(c.fake.CallsTo("StartRestore")) != 0 {
		t.Error("the restore started although the space is short")
	}
	c.finishing(service.KindRestore, service.ResultWarning)
	if code := c.run("restore", "--target", "/x", "--force"); code != 0 {
		t.Errorf("with --force: exit %d: %s", code, c.err())
	}
}

func TestAdminCommands(t *testing.T) {
	t.Run("cancel", func(t *testing.T) {
		c := newCLI(t)
		if code := c.run("cancel"); code != 0 || len(c.fake.CallsTo("Cancel")) != 1 || !strings.Contains(c.out(), "Cancel requested") {
			t.Errorf("exit %d, stdout %q", code, c.out())
		}
	})
	t.Run("verify", func(t *testing.T) {
		c := newCLI(t)
		c.finishing(service.KindVerify, service.ResultSuccess)
		if code := c.run("verify", "--full"); code != 0 {
			t.Fatal(code, c.err())
		}
		if req := c.fake.CallsTo("StartVerify")[0].Args[0].(service.VerifyRequest); req.Mode != "full" {
			t.Errorf("request = %+v", req)
		}
	})
	t.Run("keep and unkeep", func(t *testing.T) {
		c := newCLI(t)
		if code := c.run("keep", "abcdef0123"); code != 0 || !strings.Contains(c.out(), "keep: snapshot abcdef01 is now") {
			t.Errorf("keep: exit %d, stdout %q", code, c.out())
		}
		if code := c.run("unkeep", "abc"); code != 0 || len(c.fake.CallsTo("Unkeep")) != 1 {
			t.Errorf("unkeep: exit %d", code)
		}
	})
	t.Run("acknowledge", func(t *testing.T) {
		c := newCLI(t)
		if code := c.run("acknowledge", "abc"); code != exitCodeUsage || len(c.fake.CallsTo("Acknowledge")) != 0 {
			t.Errorf("without confirmation: exit %d", code)
		}
		if code := c.run("acknowledge", "abc", "--confirm"); code != 0 {
			t.Fatal(code, c.err())
		}
		if a := c.fake.CallsTo("Acknowledge")[0].Args; a[0] != "abc" || a[1] != "acknowledge" {
			t.Errorf("args = %v", a)
		}
	})
	t.Run("approve-forget", func(t *testing.T) {
		c := newCLI(t)
		c.finishing(service.KindForget, service.ResultSuccess)
		if code := c.run("approve-forget"); code != exitCodeUsage || len(c.fake.CallsTo("ApproveForget")) != 0 {
			t.Errorf("without confirmation: exit %d", code)
		}
		if code := c.run("approve-forget", "--confirm"); code != 0 {
			t.Fatal(code, c.err())
		}
		if a := c.fake.CallsTo("ApproveForget")[0].Args; a[0] != "forget" {
			t.Errorf("args = %v", a)
		}
	})
	t.Run("unlock", func(t *testing.T) {
		c := newCLI(t)
		if code := c.run("unlock"); code != 0 || c.fake.CallsTo("Unlock")[0].Args[0] != false {
			t.Errorf("stale: exit %d", code)
		}
		if code := c.run("unlock", "--all"); code != exitCodeUsage || len(c.fake.CallsTo("Unlock")) != 1 {
			t.Errorf("--all without confirmation: exit %d", code)
		}
		if code := c.run("unlock", "--all", "--confirm"); code != 0 {
			t.Fatal(code)
		}
		if a := c.fake.CallsTo("Unlock")[1].Args; a[0] != true || a[1] != "remove-all" {
			t.Errorf("args = %v", a)
		}
	})
	t.Run("notify test", func(t *testing.T) {
		c := newCLI(t)
		if code := c.run("notify", "test"); code != 0 || len(c.fake.CallsTo("NotifyTest")) != 1 {
			t.Errorf("exit %d", code)
		}
		c.fake.NotifyTestFn = func(context.Context) error {
			return fmt.Errorf("%w: NOTIFY_COMMAND is not configured", service.ErrUnavailable)
		}
		if code := c.run("notify", "test"); code != exitCodeError || !strings.Contains(c.err(), "NOTIFY_COMMAND") {
			t.Errorf("exit %d, stderr %q", code, c.err())
		}
	})
}

func TestForgetPreview(t *testing.T) {
	c := newCLI(t)
	c.fake.RetentionFn = func(context.Context) (service.RetentionInfo, error) {
		return service.RetentionInfo{
			Policy: "the last 10 snapshots", Hosts: []string{"me", "dummy"}, Total: 14, Foreign: 3, Held: true,
			Remove: []service.Snapshot{{ShortID: "aaaaaaaa", Time: cliNow.Add(-time.Hour)}, {ShortID: "bbbbbbbb", Time: cliNow.Add(-2 * time.Hour)}},
		}, nil
	}
	if code := c.run("forget"); code != 0 {
		t.Fatal(code, c.err())
	}
	for _, want := range []string{"keep the last 10 snapshots", "me, dummy", "14 snapshots", "3 snapshots of other hosts are never touched", "would remove 2 snapshots", "aaaaaaaa", "retention guard"} {
		if !strings.Contains(c.out(), want) {
			t.Errorf("output lacks %q:\n%s", want, c.out())
		}
	}
	if len(c.fake.CallsTo("ApproveForget")) != 0 {
		t.Error("forget is a preview and must change nothing")
	}

	c.fake.RetentionFn = func(context.Context) (service.RetentionInfo, error) {
		return service.RetentionInfo{AppendOnly: true}, nil
	}
	if c.run("forget"); !strings.Contains(c.out(), "append-only") {
		t.Errorf("output = %q", c.out())
	}
}

func TestLocksAndLog(t *testing.T) {
	c := newCLI(t)
	if c.run("locks"); !strings.Contains(c.out(), "no locks") {
		t.Errorf("output = %q", c.out())
	}
	c.fake.LocksFn = func(context.Context) ([]service.Lock, error) {
		return []service.Lock{{ID: "0123456789ab", Age: service.Duration(2 * time.Hour), Hostname: "pod-1", Username: "root", PID: 42, Stale: true}}, nil
	}
	if c.run("locks"); !strings.Contains(c.out(), "01234567") || !strings.Contains(c.out(), "pod-1") || !strings.Contains(c.out(), "true") {
		t.Errorf("output = %q", c.out())
	}

	if c.run("log"); c.out() != "There is no log of a backup yet.\n" {
		t.Errorf("output = %q", c.out())
	}
	started := time.Date(2026, 11, 11, 15, 16, 45, 0, time.Local)
	c.fake.LogFn = func(_ context.Context, k service.Kind) (service.Log, error) {
		return service.Log{Kind: k, Start: started}, nil
	}
	if c.run("log", "verify"); c.out() != "The log of the restore test of 2026-11-11 15:16:45 is empty.\n" {
		t.Errorf("output = %q", c.out())
	}
	c.fake.LogFn = func(_ context.Context, k service.Kind) (service.Log, error) {
		return service.Log{Kind: k, Lines: []string{"first", "second"}}, nil
	}
	if code := c.run("log", "prune"); code != 0 || c.out() != "first\nsecond\n" {
		t.Errorf("exit %d, output %q", code, c.out())
	}
	if kind := c.fake.CallsTo("Log")[2].Args[0]; kind != service.KindPrune {
		t.Errorf("kind = %v", kind)
	}
}

func TestStatusFollow(t *testing.T) {
	c := newCLI(t)
	c.fake.EventsFn = func(ctx context.Context) iter.Seq2[service.Event, error] {
		return func(yield func(service.Event, error) bool) {
			op := &service.Operation{Kind: service.KindBackup, Source: "tui", Phase: "forget", Progress: &service.Progress{Percent: 0.5}}
			for _, ev := range []service.Event{
				{Type: service.EventStarted, Time: cliNow, Operation: op},
				{Type: service.EventLog, Time: cliNow, Line: "hello"},
				{Type: service.EventFinished, Time: cliNow, Result: &service.RunResult{Kind: service.KindBackup, Result: service.ResultSuccess}},
			} {
				if !yield(ev, nil) {
					return
				}
			}
			<-ctx.Done()
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(100*time.Millisecond, cancel)
	if code := c.runCtx(ctx, "status", "--follow"); code != 0 {
		t.Fatalf("exit %d: %s", code, c.err())
	}
	for _, want := range []string{"backup started (tui)", "hello", "backup success"} {
		if !strings.Contains(c.out(), want) {
			t.Errorf("output lacks %q:\n%s", want, c.out())
		}
	}

	c = newCLI(t)
	c.fake.EventsFn = func(ctx context.Context) iter.Seq2[service.Event, error] {
		return func(yield func(service.Event, error) bool) {
			yield(service.Event{Type: service.EventLog, Line: "x"}, nil)
			<-ctx.Done()
		}
	}
	ctx, cancel = context.WithCancel(t.Context())
	time.AfterFunc(100*time.Millisecond, cancel)
	if code := c.runCtx(ctx, "--json", "status", "--follow"); code != 0 {
		t.Fatal(code)
	}
	lines := strings.Split(strings.TrimSpace(c.out()), "\n")
	if !strings.HasPrefix(lines[len(lines)-1], `{"type":"log"`) {
		t.Errorf("--json --follow prints one JSON object per line: %q", lines)
	}
}

func TestLs(t *testing.T) {
	c := newCLI(t)
	c.fake.FilesFn = func(_ context.Context, req service.FilesRequest) (service.FileList, error) {
		return service.FileList{
			Snapshot: service.Snapshot{ID: "aaaaaaaaaaaa", ShortID: "aaaaaaaa", Time: cliNow.Add(-time.Hour)},
			Path:     "/data/docs",
			Files: []service.File{
				{Path: "/data/docs/sub", Type: "dir", Mtime: cliNow},
				{Path: "/data/docs/a.txt", Type: "file", Size: 2048, Mtime: cliNow},
			},
			Truncated: req.Path == "big",
		}, nil
	}
	if code := c.run("ls", "latest", "docs"); code != 0 {
		t.Fatal(code, c.err())
	}
	for _, want := range []string{"/data/docs in snapshot aaaaaaaa", "/data/docs/sub/", "/data/docs/a.txt", "2.0 KiB"} {
		if !strings.Contains(c.out(), want) {
			t.Errorf("output lacks %q:\n%s", want, c.out())
		}
	}
	if req := c.fake.CallsTo("Files")[0].Args[0].(service.FilesRequest); req.Snapshot != "latest" || req.Path != "docs" {
		t.Errorf("request = %+v", req)
	}
	if c.run("ls", "latest", "big"); !strings.Contains(c.out(), "Only the first 2 entries") {
		t.Errorf("output = %q", c.out())
	}

	c.fake.FilesFn = nil
	if c.run("ls"); !strings.Contains(c.out(), "empty") {
		t.Errorf("output = %q", c.out())
	}
	if req := c.fake.CallsTo("Files")[2].Args[0].(service.FilesRequest); req != (service.FilesRequest{}) {
		t.Errorf("request = %+v: the service picks the latest snapshot and its top", req)
	}
	if code := c.run("ls", "a", "b", "c"); code != 2 {
		t.Errorf("exit %d for too many arguments", code)
	}
}

func TestFind(t *testing.T) {
	c := newCLI(t)
	if code := c.run("find", "x.txt"); code != 0 || !strings.Contains(c.out(), "Nothing found in 0 snapshots") {
		t.Errorf("exit %d, output %q", code, c.out())
	}
	c.fake.FindFn = func(context.Context, service.FindRequest) (service.FindResult, error) {
		newest := service.SnapshotRef{ID: "nnnnnnnnnnnn", Time: cliNow.Add(-time.Hour)}
		oldest := service.SnapshotRef{ID: "oooooooooooo", Time: cliNow.Add(-72 * time.Hour)}
		return service.FindResult{Searched: 5, Truncated: true, Versions: []service.FileVersion{
			{Path: "/d/a.txt", Type: "file", Size: 3 << 20, Mtime: cliNow, Newest: newest, Oldest: oldest, Snapshots: 4},
			{Path: "/d/old", Type: "dir", Newest: oldest, Oldest: oldest, Snapshots: 1},
		}}, nil
	}
	if code := c.run("find", "-i", "--snapshot", "latest", "*.TXT"); code != 0 {
		t.Fatal(code, c.err())
	}
	for _, want := range []string{"/d/a.txt", "3.0 MiB", "nnnnnnnn", "oooooooo", "/d/old/", "Searched 5 snapshots", "restore <snapshot> --include", "Only the first 2 matches"} {
		if !strings.Contains(c.out(), want) {
			t.Errorf("output lacks %q:\n%s", want, c.out())
		}
	}
	want := service.FindRequest{Pattern: "*.TXT", Snapshot: "latest", IgnoreCase: true}
	if req := c.fake.CallsTo("Find")[1].Args[0].(service.FindRequest); req != want {
		t.Errorf("request = %+v", req)
	}
	c.run("find", "a", "--json")
	var got service.FindResult
	if err := json.Unmarshal(c.stdout.Bytes(), &got); err != nil || len(got.Versions) != 2 {
		t.Errorf("JSON: %v %s", err, c.out())
	}
	if code := c.run("find"); code != 2 {
		t.Errorf("exit %d without a pattern", code)
	}
}

func TestDiff(t *testing.T) {
	c := newCLI(t)
	c.fake.DiffFn = func(context.Context, service.DiffRequest) (service.DiffResult, error) {
		return service.DiffResult{
			From: service.Snapshot{ShortID: "aaaaaaaa", Time: cliNow.Add(-48 * time.Hour)}, To: service.Snapshot{ShortID: "bbbbbbbb", Time: cliNow},
			Removed: service.DiffSide{Files: 3, Dirs: 1, Paths: []service.DiffPath{{Path: "/big/", Entries: 4}}},
		}, nil
	}
	if code := c.run("diff"); code != 0 {
		t.Fatal(code, c.err())
	}
	for _, want := range []string{"From snapshot aaaaaaaa", "removed  3 files, 1 directory", "/big/  (4 entries)"} {
		if !strings.Contains(c.out(), want) {
			t.Errorf("output lacks %q:\n%s", want, c.out())
		}
	}
	if req := c.fake.CallsTo("Diff")[0].Args[0].(service.DiffRequest); req != (service.DiffRequest{}) {
		t.Errorf("request = %+v: the service picks the snapshots", req)
	}
	c.run("diff", "new", "old", "--json")
	if req := c.fake.CallsTo("Diff")[1].Args[0].(service.DiffRequest); req.To != "new" || req.From != "old" {
		t.Errorf("request = %+v", req)
	}
	var got service.DiffResult
	if err := json.Unmarshal(c.stdout.Bytes(), &got); err != nil || got.Removed.Files != 3 {
		t.Errorf("JSON: %v %s", err, c.out())
	}
}

func TestRestoreAtATime(t *testing.T) {
	c := newCLI(t)
	day := func(d, h int) time.Time { return time.Date(2026, 3, d, h, 0, 0, 0, time.Local) }
	c.fake.SnapshotsFn = func(context.Context) ([]service.Snapshot, error) {
		return []service.Snapshot{
			{ID: "aaaaaaaaaaaa", ShortID: "aaaaaaaa", Time: day(1, 2)},
			{ID: "bbbbbbbbbbbb", ShortID: "bbbbbbbb", Time: day(2, 2)},
			{ID: "ssssssssssss", ShortID: "ssssssss", Time: day(2, 12), Suspicious: true},
			{ID: "pppppppppppp", ShortID: "pppppppp", Time: day(2, 13), PreRestore: true},
			{ID: "cccccccccccc", ShortID: "cccccccc", Time: day(3, 2)},
		}, nil
	}
	c.fake.RestorePreflightFn = func(_ context.Context, req service.RestoreRequest) (service.RestorePreflight, error) {
		return service.RestorePreflight{Snapshot: service.Snapshot{ShortID: req.Snapshot}, Target: req.Target}, nil
	}
	// the newest regular snapshot before the time; the arguments go to the hooks
	if code := c.run("restore", "--at", "2026-03-02 14:00", "--target", t.TempDir(), "--no-wait", "--", "--maintenance", "on"); code != 0 {
		t.Fatal(code, c.err())
	}
	req := c.fake.CallsTo("StartRestore")[0].Args[0].(service.RestoreRequest)
	if req.Snapshot != "bbbbbbbb" || !slices.Equal(req.HookArgs, []string{"--maintenance", "on"}) {
		t.Errorf("request = %+v", req)
	}
	if !strings.Contains(c.out(), "Snapshot bbbbbbbb of 2026-03-02 02:00:00 is the newest at or before 2026-03-02 14:00:00") {
		t.Errorf("output = %q", c.out())
	}

	for at, want := range map[string]string{
		"2026-03-02":           "aaaaaaaa", // a date is its first minute
		"2026-03-02T02:00":     "bbbbbbbb", // at the time counts
		"2026-03-09T00:00:00Z": "cccccccc",
	} {
		c.run("restore", "--at", at, "--target", t.TempDir(), "--no-wait")
		calls := c.fake.CallsTo("StartRestore")
		if got := calls[len(calls)-1].Args[0].(service.RestoreRequest).Snapshot; got != want {
			t.Errorf("--at %s restores %s, want %s", at, got, want)
		}
	}

	n := len(c.fake.CallsTo("StartRestore"))
	if code := c.run("restore", "--at", "2026-02-01", "--target", t.TempDir()); code != 1 || !strings.Contains(c.err(), "the oldest is aaaaaaaa") {
		t.Errorf("exit %d, %q", code, c.err())
	}
	if code := c.run("restore", "--at", "yesterday"); code != 2 || !strings.Contains(c.err(), "cannot read the time") {
		t.Errorf("exit %d, %q", code, c.err())
	}
	if len(c.fake.CallsTo("StartRestore")) != n {
		t.Error("a restore started without a snapshot for the time")
	}
}

func TestRestoreDryRun(t *testing.T) {
	c := newCLI(t)
	c.fake.RestorePreflightFn = func(_ context.Context, req service.RestoreRequest) (service.RestorePreflight, error) {
		p := service.RestorePreflight{Snapshot: service.Snapshot{ShortID: "aaaaaaaa"}, Target: "/data", InPlace: true}
		if req.Preview {
			p.Changes = &service.RestoreChanges{Overwritten: 3, Deleted: 1, DeletedPaths: []string{"/data/new.txt"}}
		}
		return p, nil
	}
	// in place and without a terminal: a dry run needs no confirmation, as it restores nothing
	if code := c.run("restore", "latest", "--dry-run"); code != 0 {
		t.Fatal(code, c.err())
	}
	for _, want := range []string{"3 overwritten, 1 deleted", "/data/new.txt", "nothing was restored"} {
		if !strings.Contains(c.out(), want) {
			t.Errorf("output lacks %q:\n%s", want, c.out())
		}
	}
	if len(c.fake.CallsTo("StartRestore")) != 0 {
		t.Error("a dry run started a restore")
	}
	c.run("restore", "--dry-run", "--json")
	var got service.RestorePreflight
	if err := json.Unmarshal(c.stdout.Bytes(), &got); err != nil || got.Changes == nil || got.Changes.Overwritten != 3 {
		t.Errorf("JSON: %v %s", err, c.out())
	}
}
