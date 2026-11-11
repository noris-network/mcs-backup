package tui

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/noris-network/mcs-backup/v2/internal/service"
)

func TestOverview(t *testing.T) {
	r := healthy(newRig(t))
	r.fake.StatusFn = func(context.Context) (service.Status, error) {
		return service.Status{
			Version: "2.0.0", Host: "app", StartedAt: t0.Add(-time.Hour),
			Repository: service.RepoStatus{Up: true, Snapshots: 3, SizeBytes: 1 << 20, LastSuccess: t0.Add(-3 * time.Hour)},
			Schedule:   service.ScheduleStatus{Enabled: true, Backup: "0 2 * * *"}, Retention: "the last 7 snapshots",
			Warnings: []service.Warning{{Code: "backup_suspicious", Severity: service.SeverityError, Message: "the latest backup is much smaller"}},
		}, nil
	}
	r.load()
	r.wantScreen("Overview", "reachable", "3 snapshots", "3h ago", "backups enabled (0 2 * * *)", "keeps the last 7 snapshots",
		"Needs attention", "the latest backup is much smaller", "Restore test is not set up", "Web UI", "off")
}

func TestServiceNotReachable(t *testing.T) {
	r := newRig(t)
	r.fake.StatusFn = func(context.Context) (service.Status, error) {
		return service.Status{}, errors.New("connection refused")
	}
	r.load()
	r.wantScreen("service not reachable: connection refused")
	healthy(r)
	r.send(streamMsg{})
	r.wantNoScreen("service not reachable")
}

func TestTabsAndSnapshotsNewestFirst(t *testing.T) {
	r := healthy(newRig(t)).load()
	r.press("2")
	s := r.screen()
	if i, j := strings.Index(s, "cccccccc"), strings.Index(s, "aaaaaaaa"); i < 0 || j < 0 || i > j {
		t.Errorf("the newest snapshot should come first:\n%s", s)
	}
	r.wantScreen("keep", "before upgrade")
	r.press("tab")
	r.wantScreen("The repository has no locks.")
	r.press("shift+tab")
	r.wantScreen("ID", "ORIGIN")
}

func TestSnapshotCursorFollowsTheSnapshot(t *testing.T) {
	r := healthy(newRig(t)).load()
	r.press("2", "down") // bbbbbbbb
	selected := func() string {
		t.Helper()
		s, ok := r.m.selectedSnapshot()
		if !ok {
			t.Fatal("no snapshot selected")
		}
		return s.ShortID
	}
	list := func(snaps ...service.Snapshot) {
		r.fake.SnapshotsFn = func(context.Context) ([]service.Snapshot, error) { return snaps, nil }
		r.press("r")
	}
	// a new backup comes in above it
	list(snap("aaaaaaaa", 48*time.Hour), snap("bbbbbbbb", 24*time.Hour), snap("cccccccc", time.Hour), snap("dddddddd", time.Minute))
	if got := selected(); got != "bbbbbbbb" {
		t.Errorf("after a new snapshot the cursor is on %s", got)
	}
	// keep rewrites it under a new ID, at the same time
	list(snap("aaaaaaaa", 48*time.Hour), snap("eeeeeeee", 24*time.Hour, func(s *service.Snapshot) { s.Keep = true }), snap("cccccccc", time.Hour), snap("dddddddd", time.Minute))
	if got := selected(); got != "eeeeeeee" {
		t.Errorf("after a tag change the cursor is on %s", got)
	}
	// it is gone: the cursor stays on its row
	list(snap("aaaaaaaa", 48*time.Hour), snap("cccccccc", time.Hour), snap("dddddddd", time.Minute))
	if got := selected(); got != "aaaaaaaa" {
		t.Errorf("after the snapshot went the cursor is on %s", got)
	}
}

func TestAccessMasksSecrets(t *testing.T) {
	r := healthy(newRig(t)).load()
	r.press("4")
	r.wantScreen("RESTIC_PASSWORD", "***", "The secrets are hidden", "local path")
	r.wantNoScreen("hunter2")
	r.press("p")
	r.wantScreen("hunter2", "The secrets are shown")
	r.press("p")
	r.wantNoScreen("hunter2")

	r.press("y")
	// the environment comes as plain text, for selecting it with the mouse
	r.wantScreen("export RESTIC_PASSWORD='hunter2'", "paste them into a shell")
	r.wantNoScreen("│", "╭")
	r.press("esc")
	r.wantScreen("Access")
	for _, c := range r.fake.CallsTo("Env") {
		if c.Source != service.SourceTUI {
			t.Errorf("Env called with source %q", c.Source)
		}
	}
}

func TestBackupWithNoteAndKeep(t *testing.T) {
	r := healthy(newRig(t)).load()
	r.press("b")
	r.wantScreen("Start a backup", "Note for the snapshot")
	r.typeText("before the move")
	r.press("tab", "enter")
	calls := r.fake.CallsTo("StartBackup")
	if len(calls) != 1 {
		t.Fatalf("StartBackup calls: %v", r.fake)
	}
	req := calls[0].Args[0].(service.BackupRequest)
	if req.Note != "before the move" || !req.Keep {
		t.Errorf("request = %+v", req)
	}
	if calls[0].Source != service.SourceTUI {
		t.Errorf("source = %q", calls[0].Source)
	}
	r.wantScreen("backup started")
}

