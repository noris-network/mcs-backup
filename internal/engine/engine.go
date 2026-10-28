// Package engine implements the flows of mcs-backup (backup, restore, retention,
// restore test, maintenance) on top of restic. It keeps no state between
// calls: the daemon and restore-if-empty drive it.
package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/noris-network/mcs-backup/v2/internal/config"
	"github.com/noris-network/mcs-backup/v2/internal/hooks"
	"github.com/noris-network/mcs-backup/v2/internal/restic"
	"github.com/noris-network/mcs-backup/v2/internal/service"
)

// Errors returned for conditions the caller has to present to the user.
var (
	ErrAppendOnly = errors.New("not possible: the repository is append-only")
	ErrNoSnapshot = errors.New("no matching snapshot")
	ErrConfirm    = errors.New("confirmation missing or wrong")
	ErrPipeMode   = errors.New("not supported in pipe mode")
	ErrNoCopy     = errors.New("no secondary repository configured")

	errTimeout = errors.New("operation timed out")
)

// MinResticVersion is the oldest supported restic release (stdin-from-command).
var MinResticVersion = restic.Version{Major: 0, Minor: 17}

// Options configures an Engine.
type Options struct {
	Config *config.Config
	// Main is the main repository.
	Main *restic.Repo
	// Second is the secondary repository, nil if none is configured.
	Second *restic.Repo
	Hooks  *hooks.Runner
	Logger *slog.Logger
	// Now returns the current time, time.Now if nil.
	Now func() time.Time
	// Rand picks the files of the restore test, randomly seeded if nil.
	Rand *rand.Rand
}

// Engine runs operations. It is safe for concurrent use as long as callers
// serialize operations that modify the repository.
type Engine struct {
	cfg    *config.Config
	main   *restic.Repo
	second *restic.Repo
	hooks  *hooks.Runner
	log    *slog.Logger
	now    func() time.Time
	rnd    *rand.Rand
}

// New creates an Engine.
func New(o Options) *Engine {
	e := &Engine{cfg: o.Config, main: o.Main, second: o.Second, hooks: o.Hooks, log: o.Logger, now: o.Now, rnd: o.Rand}
	if e.now == nil {
		e.now = time.Now
	}
	if e.log == nil {
		e.log = slog.New(slog.DiscardHandler)
	}
	if e.rnd == nil {
		e.rnd = rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), 1))
	}
	if e.hooks == nil {
		e.hooks = &hooks.Runner{Dir: o.Config.Root, Env: o.Config.Environ()}
	}
	return e
}

// HasSecond reports whether a secondary repository is configured.
func (e *Engine) HasSecond() bool { return e.second != nil }

// Progress is the progress of a running backup or restore.
type Progress struct {
	Percent    float64 // 0..1
	FilesDone  int64
	FilesTotal int64
	BytesDone  int64
	BytesTotal int64
	Current    string
}

// Sink receives what happens during an operation. All fields may be nil.
type Sink struct {
	// Phase announces the start of a phase, e.g. "backup", "forget".
	Phase    func(name string)
	Progress func(Progress)
	// Log receives lines for the run log: engine messages, hook output and the
	// output of restic.
	Log func(line string)
}

func (s Sink) phase(name string) {
	if s.Phase != nil {
		s.Phase(name)
	}
}

func (s Sink) progress(p Progress) {
	if s.Progress != nil {
		s.Progress(p)
	}
}

func (s Sink) logf(format string, args ...any) {
	if s.Log != nil {
		s.Log(fmt.Sprintf(format, args...))
	}
}

// writer returns a writer that passes each line to the log.
func (s Sink) writer() io.Writer { return &lineLog{fn: s.Log} }

type lineLog struct {
	fn  func(string)
	buf []byte
}

func (w *lineLog) Write(p []byte) (int, error) {
	if w.fn == nil {
		return len(p), nil
	}
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			return len(p), nil
		}
		w.fn(strings.TrimRight(string(w.buf[:i]), "\r"))
		w.buf = w.buf[i+1:]
	}
}

// PhaseResult is the outcome of a part of an operation.
type PhaseResult struct {
	Result service.Result `json:"result"`
	Error  string         `json:"error,omitempty"`
}

// withTimeout limits ctx to d (if positive); the cause tells a timeout apart
// from a cancellation.
func withTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if d <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeoutCause(ctx, d, errTimeout)
}

// classify maps the error of an operation to a result.
func classify(ctx context.Context, err error) service.Result {
	switch {
	case err == nil:
		return service.ResultSuccess
	case errors.Is(context.Cause(ctx), errTimeout):
		return service.ResultTimeout
	case errors.Is(ctx.Err(), context.Canceled):
		return service.ResultCancelled
	}
	return service.ResultFailure
}

