package daemon

import (
	"context"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/noris-network/mcs-backup/v2/internal/config"
	"github.com/noris-network/mcs-backup/v2/internal/engine"
	"github.com/noris-network/mcs-backup/v2/internal/notify"
	"github.com/noris-network/mcs-backup/v2/internal/restic"
	"github.com/noris-network/mcs-backup/v2/internal/service"
)

// fakeEngine is a scripted Engine.
type fakeEngine struct {
	mu sync.Mutex

	snaps, secondSnaps []restic.Snapshot
	hasSecond          bool
	locks              []service.Lock
	verifyConfigured   bool

	openMain   func(call int) (engine.RepoInfo, error)
	openSecond func(call int) (engine.RepoInfo, error)
	snapsErr   error
	sizeErr    error
	filesErr   error

	backup  func(ctx context.Context, req service.BackupRequest, sink engine.Sink) engine.BackupResult
	restore func(ctx context.Context, req service.RestoreRequest, sink engine.Sink) engine.RestoreResult
	verify  func(ctx context.Context, req service.VerifyRequest, sink engine.Sink) engine.VerifyResult
	prune   func(ctx context.Context, sink engine.Sink) engine.MaintenanceResult
	check   func(ctx context.Context, sink engine.Sink) engine.MaintenanceResult
	approve func(ctx context.Context, sink engine.Sink) engine.ApproveResult

	calls      []string
	openMains  int
	backupReqs []service.BackupRequest
}

func newFakeEngine() *fakeEngine {
	return &fakeEngine{verifyConfigured: true}
}

func (f *fakeEngine) record(call string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
}

func (f *fakeEngine) called(call string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c == call {
			n++
		}
	}
	return n
}

func (f *fakeEngine) setSnaps(s ...restic.Snapshot) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.snaps = s
}

func (f *fakeEngine) CheckVersion(context.Context) (restic.Version, error) {
	return restic.Version{Major: 0, Minor: 19, Patch: 1}, nil
}

func (f *fakeEngine) OpenMain(context.Context) (engine.RepoInfo, error) {
	f.mu.Lock()
	f.openMains++
	n := f.openMains
	fn := f.openMain
	f.mu.Unlock()
	if fn != nil {
		return fn(n)
	}
	return engine.RepoInfo{ID: "repo1"}, nil
}

func (f *fakeEngine) OpenSecond(context.Context) (engine.RepoInfo, error) {
	if f.openSecond != nil {
		return f.openSecond(1)
	}
	return engine.RepoInfo{ID: "repo2"}, nil
}

func (f *fakeEngine) HasSecond() bool { return f.hasSecond }

func (f *fakeEngine) Snapshots(context.Context) ([]restic.Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.snaps), f.snapsErr
}

func (f *fakeEngine) SecondSnapshots(context.Context) ([]restic.Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.secondSnaps), nil
}

func (f *fakeEngine) RepoSize(context.Context) (restic.Stats, error) {
	f.record("size")
	return restic.Stats{TotalSize: 4096}, f.sizeErr
}

func (f *fakeEngine) Retention(context.Context) (service.RetentionInfo, error) {
	return service.RetentionInfo{Policy: "the last 10 snapshots"}, nil
}

func (f *fakeEngine) Locks(context.Context) ([]service.Lock, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.locks), nil
}

func (f *fakeEngine) Files(_ context.Context, req service.FilesRequest) (service.FileList, error) {
	return service.FileList{Path: req.Path}, f.filesErr
}

func (f *fakeEngine) Find(context.Context, service.FindRequest) (service.FindResult, error) {
	return service.FindResult{Searched: 1}, f.filesErr
}

func (f *fakeEngine) Diff(context.Context, service.DiffRequest) (service.DiffResult, error) {
	return service.DiffResult{}, f.filesErr
}

func (f *fakeEngine) RestorePreflight(context.Context, service.RestoreRequest) (service.RestorePreflight, error) {
	return service.RestorePreflight{InPlace: true}, nil
}

func (f *fakeEngine) VerifyConfigured() bool { return f.verifyConfigured }

func (f *fakeEngine) RestoreNeedsConfirm(req service.RestoreRequest) bool { return req.Target == "" }

func (f *fakeEngine) Backup(ctx context.Context, req service.BackupRequest, sink engine.Sink) engine.BackupResult {
	f.mu.Lock()
	f.calls = append(f.calls, "backup")
	f.backupReqs = append(f.backupReqs, req)
	fn := f.backup
	f.mu.Unlock()
	if fn != nil {
		return fn(ctx, req, sink)
	}
	return engine.BackupResult{Result: service.ResultSuccess, Message: "snapshot created", Snapshot: "new1"}
}

func (f *fakeEngine) Restore(ctx context.Context, req service.RestoreRequest, sink engine.Sink) engine.RestoreResult {
	f.record("restore")
	if f.restore != nil {
		return f.restore(ctx, req, sink)
	}
	return engine.RestoreResult{Result: service.ResultSuccess, Message: "restored", Snapshot: "snap1"}
}