func TestBusy(t *testing.T) {
	r := healthy(newRig(t)).load()
	r.fake.StartBackupFn = func(context.Context, service.BackupRequest) (service.Operation, error) {
		return service.Operation{}, &service.BusyError{Current: service.Operation{Kind: service.KindPrune, Started: t0}}
	}
	r.press("b", "enter")
	r.wantScreen("another operation is running (prune)")
}

func TestRestoreInPlaceNeedsTheSnapshotTyped(t *testing.T) {
	r := healthy(newRig(t)).load()
	r.fake.RestorePreflightFn = func(_ context.Context, req service.RestoreRequest) (service.RestorePreflight, error) {
		return service.RestorePreflight{Snapshot: snap("cccccccc", time.Hour), Target: "/data", InPlace: true, Mode: req.Mode, SafetySnapshot: true,
			Hooks: []string{"pre-restore"}}, nil
	}
	r.press("2", "enter") // the newest snapshot
	r.wantScreen("Restore", "Snapshot (ID or latest)")
	r.press("enter")
	calls := r.fake.CallsTo("RestorePreflight")
	if len(calls) != 1 {
		t.Fatalf("preflight calls: %v", r.fake)
	}
	if req := calls[0].Args[0].(service.RestoreRequest); req.Snapshot != "cccccccc" || req.Target != "" || req.Mode != service.ModeExact {
		t.Errorf("preflight request = %+v", req)
	}
	r.wantScreen("in place, replaces the current data", "a snapshot of the current data is taken first", "pre-restore")

	r.press("enter")
	r.wantScreen("Type cccccccc to confirm")
	r.typeText("latest")
	r.press("enter")
	if n := len(r.fake.CallsTo("StartRestore")); n != 0 {
		t.Fatalf("a wrong confirmation started the restore")
	}
	r.wantScreen("nothing was done")

	r.m.dlg.(*restoreDialog).confirm.SetValue("cccccccc")
	r.press("enter")
	calls = r.fake.CallsTo("StartRestore")
	if len(calls) != 1 {
		t.Fatalf("StartRestore calls: %v", r.fake)
	}
	if req := calls[0].Args[0].(service.RestoreRequest); req.Confirm != "cccccccc" || req.Snapshot != "cccccccc" || req.NoSafetySnapshot {
		t.Errorf("restore request = %+v", req)
	}
	if r.m.dlg != nil {
		t.Error("the dialog stays open after the restore started")
	}
}

func TestRestoreToAnotherDirectory(t *testing.T) {
	r := healthy(newRig(t)).load()
	r.fake.RestorePreflightFn = func(_ context.Context, req service.RestoreRequest) (service.RestorePreflight, error) {
		return service.RestorePreflight{Snapshot: snap("cccccccc", time.Hour), Target: req.Target, Mode: service.ModeMerge, TargetEmpty: true}, nil
	}
	r.press("2", "t")
	r.press("down")
	r.typeText("/tmp/out")
	r.press("down")
	r.typeText("/etc/app /var/lib/app")
	r.press("down", "left") // mode: merge
	r.press("enter")
	req := r.fake.CallsTo("RestorePreflight")[0].Args[0].(service.RestoreRequest)
	if req.Snapshot != "latest" || req.Target != "/tmp/out" || len(req.Includes) != 2 || req.Mode != service.ModeMerge {
		t.Errorf("preflight request = %+v", req)
	}
	r.press("enter")
	calls := r.fake.CallsTo("StartRestore")
	if len(calls) != 1 {
		t.Fatalf("a restore to another directory needs no typed confirmation: %v", r.fake)
	}
}

func TestRestoreThatCannotStartKeepsTheDialog(t *testing.T) {
	r := healthy(newRig(t)).load()
	r.fake.RestorePreflightFn = func(_ context.Context, req service.RestoreRequest) (service.RestorePreflight, error) {
		inPlace := req.Target == ""
		return service.RestorePreflight{Snapshot: snap("cccccccc", time.Hour), Target: req.Target, InPlace: inPlace, Mode: service.ModeMerge, TargetEmpty: true}, nil
	}
	busy := true
	r.fake.StartRestoreFn = func(context.Context, service.RestoreRequest) (service.Operation, error) {
		if busy {
			return service.Operation{}, &service.BusyError{Current: service.Operation{Kind: service.KindBackup, Started: t0}}
		}
		return service.Operation{Kind: service.KindRestore}, nil
	}

	// to another directory: it starts from the preview
	r.press("2", "t", "down")
	r.typeText("/tmp/out")
	r.press("enter", "enter")
	r.wantScreen("Restore", "another operation is running (backup)", "Press enter to try", "/tmp/out")
	busy = false
	r.press("enter")
	if calls := r.fake.CallsTo("StartRestore"); len(calls) != 2 || calls[1].Args[0].(service.RestoreRequest).Target != "/tmp/out" {
		t.Fatalf("StartRestore calls: %v", r.fake)
	}
	if r.m.dlg != nil {
		t.Errorf("the dialog stays after the restore started:\n%s", r.screen())
	}
	r.wantScreen("restore started")

	// in place: it starts from the typed confirmation, which stays typed
	busy = true
	r.press("enter", "enter", "enter")
	r.typeText("cccccccc")
	r.press("enter")
	r.wantScreen("another operation is running (backup)", "> cccccccc")
	busy = false
	r.press("enter")
	if calls := r.fake.CallsTo("StartRestore"); len(calls) != 4 || calls[3].Args[0].(service.RestoreRequest).Confirm != "cccccccc" {
		t.Fatalf("StartRestore calls: %v", r.fake)
	}
	if r.m.dlg != nil {
		t.Errorf("the dialog stays after the restore started:\n%s", r.screen())
	}
}

