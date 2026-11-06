// Package servicetest provides a scriptable service.Service for tests of the
// API, the CLI, the TUI, the web UI and the MCP server.
package servicetest

import (
	"context"
	"fmt"
	"iter"
	"sync"

	"github.com/noris-network/mcs-backup/v2/internal/service"
)

// Call is one recorded method call.
type Call struct {
	Name string
	Args []any
	// Source is the request source recorded in the context.
	Source string
}

// Fake implements service.Service. Every method calls its function field if
// set and otherwise returns zero values. Calls are recorded.
type Fake struct {
	mu    sync.Mutex
	calls []Call

	StatusFn           func(ctx context.Context) (service.Status, error)
	SnapshotsFn        func(ctx context.Context) ([]service.Snapshot, error)
	RetentionFn        func(ctx context.Context) (service.RetentionInfo, error)
	LocksFn            func(ctx context.Context) ([]service.Lock, error)
	FilesFn            func(ctx context.Context, req service.FilesRequest) (service.FileList, error)
	FindFn             func(ctx context.Context, req service.FindRequest) (service.FindResult, error)
	DiffFn             func(ctx context.Context, req service.DiffRequest) (service.DiffResult, error)
	RestorePreflightFn func(ctx context.Context, req service.RestoreRequest) (service.RestorePreflight, error)
	OperationFn        func(ctx context.Context) (*service.Operation, error)
	LogFn              func(ctx context.Context, kind service.Kind) (service.Log, error)
	EventsFn           func(ctx context.Context) iter.Seq2[service.Event, error]
	EnvFn              func(ctx context.Context, reveal bool) (service.EnvInfo, error)
	StartBackupFn      func(ctx context.Context, req service.BackupRequest) (service.Operation, error)
	StartRestoreFn     func(ctx context.Context, req service.RestoreRequest) (service.Operation, error)
	StartVerifyFn      func(ctx context.Context, req service.VerifyRequest) (service.Operation, error)
	ApproveForgetFn    func(ctx context.Context, confirm string) (service.Operation, error)
	CancelFn           func(ctx context.Context) error
	KeepFn             func(ctx context.Context, ref string) (service.TagResult, error)
	UnkeepFn           func(ctx context.Context, ref string) (service.TagResult, error)
	AcknowledgeFn      func(ctx context.Context, ref, confirm string) (service.TagResult, error)
	UnlockFn           func(ctx context.Context, all bool, confirm string) error
	SetScheduleFn      func(ctx context.Context, req service.ScheduleRequest) (service.Status, error)
	NotifyTestFn       func(ctx context.Context) error
	WebUIFn            func(ctx context.Context) (service.WebUIStatus, error)
	SetWebUIFn         func(ctx context.Context, req service.WebUIRequest) (service.WebUIStatus, error)
	LoginLinkFn        func(ctx context.Context) (string, error)
}

var _ service.Service = (*Fake)(nil)

func (f *Fake) record(ctx context.Context, name string, args ...any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, Call{Name: name, Args: args, Source: service.SourceFrom(ctx)})
}

// Calls returns all recorded calls.
func (f *Fake) Calls() []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Call(nil), f.calls...)
}

// CallsTo returns the recorded calls of one method.
func (f *Fake) CallsTo(name string) []Call {
	var out []Call
	for _, c := range f.Calls() {
		if c.Name == name {
			out = append(out, c)
		}
	}
	return out
}

// String lists the recorded calls, for failure messages.
func (f *Fake) String() string { return fmt.Sprint(f.Calls()) }

func (f *Fake) Status(ctx context.Context) (service.Status, error) {
	f.record(ctx, "Status")
	if f.StatusFn != nil {
		return f.StatusFn(ctx)
	}
	return service.Status{}, nil
}

func (f *Fake) Snapshots(ctx context.Context) ([]service.Snapshot, error) {
	f.record(ctx, "Snapshots")
	if f.SnapshotsFn != nil {
		return f.SnapshotsFn(ctx)
	}
	return nil, nil
}

func (f *Fake) Retention(ctx context.Context) (service.RetentionInfo, error) {
	f.record(ctx, "Retention")
	if f.RetentionFn != nil {
		return f.RetentionFn(ctx)
	}
	return service.RetentionInfo{}, nil
}

func (f *Fake) Locks(ctx context.Context) ([]service.Lock, error) {
	f.record(ctx, "Locks")
	if f.LocksFn != nil {
		return f.LocksFn(ctx)
	}
	return nil, nil
}

func (f *Fake) Files(ctx context.Context, req service.FilesRequest) (service.FileList, error) {
	f.record(ctx, "Files", req)
	if f.FilesFn != nil {
		return f.FilesFn(ctx, req)
	}
	return service.FileList{}, nil
}

func (f *Fake) Find(ctx context.Context, req service.FindRequest) (service.FindResult, error) {
	f.record(ctx, "Find", req)
	if f.FindFn != nil {
		return f.FindFn(ctx, req)
	}
	return service.FindResult{}, nil
}

func (f *Fake) Diff(ctx context.Context, req service.DiffRequest) (service.DiffResult, error) {
	f.record(ctx, "Diff", req)
	if f.DiffFn != nil {
		return f.DiffFn(ctx, req)
	}
	return service.DiffResult{}, nil
}

