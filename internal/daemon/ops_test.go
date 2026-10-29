package daemon

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/noris-network/mcs-backup/v2/internal/engine"
	"github.com/noris-network/mcs-backup/v2/internal/service"
)

// blockingBackup makes the fake backup wait until release is closed.
func blockingBackup(release <-chan struct{}) func(context.Context, service.BackupRequest, engine.Sink) engine.BackupResult {
	return func(ctx context.Context, _ service.BackupRequest, _ engine.Sink) engine.BackupResult {
		select {
		case <-release:
			return engine.BackupResult{Result: service.ResultSuccess, Snapshot: "new1"}
		case <-ctx.Done():
			return engine.BackupResult{Result: service.ResultCancelled, Error: "cancelled"}
		}
	}
}

func TestOnlyOneOperationAtATime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		d := newTestDaemon(t, func(f *fakeEngine) { f.backup = blockingBackup(release) })
		ctx, stop := start(t, d)
		synctest.Wait()

		op, err := d.StartBackup(service.WithSource(ctx, service.SourceTUI), service.BackupRequest{Note: "first"})
		if err != nil {
			t.Fatal(err)
		}
		if op.Kind != service.KindBackup || op.Source != service.SourceTUI || op.Origin != service.OriginManual || op.ID == "" {
			t.Errorf("operation = %+v", op)
		}
		synctest.Wait()

		_, err = d.StartBackup(ctx, service.BackupRequest{})
		var busy *service.BusyError
		if !errors.Is(err, service.ErrBusy) || !errors.As(err, &busy) || busy.Current.ID != op.ID {
			t.Fatalf("second backup: %v", err)
		}
		if _, err := d.StartRestore(ctx, service.RestoreRequest{Snapshot: "latest", Confirm: "latest"}); !errors.Is(err, service.ErrBusy) {
			t.Errorf("restore while a backup runs: %v", err)
		}
		if _, err := d.Keep(ctx, "x"); !errors.Is(err, service.ErrBusy) {
			t.Errorf("tag change while a backup runs: %v", err)
		}
		cur, _ := d.Operation(ctx)
		if cur == nil || cur.ID != op.ID {
			t.Errorf("Operation() = %+v", cur)
		}
		if st, _ := d.Status(ctx); st.Operation == nil || st.Operation.ID != op.ID {
			t.Errorf("Status().Operation = %+v", st.Operation)
		}

		close(release)
		synctest.Wait()
		if cur, _ := d.Operation(ctx); cur != nil {
			t.Errorf("Operation() after the end = %+v", cur)
		}
		st, _ := d.Status(ctx)
		if r := st.LastRuns[service.KindBackup]; r.Result != service.ResultSuccess || r.ID != op.ID || r.Source != service.SourceTUI {
			t.Errorf("last run = %+v", r)
		}
		// the slot is free again
		if _, err := d.StartBackup(ctx, service.BackupRequest{}); err != nil {
			t.Errorf("backup after the end: %v", err)
		}
		synctest.Wait()
		stop()
	})
}

func TestCancel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := newTestDaemon(t, func(f *fakeEngine) { f.backup = blockingBackup(make(chan struct{})) })
		ctx, stop := start(t, d)
		synctest.Wait()
		if _, err := d.StartBackup(ctx, service.BackupRequest{}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if err := d.Cancel(ctx); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		st, _ := d.Status(ctx)
		if st.Operation != nil || st.LastRuns[service.KindBackup].Result != service.ResultCancelled {
			t.Errorf("status = %+v", st)
		}
		if names := d.notes.names(); len(names) != 0 {
			t.Errorf("notifications = %q: a cancel is not an incident", names)
		}
		if err := d.Cancel(ctx); err != nil {
			t.Errorf("cancel without an operation: %v", err)
		}
		stop()
	})
}