func TestRestoreSpaceWarning(t *testing.T) {
	r := healthy(newRig(t)).load()
	r.fake.RestorePreflightFn = func(_ context.Context, req service.RestoreRequest) (service.RestorePreflight, error) {
		return service.RestorePreflight{Snapshot: snap("cccccccc", time.Hour), Target: req.Target, SpaceWarning: true, NeedBytes: 10 << 30, FreeBytes: 1 << 30}, nil
	}
	r.press("2", "t", "down")
	r.typeText("/tmp/out")
	r.press("enter", "enter")
	r.wantScreen("not enough free space")
	if len(r.fake.CallsTo("StartRestore")) != 0 {
		t.Fatal("restored despite the space warning")
	}
	r.press("f", "enter")
	calls := r.fake.CallsTo("StartRestore")
	if len(calls) != 1 || !calls[0].Args[0].(service.RestoreRequest).IgnoreSpaceWarning {
		t.Errorf("calls: %v", r.fake)
	}
}

func TestRestoreWithoutSafetySnapshotNeedsTwoConfirmations(t *testing.T) {
	r := healthy(newRig(t)).load()
	req := service.RestoreRequest{Snapshot: "cccccccc", Mode: service.ModeExact, Confirm: "cccccccc"}
	r.m.lastRestore = &req
	r.send(eventMsg{service.Event{Type: service.EventFinished, Result: &service.RunResult{Kind: service.KindRestore, Result: service.ResultFailure, Error: "safety snapshot failed"}}})
	r.wantScreen("restore: failure", "safety snapshot failed", "x  repeat the restore without the safety snapshot")

	r.press("x")
	r.wantScreen("Restore without safety snapshot")
	r.typeText("yes")
	r.press("enter")
	if len(r.fake.CallsTo("StartRestore")) != 0 {
		t.Fatal("started with a wrong word")
	}
	r.m.dlg.(*confirmDialog).in.SetValue("no-safety-snapshot")
	r.press("enter")
	r.wantScreen("Confirm the restore")
	if len(r.fake.CallsTo("StartRestore")) != 0 {
		t.Fatal("one confirmation is not enough")
	}
	r.m.dlg.(*confirmDialog).in.SetValue("cccccccc")
	r.press("enter")
	calls := r.fake.CallsTo("StartRestore")
	if len(calls) != 1 {
		t.Fatalf("calls: %v", r.fake)
	}
	if got := calls[0].Args[0].(service.RestoreRequest); !got.NoSafetySnapshot || got.Confirm != "cccccccc" {
		t.Errorf("request = %+v", got)
	}
}

func TestKeepAndAcknowledge(t *testing.T) {
	r := healthy(newRig(t)).load()
	r.fake.SnapshotsFn = func(context.Context) ([]service.Snapshot, error) {
		return []service.Snapshot{snap("aaaaaaaa", 2*time.Hour), snap("bbbbbbbb", time.Hour, func(s *service.Snapshot) { s.Suspicious = true })}, nil
	}
	r.press("r", "2")
	r.press("a") // newest is suspicious
	r.wantScreen("Acknowledge the size change")
	r.typeText("nope")
	r.press("enter")
	if len(r.fake.CallsTo("Acknowledge")) != 0 {
		t.Fatal("acknowledged with a wrong word")
	}
	r.press("esc")
	r.press("a")
	r.m.dlg.(*confirmDialog).in.SetValue("acknowledge")
	r.press("enter")
	calls := r.fake.CallsTo("Acknowledge")
	if len(calls) != 1 || calls[0].Args[1] != "acknowledge" {
		t.Fatalf("calls: %v", r.fake)
	}

	r.press("K")
	if len(r.fake.CallsTo("Keep")) != 1 {
		t.Errorf("K should keep the snapshot: %v", r.fake)
	}
	r.press("down", "a")
	r.wantScreen("is not marked suspicious")
}

