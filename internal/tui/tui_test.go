package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

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
	if !strings.Contains(r.copied, "export RESTIC_PASSWORD='hunter2'") {
		t.Errorf("copied = %q", r.copied)
	}
	r.wantScreen("copied to the clipboard")
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
	r.wantScreen("would remove 2 snapshots", "aaaaaaaa", "held back until it is approved")

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
	r.wantScreen("Running: backup running for 1m", "c cancel")
	r.send(eventMsg{service.Event{Type: service.EventLog, Line: "scanned 12 files"}})
	op2 := *op
	op2.Progress = &service.Progress{Percent: 0.5, Current: "/data/file"}
	r.send(eventMsg{service.Event{Type: service.EventProgress, Operation: &op2}})
	r.wantScreen("scanned 12 files", "50%", "/data/file")

	r.press("c")
	if len(r.fake.CallsTo("Cancel")) != 1 {
		t.Errorf("c did not cancel: %v", r.fake)
	}

	r.send(eventMsg{service.Event{Type: service.EventFinished, Result: &service.RunResult{Kind: service.KindBackup, Result: service.ResultSuccess, Snapshot: "dddddddd00", Duration: service.Duration(time.Minute)}}})
	r.fake.StatusFn = status
	r.send(eventMsg{service.Event{Type: service.EventFinished, Result: &service.RunResult{Kind: service.KindBackup, Result: service.ResultSuccess, Snapshot: "dddddddd00", Duration: service.Duration(time.Minute)}}})
	r.wantScreen("backup: success", "snapshot dddddddd")
	r.wantNoScreen("Running:")
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
	r.wantScreen("Scheduled backups", "Disable until enabled again", "Pause for 4h")
	r.press("down", "enter")
	calls := r.fake.CallsTo("SetSchedule")
	if len(calls) != 1 || calls[0].Args[0].(service.ScheduleRequest).Action != service.ScheduleDisable {
		t.Fatalf("calls: %v", r.fake)
	}

	r.press("s", "down", "down", "down", "enter") // 4h
	req := r.fake.CallsTo("SetSchedule")[1].Args[0].(service.ScheduleRequest)
	if req.Action != service.ScheduleMaintenance || req.For.Std() != 4*time.Hour {
		t.Errorf("request = %+v", req)
	}

	r.press("s", "down", "down", "down", "down", "down", "enter") // another time
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

func TestWebUILoginLink(t *testing.T) {
	r := healthy(newRig(t)).load()
	r.fake.WebUIFn = func(context.Context) (service.WebUIStatus, error) { return service.WebUIStatus{}, nil }
	r.fake.LoginLinkFn = func(context.Context) (string, error) { return "http://127.0.0.1:8080/login?code=abc", nil }
	r.press("w")
	if calls := r.fake.CallsTo("SetWebUI"); len(calls) != 1 || !calls[0].Args[0].(service.WebUIRequest).Enable {
		t.Errorf("the web UI should be switched on: %v", r.fake)
	}
	r.wantScreen("http://127.0.0.1:8080/login?code=abc", "once and for two minutes", "kubectl port-forward")
	r.press("c")
	if r.copied != "http://127.0.0.1:8080/login?code=abc" {
		t.Errorf("copied = %q", r.copied)
	}
	r.press("o")
	if r.opened != r.copied {
		t.Errorf("opened = %q", r.opened)
	}
	r.press("d")
	if calls := r.fake.CallsTo("SetWebUI"); len(calls) != 2 || calls[1].Args[0].(service.WebUIRequest).Enable {
		t.Errorf("d should switch the web UI off: %v", r.fake)
	}
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