func TestEventsAndLog(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := newTestDaemon(t, func(f *fakeEngine) {
			f.backup = func(_ context.Context, _ service.BackupRequest, sink engine.Sink) engine.BackupResult {
				sink.Phase("backup")
				sink.Log("connecting with hunter2-secret")
				sink.Progress(service.Progress{Percent: 0.5, FilesDone: 5, FilesTotal: 10})
				sink.Phase("forget")
				return engine.BackupResult{Result: service.ResultSuccess, Snapshot: "new1", Message: "done"}
			}
		})
		ctx, stop := start(t, d)
		synctest.Wait()

		var events []service.Event
		ectx, cancelEvents := context.WithCancel(ctx)
		go func() {
			for ev, err := range d.Events(ectx) {
				if err != nil {
					return
				}
				events = append(events, ev)
			}
		}()
		synctest.Wait()

		op, _ := d.StartBackup(ctx, service.BackupRequest{})
		synctest.Wait()
		cancelEvents()
		synctest.Wait()

		var types []service.EventType
		for _, ev := range events {
			types = append(types, ev.Type)
		}
		want := []service.EventType{service.EventStarted, service.EventPhase, service.EventLog, service.EventProgress, service.EventPhase, service.EventFinished}
		if !slices.Equal(types, want) {
			t.Fatalf("event types = %v, want %v", types, want)
		}
		if events[0].Operation.ID != op.ID || events[1].Operation.Phase != "backup" {
			t.Errorf("events = %+v", events[:2])
		}
		if line := events[2].Line; line != "connecting with ***" {
			t.Errorf("log line = %q: secrets must be hidden", line)
		}
		if p := events[3].Operation.Progress; p == nil || p.Percent != 0.5 || p.FilesTotal != 10 {
			t.Errorf("progress = %+v", p)
		}
		if events[4].Operation.Progress != nil {
			t.Error("a new phase resets the progress")
		}
		if r := events[5].Result; r == nil || r.Result != service.ResultSuccess || r.Message != "done" || r.Snapshot != "new1" {
			t.Errorf("result = %+v", r)
		}

		log, _ := d.Log(ctx, service.KindBackup)
		if !slices.Equal(log.Lines, []string{"connecting with ***"}) {
			t.Errorf("log = %+v", log)
		}
		if copyLog, _ := d.Log(ctx, service.KindCopy); !slices.Equal(copyLog.Lines, log.Lines) || copyLog.Kind != service.KindBackup {
			t.Errorf("the copy log is part of the backup log: %+v", copyLog)
		}
		if empty, _ := d.Log(ctx, service.KindPrune); len(empty.Lines) != 0 {
			t.Errorf("prune log = %+v", empty)
		}
		stop()
	})
}

func TestEventsStartWithTheRunningOperation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := newTestDaemon(t, func(f *fakeEngine) { f.backup = blockingBackup(make(chan struct{})) })
		ctx, stop := start(t, d)
		synctest.Wait()
		op, _ := d.StartBackup(ctx, service.BackupRequest{})
		synctest.Wait()

		ectx, cancel := context.WithCancel(ctx)
		var first service.Event
		for ev, err := range d.Events(ectx) {
			if err != nil {
				t.Fatal(err)
			}
			first = ev
			break
		}
		cancel()
		if first.Type != service.EventStarted || first.Operation == nil || first.Operation.ID != op.ID {
			t.Errorf("first event = %+v: a client that connects late must learn what is running", first)
		}
		_ = d.Cancel(ctx)
		synctest.Wait()
		stop()
	})
}

func TestSlowEventConsumerIsDropped(t *testing.T) {
	b := newBroadcaster()
	ch, _ := b.subscribe()
	for range subscriberBuffer + 10 {
		b.publish(service.Event{Type: service.EventLog})
	}
	n := 0
	for range ch {
		n++
	}
	if n != subscriberBuffer {
		t.Errorf("received %d events, want %d before the channel is closed", n, subscriberBuffer)
	}
}

func TestRunLogIsBounded(t *testing.T) {
	l := &runLog{}
	for i := range maxLogLines + 100 {
		l.add(string(rune('a' + i%26)))
	}
	got := l.snapshot(service.KindBackup)
	if len(got.Lines) > maxLogLines+1 || !strings.Contains(got.Lines[0], "earlier lines dropped") {
		t.Errorf("lines = %d, first = %q", len(got.Lines), got.Lines[0])
	}
}