func TestAppendOnlyHidesAndRefusesTagChanges(t *testing.T) {
	r := healthy(newRig(t))
	status := r.fake.StatusFn
	r.fake.StatusFn = func(ctx context.Context) (service.Status, error) {
		st, _ := status(ctx)
		st.AppendOnly = true
		st.ForgetHeld = true
		return st, nil
	}
	r.load()
	r.wantNoScreen("approve the held forget")
	r.press("2", "K")
	r.press("U")
	if len(r.fake.CallsTo("Keep"))+len(r.fake.CallsTo("Unkeep")) != 0 {
		t.Error("tags changed on an append-only repository")
	}
	r.press("3", "U")
	if r.m.dlg != nil {
		t.Error("remove all locks is offered on an append-only repository")
	}
}

func TestForgetPreviewAndApproval(t *testing.T) {
	r := healthy(newRig(t))
	status := r.fake.StatusFn
	r.fake.StatusFn = func(ctx context.Context) (service.Status, error) {
		st, _ := status(ctx)
		st.ForgetHeld = true
		return st, nil
	}
	r.fake.RetentionFn = func(context.Context) (service.RetentionInfo, error) {
		return service.RetentionInfo{Policy: "the last 1 snapshots", Hosts: []string{"app"}, Total: 3, Held: true, Remove: []service.Snapshot{snap("aaaaaaaa", 48*time.Hour), snap("bbbbbbbb", 24*time.Hour)}}, nil
	}
	r.load()
	r.wantScreen("F  approve the held forget")
	r.press("2", "f")
	r.wantScreen("would remove 2 snapshots", "aaaaaaaa", "held back until it is approved", "f back to the list")
	r.wantNoScreen("enter restore", "keep/unkeep")
	// the keys of the list do nothing to the snapshots out of view
	r.press("down", "K", "a", "d", "o", "t", "enter")
	if r.m.dlg != nil || r.m.cursor[tabSnapshots] != 0 || len(r.fake.CallsTo("Keep"))+len(r.fake.CallsTo("Unkeep"))+len(r.fake.CallsTo("Diff")) != 0 {
		t.Errorf("a key of the list acted in the forget preview: dialog %T, cursor %d, %v", r.m.dlg, r.m.cursor[tabSnapshots], r.fake)
	}
	r.press("f")
	r.wantScreen("ORIGIN", "enter restore")
	r.press("f", "esc")
	r.wantScreen("ORIGIN")

	r.press("1", "F")
	r.wantScreen("Approve the held forget")
	r.m.dlg.(*confirmDialog).in.SetValue("forget")
	r.press("enter")
	if calls := r.fake.CallsTo("ApproveForget"); len(calls) != 1 || calls[0].Args[0] != "forget" {
		t.Errorf("calls: %v", r.fake)
	}
}

func TestUnlock(t *testing.T) {
	r := healthy(newRig(t))
	r.fake.LocksFn = func(context.Context) ([]service.Lock, error) {
		return []service.Lock{{ID: "1234567890", Age: service.Duration(2 * time.Hour), Hostname: "old", Stale: true}}, nil
	}
	r.load()
	r.press("3")
	r.wantScreen("1234567", "stale", "old")
	r.press("u")
	if calls := r.fake.CallsTo("Unlock"); len(calls) != 1 || calls[0].Args[0] != false {
		t.Errorf("calls: %v", r.fake)
	}
	r.press("U")
	r.wantScreen("Remove all locks")
	r.m.dlg.(*confirmDialog).in.SetValue("remove-all")
	r.press("enter")
	if calls := r.fake.CallsTo("Unlock"); len(calls) != 2 || calls[1].Args[0] != true || calls[1].Args[1] != "remove-all" {
		t.Errorf("calls: %v", r.fake)
	}
}

func TestLiveOperation(t *testing.T) {
	r := healthy(newRig(t)).load()
	op := &service.Operation{ID: "1", Kind: service.KindBackup, Source: "schedule", Started: t0.Add(-time.Minute), Phase: "backup"}
	status := r.fake.StatusFn
	r.fake.StatusFn = func(ctx context.Context) (service.Status, error) {
		st, _ := status(ctx)
		st.Operation = op
		return st, nil
	}
	r.send(eventMsg{service.Event{Type: service.EventStarted, Operation: op}})
	r.wantScreen("Backing up · backup, started by schedule, running for 1m", "c cancel")
	r.wantNoScreen("Operation") // the panel shows it, not the details as well
	r.send(eventMsg{service.Event{Type: service.EventLog, Line: "scanned 12 files"}})
	op2 := *op
	op2.Progress = &service.Progress{Percent: 0.5, Current: "/data/file"}
	r.send(eventMsg{service.Event{Type: service.EventProgress, Operation: &op2}})
	r.wantScreen("scanned 12 files", "50%", "/data/file")
	if n := strings.Count(r.screen(), "50%"); n != 1 {
		t.Errorf("the progress is shown %d times:\n%s", n, r.screen())
	}

	r.press("c")
	if len(r.fake.CallsTo("Cancel")) != 1 {
		t.Errorf("c did not cancel: %v", r.fake)
	}

	r.send(eventMsg{service.Event{Type: service.EventFinished, Result: &service.RunResult{Kind: service.KindBackup, Result: service.ResultSuccess, Snapshot: "dddddddd00", Duration: service.Duration(time.Minute)}}})
	r.fake.StatusFn = status
	r.send(eventMsg{service.Event{Type: service.EventFinished, Result: &service.RunResult{Kind: service.KindBackup, Result: service.ResultSuccess, Snapshot: "dddddddd00", Duration: service.Duration(time.Minute)}}})
	r.wantScreen("backup: success", "snapshot dddddddd")
	r.wantNoScreen("Backing up")
}