// runHook runs a hook script and logs its output.
func (e *Engine) runHook(ctx context.Context, name, script string, args []string, sink Sink, env ...string) error {
	sink.logf("running %s hook %s", name, script)
	r := *e.hooks
	r.Output = func(_, line string) { sink.logf("%s: %s", name, line) }
	return r.Run(ctx, name, script, args, env...)
}

// retryLocked runs fn and, if the repository is locked, removes stale locks
// (AUTO_UNLOCK_AFTER) and runs it once more.
func (e *Engine) retryLocked(ctx context.Context, repo *restic.Repo, appendOnly bool, sink Sink, fn func() error) error {
	err := fn()
	if !errors.Is(err, restic.ErrLocked) || e.cfg.AutoUnlockAfter <= 0 {
		return err
	}
	if !e.autoUnlock(ctx, repo, appendOnly, sink) {
		return err
	}
	return fn()
}

// autoUnlock removes locks if all of them are older than AUTO_UNLOCK_AFTER.
// restic refreshes the locks of running processes, so a young lock means
// someone is working.
func (e *Engine) autoUnlock(ctx context.Context, repo *restic.Repo, appendOnly bool, sink Sink) bool {
	locks, err := repo.Locks(ctx)
	if err != nil || len(locks) == 0 {
		return false
	}
	now := e.now()
	for _, l := range locks {
		if now.Sub(l.Time) < e.cfg.AutoUnlockAfter {
			sink.logf("repository is locked by %s (pid %d), lock is only %v old: not removing it",
				l.Hostname, l.PID, now.Sub(l.Time).Round(time.Second))
			return false
		}
	}
	sink.logf("repository is locked, all %d locks are older than %v: removing them", len(locks), e.cfg.AutoUnlockAfter)
	// An append-only repository does not allow --remove-all.
	if err := repo.Unlock(ctx, !appendOnly); err != nil {
		sink.logf("unlock failed: %v", err)
		return false
	}
	return true
}

// Snapshots lists the snapshots of the main repository.
func (e *Engine) Snapshots(ctx context.Context) ([]restic.Snapshot, error) {
	return e.main.Snapshots(ctx)
}

// SecondSnapshots lists the snapshots of the secondary repository.
func (e *Engine) SecondSnapshots(ctx context.Context) ([]restic.Snapshot, error) {
	if e.second == nil {
		return nil, ErrNoCopy
	}
	return e.second.Snapshots(ctx)
}

// repoStatsTimeout limits how long gathering the repository size may take.
const repoStatsTimeout = 5 * time.Minute

// RepoSize returns the raw data size of the main repository.
func (e *Engine) RepoSize(ctx context.Context) (restic.Stats, error) {
	ctx, cancel := context.WithTimeout(ctx, repoStatsTimeout)
	defer cancel()
	return e.main.Stats(ctx, restic.StatsRawData)
}

// CheckVersion verifies that the restic binary is recent enough.
func (e *Engine) CheckVersion(ctx context.Context) (restic.Version, error) {
	v, err := e.main.Version(ctx)
	if err != nil {
		return v, err
	}
	if !v.AtLeast(MinResticVersion.Major, MinResticVersion.Minor) {
		return v, fmt.Errorf("restic %s is too old: mcs-backup needs %d.%d or newer", v, MinResticVersion.Major, MinResticVersion.Minor)
	}
	return v, nil
}

// RepoInfo describes an opened repository.
type RepoInfo struct {
	ID      string
	Created bool
}

// OpenMain reads the config of the main repository and creates the repository
// if it does not exist yet. Only the daemon creates repositories.
func (e *Engine) OpenMain(ctx context.Context) (RepoInfo, error) {
	return e.open(ctx, e.main, nil)
}

// OpenSecond does the same for the secondary repository, which is created with
// the chunker parameters of the main one so that copying deduplicates.
func (e *Engine) OpenSecond(ctx context.Context) (RepoInfo, error) {
	if e.second == nil {
		return RepoInfo{}, ErrNoCopy
	}
	return e.open(ctx, e.second, e.main)
}

func (e *Engine) open(ctx context.Context, repo, from *restic.Repo) (RepoInfo, error) {
	c, err := repo.Config(ctx)
	if err == nil {
		return RepoInfo{ID: c.ID}, nil
	}
	if !errors.Is(err, restic.ErrRepoMissing) {
		return RepoInfo{}, err
	}
	e.log.Info("repository does not exist, creating it")
	id, err := repo.Init(ctx, from)
	if err != nil {
		return RepoInfo{}, fmt.Errorf("create repository: %w", err)
	}
	return RepoInfo{ID: id, Created: true}, nil
}