func TestRetentionHeldAndApproval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := newTestDaemon(t, func(f *fakeEngine) {
			f.backup = func(context.Context, service.BackupRequest, engine.Sink) engine.BackupResult {
				return engine.BackupResult{
					Result: service.ResultWarning, Snapshot: "new1", Warnings: []string{"forget held"},
					Forget: engine.ForgetOutcome{Held: true, Removed: 4, Total: 12},
				}
			}
		})
		ctx, stop := start(t, d)
		synctest.Wait()

		for range 2 {
			if _, err := d.StartBackup(ctx, service.BackupRequest{}); err != nil {
				t.Fatal(err)
			}
			synctest.Wait()
		}
		st, _ := d.Status(ctx)
		if !st.ForgetHeld || !hasWarning(st, "forget_held") {
			t.Errorf("status = %+v", st)
		}
		if body := scrape(t, d.Daemon); !strings.Contains(body, "mcsbackup_forget_held 1") {
			t.Error("forget_held metric")
		}
		held := 0
		for _, n := range d.notes.names() {
			if n == "retention.held" {
				held++
			}
		}
		if held != 1 {
			t.Errorf("notifications = %q: the guard is reported once, not on every backup", d.notes.names())
		}

		if _, err := d.ApproveForget(ctx, "yes"); !errors.Is(err, service.ErrConfirm) {
			t.Errorf("wrong confirmation: %v", err)
		}
		if d.eng.called("approve") != 0 {
			t.Error("forget ran without confirmation")
		}
		op, err := d.ApproveForget(ctx, service.ConfirmForget)
		if err != nil || op.Kind != service.KindForget {
			t.Fatalf("ApproveForget() = %+v, %v", op, err)
		}
		synctest.Wait()
		st, _ = d.Status(ctx)
		if st.ForgetHeld || st.LastRuns[service.KindForget].Result != service.ResultSuccess {
			t.Errorf("status = %+v", st)
		}
		stop()
	})
}

func TestNotificationsFollowTheResults(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var results []service.Result
		d := newTestDaemon(t, func(f *fakeEngine) {
			f.backup = func(context.Context, service.BackupRequest, engine.Sink) engine.BackupResult {
				r := results[0]
				results = results[1:]
				return engine.BackupResult{Result: r, Snapshot: "s", Error: map[bool]string{true: "boom"}[!r.OK()]}
			}
		})
		ctx, stop := start(t, d)
		synctest.Wait()

		run := func(r service.Result) {
			results = []service.Result{r}
			if _, err := d.StartBackup(ctx, service.BackupRequest{}); err != nil {
				t.Fatal(err)
			}
			synctest.Wait()
		}
		for _, r := range []service.Result{
			service.ResultSuccess, service.ResultFailure, service.ResultFailure, service.ResultSuccess, service.ResultSuccess,
			service.ResultWarning, service.ResultSuspicious, service.ResultTimeout, service.ResultCancelled, service.ResultSkipped, service.ResultSuccess,
		} {
			run(r)
		}
		want := []string{
			"backup.succeeded", "backup.failed", "backup.failed", "backup.recovered", "backup.succeeded",
			"backup.warning", "backup.suspicious", "backup.failed", "backup.recovered",
		}
		if got := d.notes.names(); !slices.Equal(got, want) {
			t.Errorf("notifications =\n %q\nwant\n %q", got, want)
		}
		ev := d.notes.events[1]
		if ev.Kind != "backup" || ev.Result != "failure" || ev.Error != "boom" || ev.Snapshot != "s" {
			t.Errorf("event = %+v", ev)
		}
		stop()
	})
}