func TestRetryStaysCancellableAfterTheFailure(t *testing.T) {
	r := healthy(newRig(t))
	status := r.fake.StatusFn
	r.fake.StatusFn = func(ctx context.Context) (service.Status, error) {
		st, _ := status(ctx)
		st.Retry = &service.RetryInfo{Attempt: 2, Max: 3, At: t0.Add(5 * time.Minute)}
		return st, nil
	}
	r.load()
	// the failed backup is the last result, and its retry waits
	r.send(eventMsg{service.Event{Type: service.EventFinished, Result: &service.RunResult{Kind: service.KindBackup, Result: service.ResultFailure, Error: "repository not reachable", WillRetry: true}}})
	r.wantScreen("backup: failure", "tried again (2/3)", "c cancels the retry", "c cancel ·")
	r.press("c")
	if len(r.fake.CallsTo("Cancel")) != 1 {
		t.Errorf("c did not cancel the retry: %v", r.fake)
	}
}

func TestNotesForInformationAreNotAlarms(t *testing.T) {
	r := healthy(newRig(t))
	status := r.fake.StatusFn
	r.fake.StatusFn = func(ctx context.Context) (service.Status, error) {
		st, _ := status(ctx)
		st.Warnings = []service.Warning{{Code: "maintenance", Severity: service.SeverityInfo, Message: "scheduled backups are paused"}}
		return st, nil
	}
	r.load()
	r.wantScreen("Notes", "scheduled backups are paused")
	r.wantNoScreen("Needs attention")
}

func TestInputsShowTheirPlaceholder(t *testing.T) {
	r := healthy(newRig(t)).load()
	r.press("b")
	r.wantScreen("> e.g. before the upgrade")
	r.press("esc", "2", "enter")
	r.wantScreen("> /path/to/directory", "> /path/in/snapshot")
}

func TestLogWrapsLongLines(t *testing.T) {
	r := healthy(newRig(t)).load()
	long := "snapshot 2cfa8ce2 of [/home/someone/workspace/mcs-backup/local/s3-demo/data] at 2026-10-29 13:45:00.986555142 +0100 CET by someone@host"
	r.fake.LogFn = func(_ context.Context, kind service.Kind) (service.Log, error) {
		return service.Log{Kind: kind, Lines: []string{long, "[0:00]          0 packs copied"}}, nil
	}
	r.press("l")
	r.wantScreen("+0100 CET by someone@host", "[0:00]          0 packs copied") // the end of the long line, and the spaces kept
	r.wantNoScreen("…")
}

func TestCancelWithoutOperation(t *testing.T) {
	r := healthy(newRig(t)).load()
	r.press("c")
	r.wantScreen("nothing to cancel")
	if len(r.fake.CallsTo("Cancel")) != 0 {
		t.Error("Cancel was called")
	}
}

func TestLogDialog(t *testing.T) {
	r := healthy(newRig(t)).load()
	r.fake.LogFn = func(_ context.Context, kind service.Kind) (service.Log, error) {
		return service.Log{Kind: kind, Lines: []string{"first line", "second line"}}, nil
	}
	r.press("l")
	r.wantScreen("Log of the last backup run", "first line", "second line")
	r.send(eventMsg{service.Event{Type: service.EventLog, Line: "third line"}})
	r.wantScreen("third line")
	r.press("esc")
	r.wantNoScreen("first line")
}

func TestSchedule(t *testing.T) {
	r := healthy(newRig(t)).load()
	r.press("s")
	// the backups run: the menu says so and offers no Enable
	r.wantScreen("Scheduled backups", "Now: backups enabled (0 2 * * *)", "Disable until enabled again", "Pause for 4h")
	r.wantNoScreen("Enable")
	r.press("enter")
	calls := r.fake.CallsTo("SetSchedule")
	if len(calls) != 1 || calls[0].Args[0].(service.ScheduleRequest).Action != service.ScheduleDisable {
		t.Fatalf("calls: %v", r.fake)
	}

	r.press("s", "down", "down", "enter") // 4h
	req := r.fake.CallsTo("SetSchedule")[1].Args[0].(service.ScheduleRequest)
	if req.Action != service.ScheduleMaintenance || req.For.Std() != 4*time.Hour {
		t.Errorf("request = %+v", req)
	}

	r.press("s", "down", "down", "down", "down", "enter") // another time
	r.wantScreen("Maintenance window")
	r.typeText("soon")
	r.press("enter")
	r.wantScreen("Not a duration")
	r.m.dlg.(*promptDialog).in.SetValue("90m")
	r.press("enter")
	req = r.fake.CallsTo("SetSchedule")[2].Args[0].(service.ScheduleRequest)
	if req.For.Std() != 90*time.Minute {
		t.Errorf("request = %+v", req)
	}
}