func (f *fakeEngine) Verify(ctx context.Context, req service.VerifyRequest, sink engine.Sink) engine.VerifyResult {
	f.record("verify")
	if f.verify != nil {
		return f.verify(ctx, req, sink)
	}
	return engine.VerifyResult{Result: service.ResultSuccess, Message: "verified"}
}

func (f *fakeEngine) Prune(ctx context.Context, sink engine.Sink) engine.MaintenanceResult {
	f.record("prune")
	if f.prune != nil {
		return f.prune(ctx, sink)
	}
	return engine.MaintenanceResult{Result: service.ResultSuccess}
}

func (f *fakeEngine) Check(ctx context.Context, sink engine.Sink) engine.MaintenanceResult {
	f.record("check")
	if f.check != nil {
		return f.check(ctx, sink)
	}
	return engine.MaintenanceResult{Result: service.ResultSuccess}
}

func (f *fakeEngine) ApproveForget(ctx context.Context, sink engine.Sink) engine.ApproveResult {
	f.record("approve")
	if f.approve != nil {
		return f.approve(ctx, sink)
	}
	return engine.ApproveResult{Result: service.ResultSuccess, Main: engine.ForgetOutcome{Ran: true, Removed: 4}}
}

func (f *fakeEngine) Keep(context.Context, string) (service.TagResult, error) {
	f.record("keep")
	return service.TagResult{ID: "kept"}, nil
}

func (f *fakeEngine) Unkeep(context.Context, string) (service.TagResult, error) {
	f.record("unkeep")
	return service.TagResult{ID: "unkept"}, nil
}

func (f *fakeEngine) Acknowledge(context.Context, string) (service.TagResult, error) {
	f.record("acknowledge")
	return service.TagResult{ID: "acked"}, nil
}

func (f *fakeEngine) ReleaseStaleLocks(context.Context, engine.Sink) {
	f.record("release-locks")
}

func (f *fakeEngine) Unlock(_ context.Context, all bool) error {
	if all {
		f.record("unlock-all")
	} else {
		f.record("unlock")
	}
	return nil
}

// recorder is a Notifier that remembers the events.
type recorder struct {
	mu     sync.Mutex
	events []notify.Event
	errs   int64
	// started is the context of Start; Stop notes the time it was given and
	// whether started had ended by then.
	started    context.Context
	stopLeft   time.Duration
	endedFirst bool
}

func (r *recorder) Start(ctx context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.started = ctx
}

func (r *recorder) Stop(ctx context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if dl, ok := ctx.Deadline(); ok {
		r.stopLeft = time.Until(dl)
	}
	r.endedFirst = r.started.Err() != nil
}

func (r *recorder) Notify(ev notify.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

func (r *recorder) Send(_ context.Context, ev notify.Event) error {
	r.Notify(ev)
	return nil
}
func (r *recorder) Enabled() bool     { return true }
func (r *recorder) Errors() int64     { return r.errs }
func (r *recorder) LastError() string { return "" }

func (r *recorder) names() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, e := range r.events {
		out = append(out, e.Name)
	}
	return out
}

// testDaemon builds a Daemon around a fake engine.
type testDaemon struct {
	*Daemon
	eng      *fakeEngine
	notes    *recorder
	root     string
	stateDir string
}

func newTestDaemon(t *testing.T, mod func(*fakeEngine), env ...string) *testDaemon {
	t.Helper()
	root, stateDir := t.TempDir(), t.TempDir()
	environ := append([]string{
		"PATH=" + os.Getenv("PATH"), "TZ=UTC", "RESTIC_REPOSITORY=/main", "RESTIC_PASSWORD=hunter2-secret",
		`RETENTION_POLICY={"last":10}`, "BACKUP_ROOT=" + root, "MCS_BACKUP_HOST=me",
		"MCS_BACKUP_STATE_DIR=" + stateDir, "PRUNE_SCHEDULE=", "CHECK_SCHEDULE=", "VERIFY_SCHEDULE=",
	}, env...)
	cfg, err := config.Load(environ)
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	eng := newFakeEngine()
	if mod != nil {
		mod(eng)
	}
	notes := &recorder{}
	d := New(Options{
		Config: cfg, Engine: eng, Notifier: notes, Version: "v2-test",
		MinBackoff: 10 * time.Second, MaxBackoff: time.Minute, MonitorInterval: 5 * time.Minute,
	})
	return &testDaemon{Daemon: d, eng: eng, notes: notes, root: root, stateDir: stateDir}
}

// snap builds a snapshot taken age before now.
func snap(id, host string, ago time.Duration, bytes int64, tags ...string) restic.Snapshot {
	return restic.Snapshot{
		ID: id, ShortID: id, Hostname: host, Time: time.Now().Add(-ago), Tags: tags,
		Summary: &restic.SnapshotSummary{TotalBytesProcessed: bytes, TotalFilesProcessed: 1},
	}
}