func TestTagOperationsAndConfirmations(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := newTestDaemon(t, nil)
		ctx, stop := start(t, d)
		synctest.Wait()

		if res, err := d.Keep(ctx, "abc"); err != nil || res.ID != "kept" {
			t.Errorf("Keep() = %+v, %v", res, err)
		}
		if _, err := d.Unkeep(ctx, "abc"); err != nil {
			t.Error(err)
		}
		if _, err := d.Acknowledge(ctx, "abc", ""); !errors.Is(err, service.ErrConfirm) {
			t.Errorf("Acknowledge without confirmation: %v", err)
		}
		if d.eng.called("acknowledge") != 0 {
			t.Error("acknowledged without confirmation")
		}
		if _, err := d.Acknowledge(ctx, "abc", service.ConfirmAcknowledge); err != nil {
			t.Error(err)
		}
		if err := d.Unlock(ctx, true, "yes"); !errors.Is(err, service.ErrConfirm) {
			t.Errorf("Unlock(all) with a wrong confirmation: %v", err)
		}
		if err := d.Unlock(ctx, false, ""); err != nil {
			t.Errorf("Unlock(stale) needs no confirmation: %v", err)
		}
		if err := d.Unlock(ctx, true, service.ConfirmRemoveAll); err != nil {
			t.Error(err)
		}
		for _, c := range []string{"keep", "unkeep", "acknowledge", "unlock", "unlock-all"} {
			if d.eng.called(c) != 1 {
				t.Errorf("%s was called %d times", c, d.eng.called(c))
			}
		}
		if st, _ := d.Status(ctx); st.LastRuns[service.KindTag].Result != service.ResultSuccess {
			t.Errorf("last tag run = %+v", st.LastRuns[service.KindTag])
		}
		stop()
	})
}

func TestAppendOnlyRefusesDestructiveActions(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := newTestDaemon(t, nil, "APPEND_ONLY=true", "RETENTION_POLICY=")
		ctx := t.Context()
		if err := d.Unlock(ctx, true, service.ConfirmRemoveAll); !errors.Is(err, service.ErrAppendOnly) {
			t.Errorf("Unlock(all): %v", err)
		}
		if _, err := d.ApproveForget(ctx, service.ConfirmForget); !errors.Is(err, service.ErrAppendOnly) {
			t.Errorf("ApproveForget: %v", err)
		}
		if d.eng.called("unlock-all") != 0 || d.eng.called("approve") != 0 {
			t.Error("the engine was called")
		}
	})
}

func TestVerifyNotConfigured(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := newTestDaemon(t, func(f *fakeEngine) { f.verifyConfigured = false })
		_, err := d.StartVerify(t.Context(), service.VerifyRequest{})
		if !errors.Is(err, service.ErrUnavailable) {
			t.Errorf("StartVerify() = %v", err)
		}
	})
}

func TestNotifyTestAndUnavailableParts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := newTestDaemon(t, nil)
		if err := d.NotifyTest(t.Context()); err != nil {
			t.Fatal(err)
		}
		if got := d.notes.names(); !slices.Equal(got, []string{"test"}) {
			t.Errorf("notifications = %q", got)
		}
		if _, err := d.Env(t.Context(), false); !errors.Is(err, service.ErrUnavailable) {
			t.Errorf("Env() without a provider: %v", err)
		}
		if _, err := d.WebUI(t.Context()); err == nil {
			t.Error("WebUI() without a web UI")
		}
		if _, err := d.LoginLink(t.Context()); err == nil {
			t.Error("LoginLink() without a web UI")
		}
	})
}

// An unconfirmed restore over the live data is refused at once, before an
// operation starts, whoever asks (API, MCP, web UI).
func TestRestoreOverLiveDataNeedsConfirmation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := newTestDaemon(t, nil)
		ctx, stop := start(t, d)
		defer stop()
		synctest.Wait()

		for _, req := range []service.RestoreRequest{
			{Snapshot: "latest"},
			{Snapshot: "latest", Confirm: "yes"},
			{Snapshot: "abc123", Confirm: "latest"},
		} {
			if _, err := d.StartRestore(ctx, req); !errors.Is(err, service.ErrConfirm) {
				t.Errorf("StartRestore(%+v) = %v, want a confirmation error", req, err)
			}
		}
		synctest.Wait()
		if cur, _ := d.Operation(ctx); cur != nil {
			t.Errorf("an operation started: %+v", cur)
		}
		// a restore elsewhere needs none
		if _, err := d.StartRestore(ctx, service.RestoreRequest{Snapshot: "latest", Target: "/elsewhere"}); err != nil {
			t.Errorf("restore to another directory: %v", err)
		}
		synctest.Wait()
	})
}