func TestScheduleMenuFitsTheState(t *testing.T) {
	for _, c := range []struct {
		name     string
		sched    service.ScheduleStatus
		want, no []string
	}{
		{"disabled", service.ScheduleStatus{Backup: "0 2 * * *"}, []string{"Now: backups are disabled", "> Enable"}, []string{"Disable", "Pause"}},
		{"paused", service.ScheduleStatus{Enabled: true, Backup: "0 2 * * *", MaintenanceUntil: t0.Add(time.Hour)}, []string{"Now: paused for maintenance", "> Enable", "Disable", "Pause for 4h"}, nil},
		{"manual", service.ScheduleStatus{Enabled: true}, []string{"Now: no backup schedule"}, []string{"Enable", "Disable", "Pause"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := healthy(newRig(t))
			status := r.fake.StatusFn
			r.fake.StatusFn = func(ctx context.Context) (service.Status, error) {
				st, _ := status(ctx)
				st.Schedule = c.sched
				return st, nil
			}
			r.load()
			r.press("s")
			r.wantScreen(c.want...)
			r.wantNoScreen(c.no...)
		})
	}
}

func TestWebUILoginLink(t *testing.T) {
	r := healthy(newRig(t)).load()
	r.fake.WebUIFn = func(context.Context) (service.WebUIStatus, error) { return service.WebUIStatus{}, nil }
	r.fake.LoginLinkFn = func(context.Context) (string, error) { return "http://127.0.0.1:8080/login?code=abc", nil }
	r.press("w")
	if calls := r.fake.CallsTo("SetWebUI"); len(calls) != 1 || !calls[0].Args[0].(service.WebUIRequest).Enable {
		t.Errorf("the web UI should be switched on: %v", r.fake)
	}
	r.wantScreen("http://127.0.0.1:8080/login?code=abc", "once and for two minutes", "kubectl port-forward")
	r.wantNoScreen("│", "╭") // no frame around the link: a triple click selects it alone
	r.press("o")
	if r.opened != "http://127.0.0.1:8080/login?code=abc" {
		t.Errorf("opened = %q", r.opened)
	}
	r.press("d")
	if calls := r.fake.CallsTo("SetWebUI"); len(calls) != 2 || calls[1].Args[0].(service.WebUIRequest).Enable {
		t.Errorf("d should switch the web UI off: %v", r.fake)
	}
}

func TestWebUILoginLinkWithoutABrowser(t *testing.T) {
	r := healthy(newRig(t))
	r.m.o.Open = nil // in a container or an SSH session
	r.load()
	r.fake.WebUIFn = func(context.Context) (service.WebUIStatus, error) { return service.WebUIStatus{Enabled: true}, nil }
	r.fake.LoginLinkFn = func(context.Context) (string, error) { return "http://127.0.0.1:8080/login?code=abc", nil }
	r.press("w")
	r.wantScreen("http://127.0.0.1:8080/login?code=abc", "kubectl port-forward", "d switch the web UI off")
	r.wantNoScreen("o open here")
	r.press("o")
	if r.m.dlg == nil {
		t.Error("o closed the dialog")
	}
}

func TestPlainDialogsShowMessages(t *testing.T) {
	r := healthy(newRig(t))
	r.m.o.Open = func(string) error { return errors.New("no browser found") }
	r.load()
	r.fake.WebUIFn = func(context.Context) (service.WebUIStatus, error) { return service.WebUIStatus{Enabled: true}, nil }
	r.fake.LoginLinkFn = func(context.Context) (string, error) { return "http://127.0.0.1:8080/login?code=abc", nil }
	r.press("w", "o")
	// the link stays, and the reason is on the screen with it
	r.wantScreen("http://127.0.0.1:8080/login?code=abc", "open browser: no browser found")
}

func TestWebUIUnavailable(t *testing.T) {
	r := healthy(newRig(t)).load()
	r.fake.WebUIFn = func(context.Context) (service.WebUIStatus, error) {
		return service.WebUIStatus{}, service.ErrUnavailable
	}
	r.press("w")
	r.wantScreen("not available in this configuration")
	if r.m.dlg != nil {
		t.Error("a dialog opened")
	}
}

func TestHelpAndQuit(t *testing.T) {
	r := healthy(newRig(t)).load()
	r.press("?")
	r.wantScreen("Keys", "Backup", "Acknowledge")
	r.press("x")
	r.wantNoScreen("Acknowledge")
	if r.quit {
		t.Fatal("quit too early")
	}
	r.press("q")
	if !r.quit {
		t.Error("q did not quit")
	}
}

