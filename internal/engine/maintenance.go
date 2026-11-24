package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/noris-network/mcs-backup/v2/internal/restic"
	"github.com/noris-network/mcs-backup/v2/internal/service"
)

// staleLockAge is the age at which restic itself treats a lock as stale.
const staleLockAge = 30 * time.Minute

// RepoResult is the outcome of an operation on one repository.
type RepoResult struct {
	Repo   string // "main" or "copy"
	Result service.Result
	Error  string
}

// MaintenanceResult is the outcome of prune or check.
type MaintenanceResult struct {
	Result   service.Result
	Error    string
	Repos    []RepoResult
	Duration time.Duration
}

// Prune removes unreferenced data from the main and the secondary repository.
// Append-only repositories are skipped.
func (e *Engine) Prune(ctx context.Context, sink Sink) MaintenanceResult {
	return e.maintain(ctx, e.cfg.Timeouts.Prune, "prune", sink, func(ctx context.Context, repo *restic.Repo, _ bool) error {
		out := sink.writer()
		defer out.Flush()
		return repo.Prune(ctx, e.cfg.Restic.Prune, out)
	}, true)
}

// Check verifies the structure of the main and the secondary repository. It
// also works on append-only repositories.
func (e *Engine) Check(ctx context.Context, sink Sink) MaintenanceResult {
	return e.maintain(ctx, e.cfg.Timeouts.Check, "check", sink, func(ctx context.Context, repo *restic.Repo, _ bool) error {
		out := sink.writer()
		defer out.Flush()
		return repo.Check(ctx, e.cfg.Restic.Check, out)
	}, false)
}

func (e *Engine) maintain(ctx context.Context, timeout time.Duration, phase string, sink Sink,
	run func(context.Context, *restic.Repo, bool) error, modifies bool) MaintenanceResult {

	start := e.now()
	ctx, cancel := withTimeout(ctx, timeout)
	defer cancel()
	sink.phase(phase)

	type target struct {
		name       string
		repo       *restic.Repo
		appendOnly bool
	}
	targets := []target{{"main", e.main, e.cfg.AppendOnly}}
	if e.second != nil {
		targets = append(targets, target{"copy", e.second, e.cfg.Copy.AppendOnly})
	}

	res := MaintenanceResult{Result: service.ResultSkipped}
	var errs []error
	for _, t := range targets {
		rr := RepoResult{Repo: t.name, Result: service.ResultSkipped}
		switch {
		case modifies && t.appendOnly:
			sink.logf("%s: skipping the %s repository, it is append-only", phase, t.name)
		case ctx.Err() != nil:
			rr.Result = classify(ctx, ctx.Err())
		default:
			sink.logf("%s: %s repository", phase, t.name)
			err := e.retryLocked(ctx, t.repo, t.appendOnly, sink, func() error { return run(ctx, t.repo, t.appendOnly) })
			rr.Result = classify(ctx, err)
			if err != nil {
				rr.Error = err.Error()
				errs = append(errs, fmt.Errorf("%s repository: %w", t.name, err))
			}
		}
		res.Repos = append(res.Repos, rr)
		res.Result = res.Result.Worse(rr.Result)
	}
	if err := errors.Join(errs...); err != nil {
		res.Error = err.Error()
	}
	res.Duration = e.now().Sub(start)
	return res
}

// Locks lists the locks of the main repository.
func (e *Engine) Locks(ctx context.Context) ([]service.Lock, error) {
	locks, err := e.main.Locks(ctx)
	if err != nil {
		return nil, err
	}
	now := e.now()
	host, _ := os.Hostname()
	out := make([]service.Lock, len(locks))
	for i, l := range locks {
		age := now.Sub(l.Time)
		// what restic checks: the lock is old, or its process is gone from this machine
		stale := age > staleLockAge || (host != "" && l.Hostname == host && l.PID > 0 && !e.processAlive(l.PID))
		out[i] = service.Lock{
			ID: l.ID, Time: l.Time, Age: service.Duration(age), Exclusive: l.Exclusive,
			Hostname: l.Hostname, Username: l.Username, PID: l.PID, Stale: stale,
		}
	}
	return out, nil
}

// processAlive reports whether a process runs on this machine, like restic
// checks it for a lock: any error of signal 0 counts as gone.
func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	defer func() { _ = p.Release() }()
	return p.Signal(syscall.Signal(0)) == nil
}

// ReleaseStaleLocks removes the locks that a restic which was stopped may have
// left behind: a restic stopped while it takes its lock can leave the lock in
// the repository, and that lock blocks every forget and prune. restic unlock
// removes stale locks only (of processes that are gone on this machine, or
// older than 30 minutes), never the lock of a restic that runs. A failure is
// only logged.
func (e *Engine) ReleaseStaleLocks(ctx context.Context, sink Sink) {
	repos := []struct {
		name string
		repo *restic.Repo
	}{{"main", e.main}, {"secondary", e.second}}
	for _, r := range repos {
		if r.repo == nil {
			continue
		}
		if err := r.repo.Unlock(ctx, false); err != nil {
			sink.logf("removing stale locks from the %s repository failed: %v", r.name, err)
			continue
		}
		sink.logf("removed the stale locks a stopped restic may have left in the %s repository", r.name)
	}
}

// Unlock removes stale locks, or all locks if all is set.
func (e *Engine) Unlock(ctx context.Context, all bool) error {
	if all && e.cfg.AppendOnly {
		return ErrAppendOnly
	}
	return e.main.Unlock(ctx, all)
}