func (f *Fake) RestorePreflight(ctx context.Context, req service.RestoreRequest) (service.RestorePreflight, error) {
	f.record(ctx, "RestorePreflight", req)
	if f.RestorePreflightFn != nil {
		return f.RestorePreflightFn(ctx, req)
	}
	return service.RestorePreflight{}, nil
}

func (f *Fake) Operation(ctx context.Context) (*service.Operation, error) {
	f.record(ctx, "Operation")
	if f.OperationFn != nil {
		return f.OperationFn(ctx)
	}
	return nil, nil
}

func (f *Fake) Log(ctx context.Context, kind service.Kind) (service.Log, error) {
	f.record(ctx, "Log", kind)
	if f.LogFn != nil {
		return f.LogFn(ctx, kind)
	}
	return service.Log{Kind: kind}, nil
}

func (f *Fake) Events(ctx context.Context) iter.Seq2[service.Event, error] {
	f.record(ctx, "Events")
	if f.EventsFn != nil {
		return f.EventsFn(ctx)
	}
	return func(func(service.Event, error) bool) { <-ctx.Done() }
}

func (f *Fake) Env(ctx context.Context, reveal bool) (service.EnvInfo, error) {
	f.record(ctx, "Env", reveal)
	if f.EnvFn != nil {
		return f.EnvFn(ctx, reveal)
	}
	return service.EnvInfo{}, nil
}

func (f *Fake) StartBackup(ctx context.Context, req service.BackupRequest) (service.Operation, error) {
	f.record(ctx, "StartBackup", req)
	if f.StartBackupFn != nil {
		return f.StartBackupFn(ctx, req)
	}
	return service.Operation{ID: "op1", Kind: service.KindBackup}, nil
}

func (f *Fake) StartRestore(ctx context.Context, req service.RestoreRequest) (service.Operation, error) {
	f.record(ctx, "StartRestore", req)
	if f.StartRestoreFn != nil {
		return f.StartRestoreFn(ctx, req)
	}
	return service.Operation{ID: "op1", Kind: service.KindRestore}, nil
}

func (f *Fake) StartVerify(ctx context.Context, req service.VerifyRequest) (service.Operation, error) {
	f.record(ctx, "StartVerify", req)
	if f.StartVerifyFn != nil {
		return f.StartVerifyFn(ctx, req)
	}
	return service.Operation{ID: "op1", Kind: service.KindVerify}, nil
}

func (f *Fake) ApproveForget(ctx context.Context, confirm string) (service.Operation, error) {
	f.record(ctx, "ApproveForget", confirm)
	if f.ApproveForgetFn != nil {
		return f.ApproveForgetFn(ctx, confirm)
	}
	return service.Operation{ID: "op1", Kind: service.KindForget}, nil
}

func (f *Fake) Cancel(ctx context.Context) error {
	f.record(ctx, "Cancel")
	if f.CancelFn != nil {
		return f.CancelFn(ctx)
	}
	return nil
}

func (f *Fake) Keep(ctx context.Context, ref string) (service.TagResult, error) {
	f.record(ctx, "Keep", ref)
	if f.KeepFn != nil {
		return f.KeepFn(ctx, ref)
	}
	return service.TagResult{ID: ref + "-kept"}, nil
}

func (f *Fake) Unkeep(ctx context.Context, ref string) (service.TagResult, error) {
	f.record(ctx, "Unkeep", ref)
	if f.UnkeepFn != nil {
		return f.UnkeepFn(ctx, ref)
	}
	return service.TagResult{ID: ref + "-unkept"}, nil
}

func (f *Fake) Acknowledge(ctx context.Context, ref, confirm string) (service.TagResult, error) {
	f.record(ctx, "Acknowledge", ref, confirm)
	if f.AcknowledgeFn != nil {
		return f.AcknowledgeFn(ctx, ref, confirm)
	}
	return service.TagResult{ID: ref + "-acked"}, nil
}

func (f *Fake) Unlock(ctx context.Context, all bool, confirm string) error {
	f.record(ctx, "Unlock", all, confirm)
	if f.UnlockFn != nil {
		return f.UnlockFn(ctx, all, confirm)
	}
	return nil
}

func (f *Fake) SetSchedule(ctx context.Context, req service.ScheduleRequest) (service.Status, error) {
	f.record(ctx, "SetSchedule", req)
	if f.SetScheduleFn != nil {
		return f.SetScheduleFn(ctx, req)
	}
	return service.Status{}, nil
}

func (f *Fake) NotifyTest(ctx context.Context) error {
	f.record(ctx, "NotifyTest")
	if f.NotifyTestFn != nil {
		return f.NotifyTestFn(ctx)
	}
	return nil
}

func (f *Fake) WebUI(ctx context.Context) (service.WebUIStatus, error) {
	f.record(ctx, "WebUI")
	if f.WebUIFn != nil {
		return f.WebUIFn(ctx)
	}
	return service.WebUIStatus{}, nil
}

func (f *Fake) SetWebUI(ctx context.Context, req service.WebUIRequest) (service.WebUIStatus, error) {
	f.record(ctx, "SetWebUI", req)
	if f.SetWebUIFn != nil {
		return f.SetWebUIFn(ctx, req)
	}
	return service.WebUIStatus{Enabled: req.Enable}, nil
}

func (f *Fake) LoginLink(ctx context.Context) (string, error) {
	f.record(ctx, "LoginLink")
	if f.LoginLinkFn != nil {
		return f.LoginLinkFn(ctx)
	}
	return "http://127.0.0.1:8080/login?code=abc", nil
}