func TestDialogsFitALowTerminal(t *testing.T) {
	r := healthy(newRig(t)).load()
	r.m.width, r.m.height = 80, 24

	r.press("?")
	r.wantWholeDialog("any other key closes")
	r.wantNoScreen("q quit", "test notification") // the legend gives the dialog its room
	r.press("G")
	r.wantScreen("test notification")
	r.wantWholeDialog("any other key closes")
	r.press("x")
	r.wantNoScreen("Keys")

	log := make([]string, 60)
	for i := range log {
		log[i] = fmt.Sprintf("log line %02d", i)
	}
	r.fake.LogFn = func(_ context.Context, kind service.Kind) (service.Log, error) {
		return service.Log{Kind: kind, Lines: log}, nil
	}
	r.press("l")
	r.wantWholeDialog("esc close")
	r.wantScreen("log line 59", "of 60") // the end, where new lines come in
	r.press("g")
	r.wantScreen("log line 00", "lines 1-")
	r.press("esc")

	r.fake.FilesFn = func(context.Context, service.FilesRequest) (service.FileList, error) {
		list := service.FileList{Snapshot: snap("cccccccc", time.Hour), Path: "/"}
		for i := range 40 {
			list.Files = append(list.Files, service.File{Path: fmt.Sprintf("/file-%02d", i), Type: "file", Size: 1, Mtime: t0})
		}
		return list, nil
	}
	r.press("2", "o")
	r.wantWholeDialog("esc close")
	r.press("pgdown", "pgdown", "pgdown", "pgdown")
	r.wantScreen("> file-39")
	r.wantWholeDialog("esc close")
	r.press("esc")

	r.fake.RestorePreflightFn = func(_ context.Context, req service.RestoreRequest) (service.RestorePreflight, error) {
		c := &service.RestoreChanges{Overwritten: 40, Deleted: 40}
		for i := range 20 {
			c.DeletedPaths = append(c.DeletedPaths, fmt.Sprintf("/data/deleted-%02d", i))
			c.OverwrittenPaths = append(c.OverwrittenPaths, fmt.Sprintf("/data/overwritten-%02d", i))
		}
		return service.RestorePreflight{Snapshot: snap("cccccccc", time.Hour), Target: "/data", InPlace: true, Mode: req.Mode, Changes: c}, nil
	}
	r.press("enter", "enter") // the form for the newest snapshot, then its check
	r.wantWholeDialog("esc back")
	r.wantScreen("Deletes", "/data/deleted-00")
	r.wantNoScreen("/data/overwritten-19", "p what would change") // what would change is in view already
	r.press("G")
	r.wantScreen("/data/overwritten-19")
	r.wantWholeDialog("esc back")
	r.press("enter") // the keys of the preview still work
	r.wantScreen("Type cccccccc to confirm")
}

func TestDialogCapturesKeys(t *testing.T) {
	r := healthy(newRig(t)).load()
	r.press("b")
	r.typeText("quit q c")
	if r.quit || len(r.fake.CallsTo("Cancel")) != 0 {
		t.Error("keys typed into a dialog reached the tabs")
	}
	r.press("esc")
	if r.m.dlg != nil {
		t.Error("esc did not close the dialog")
	}
}

func TestLongValuesWrapUnderTheirColumn(t *testing.T) {
	r := healthy(newRig(t)).load()
	long := "/home/someone/workspace/a-project/with/a/very/long/path/to/the/data/directory/that/does/not/fit/in/the/box"
	r.fake.RestorePreflightFn = func(_ context.Context, req service.RestoreRequest) (service.RestorePreflight, error) {
		return service.RestorePreflight{Snapshot: snap("cccccccc", time.Hour), Target: long, InPlace: true, Mode: req.Mode, SafetySnapshot: true}, nil
	}
	r.press("2", "enter", "enter")
	r.wantScreen("Target", "Safety snapshot", "that/does/not/fit/in/the/box")
	lines := strings.Split(r.screen(), "\n")
	for i, l := range lines {
		if w := len([]rune(l)); w > r.m.width {
			t.Errorf("line %d is %d cells wide on a terminal of %d: %q", i, w, r.m.width, l)
		}
	}
	// a wrapped value continues under the value column, not under the label
	indent := strings.Repeat(" ", len("Safety snapshot")+2)
	for i, l := range lines {
		if strings.Contains(l, "Target") {
			if next := strings.TrimPrefix(lines[i+1], "│ "); !strings.HasPrefix(next, indent) {
				t.Errorf("the continuation of the target is not indented: %q", lines[i+1])
			}
		}
	}
}

func TestPanelAndLegendStayAtTheBottom(t *testing.T) {
	r := healthy(newRig(t)).load()
	r.send(eventMsg{service.Event{Type: service.EventFinished, Result: &service.RunResult{Kind: service.KindRestore, Result: service.ResultSuccess, Snapshot: "dddddddd00", Duration: service.Duration(4 * time.Second)}}})
	lastLine := func(screen, text string) int {
		t.Helper()
		lines := strings.Split(screen, "\n")
		for i, line := range slices.Backward(lines) {
			if strings.Contains(line, text) {
				return i
			}
		}
		t.Fatalf("the screen lacks %q:\n%s", text, screen)
		return -1
	}
	legend, panel, height := make([]int, 0, 4), make([]int, 0, 4), make([]int, 0, 4)
	for _, tab := range []string{"1", "2", "3", "4"} {
		r.press(tab)
		s := r.screen()
		legend = append(legend, lastLine(s, "q quit"))
		panel = append(panel, lastLine(s, "╰"))
		height = append(height, len(strings.Split(s, "\n")))
	}
	// a blank line separates the panel from the content above it
	lines := strings.Split(r.screen(), "\n")
	top := -1
	for i, l := range lines {
		if strings.HasPrefix(l, "╭") {
			top = i
		}
	}
	if top < 1 || strings.TrimSpace(lines[top-1]) != "" {
		t.Errorf("no blank line above the panel:\n%s", r.screen())
	}
	for i := 1; i < len(legend); i++ {
		if legend[i] != legend[0] || panel[i] != panel[0] || height[i] != r.m.height {
			t.Fatalf("the bottom moves between tabs: legend %v, panel %v, height %v (terminal %d)", legend, panel, height, r.m.height)
		}
	}
}

func TestSnapshotColumnsStayAlignedWithLongAges(t *testing.T) {
	r := healthy(newRig(t))
	r.fake.SnapshotsFn = func(context.Context) ([]service.Snapshot, error) {
		return []service.Snapshot{snap("aaaaaaaa", time.Hour), snap("bbbbbbbb", 15*time.Hour+11*time.Minute), snap("cccccccc", 100*24*time.Hour+23*time.Hour)}, nil
	}
	r.load().press("2")
	col := -1
	for l := range strings.SplitSeq(r.screen(), "\n") {
		if !strings.Contains(l, "scheduled") {
			continue
		}
		if i := len([]rune(l[:strings.Index(l, "scheduled")])); col >= 0 && i != col {
			t.Errorf("the origin column moves: %q", l)
		} else {
			col = i
		}
	}
	if col < 0 {
		t.Fatalf("no snapshot rows:\n%s", r.screen())
	}
}

func TestOverviewNeverCutsAFrame(t *testing.T) {
	r := healthy(newRig(t))
	status := r.fake.StatusFn
	op := &service.Operation{ID: "1", Kind: service.KindBackup, Source: "manual", Started: t0.Add(-time.Minute), Phase: "backup", Progress: &service.Progress{Percent: 0.5}}
	r.fake.StatusFn = func(ctx context.Context) (service.Status, error) {
		st, _ := status(ctx)
		st.Operation = op
		st.Copy = &service.RepoStatus{Up: true, Snapshots: 2}
		st.Warnings = []service.Warning{{Code: "backup_suspicious", Severity: service.SeverityError, Message: "a snapshot is tagged suspicious: check it and acknowledge it"}}
		return st, nil
	}
	r.load()
	r.send(eventMsg{service.Event{Type: service.EventStarted, Operation: op}})
	for h := 24; h <= 50; h++ {
		r.m.height = h
		s := r.screen()
		if opens, closes := strings.Count(s, "╭"), strings.Count(s, "╰"); opens != closes {
			t.Fatalf("height %d: %d frames opened and %d closed:\n%s", h, opens, closes, s)
		}
		if !strings.Contains(s, "Needs attention") {
			t.Fatalf("height %d: the attention frame is gone:\n%s", h, s)
		}
		if !strings.Contains(s, "q quit") || !strings.Contains(s, "Backing up") {
			t.Fatalf("height %d: the panel or the legend is gone:\n%s", h, s)
		}
	}
}

func TestShellLinesStayReadableForAShell(t *testing.T) {
	values := []string{
		"s3:http://127.0.0.1:9000/backuptest/mcs-backup-demo/20261028-162121",
		"it's a 'quoted' secret with spaces and a backslash \\ at the end of a long enough value to wrap around",
		strings.Repeat("0123456789", 12),
	}
	for _, width := range []int{20, 33, 40, 61, 80} {
		for _, v := range values {
			script := "export NAME=" + shellQuoteForTest(v) + "\n"
			lines := shellLines(script, width)
			for _, l := range lines {
				if n := len([]rune(l)); n > width {
					t.Errorf("width %d: a line is %d cells wide: %q", width, n, l)
				}
			}
			cmd := exec.CommandContext(t.Context(), "sh", "-c", strings.Join(lines, "\n")+"\nprintf %s \"$NAME\"")
			out, err := cmd.Output()
			if err != nil || string(out) != v {
				t.Errorf("width %d: the shell reads %q (%v), want %q\n%s", width, out, err, v, strings.Join(lines, "\n"))
			}
		}
	}
	if got := shellLines("# a note that is longer than the line", 20); len(got) < 2 || !strings.HasPrefix(got[1], "# ") {
		t.Errorf("a comment does not continue as a comment: %q", got)
	}
}

// shellQuoteForTest quotes like envexport: in single quotes, with ' as '\”.
func shellQuoteForTest(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func TestResizeRepaintsTheScreen(t *testing.T) {
	r := healthy(newRig(t)).load()
	_, cmd := r.m.Update(tea.WindowSizeMsg{Width: 90, Height: 30})
	if cmd == nil {
		t.Fatal("a new size does not clear the screen")
	}
	if _, cmd := r.m.Update(tea.WindowSizeMsg{Width: 90, Height: 30}); cmd != nil {
		t.Error("the same size clears the screen again")
	}
	if r.m.width != 90 || r.m.height != 30 {
		t.Errorf("size = %dx%d", r.m.width, r.m.height)
	}
}
