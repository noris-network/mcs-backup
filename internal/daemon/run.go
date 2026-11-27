package daemon

import (
	"context"
	"fmt"
	"time"

	"github.com/noris-network/mcs-backup/v2/internal/engine"
	"github.com/noris-network/mcs-backup/v2/internal/metrics"
	"github.com/noris-network/mcs-backup/v2/internal/notify"
	"github.com/noris-network/mcs-backup/v2/internal/present"
	"github.com/noris-network/mcs-backup/v2/internal/service"
)

// afterTimeout bounds the bookkeeping after an operation (reading the
// repository again), which must not hold the slot for long.
const afterTimeout = 10 * time.Minute

// notify passes ev to the notifier with the secrets in its texts hidden.
func (d *Daemon) notify(ev notify.Event) {
	if n := d.o.Notifier; n != nil {
		ev.Message, ev.Error = d.redact.String(ev.Message), d.redact.String(ev.Error)
		n.Notify(ev)
	}
}

// outcome is what an operation reports to the generic runner.
type outcome struct {
	result   service.Result
	message  string
	err      string
	snapshot string
	warnings []string
	// refresh: read the snapshots and the size of the repository afterwards.
	refresh bool
}

// launch runs an operation in the background.
func (d *Daemon) launch(ctx context.Context, kind service.Kind, detail string, run func(context.Context, engine.Sink) outcome) (service.Operation, error) {
	r, opCtx, err := d.begin(ctx, kind, "", detail, 0)
	if err != nil {
		return service.Operation{}, err
	}
	d.mu.Lock()
	op := r.snapshotOp()
	d.mu.Unlock()
	d.wg.Go(func() {
		out := run(opCtx, d.sink(r))
		d.releaseLocks(r, out.result)
		res := service.RunResult{
			ID: r.op.ID, Kind: kind, Result: out.result, Message: out.message, Error: out.err, Snapshot: out.snapshot,
			Warnings: out.warnings, Source: r.op.Source, Started: r.op.Started, Duration: service.Duration(d.now().Sub(r.op.Started)),
		}
		if out.refresh {
			d.sink(r).Phase("refresh") // the operation is done, its panel must not say otherwise
			ctx, cancel := context.WithTimeout(d.opsCtx, afterTimeout)
			d.refresh(ctx, true)
			cancel()
		}
		d.end(r, res)
		d.notifyRun(res)
		d.drain()
	})
	return op, nil
}

// StartBackup implements service.Service.
func (d *Daemon) StartBackup(ctx context.Context, req service.BackupRequest) (service.Operation, error) {
	return d.startBackup(ctx, req, 1)
}

func (d *Daemon) startBackup(ctx context.Context, req service.BackupRequest, attempt int) (service.Operation, error) {
	if req.Origin == "" {
		req.Origin = service.OriginManual
	}
	r, opCtx, err := d.begin(ctx, service.KindBackup, req.Origin, req.Note, attempt)
	if err != nil {
		return service.Operation{}, err
	}
	d.mu.Lock()
	if attempt > 1 {
		d.retry = nil // consumed
	}
	d.backupDue = false // any backup is the one that was due
	op := r.snapshotOp()
	d.mu.Unlock()
	d.wg.Go(func() {
		res := d.eng.Backup(opCtx, req, d.sink(r))
		d.releaseLocks(r, res.Result)
		d.finishBackup(r, req, attempt, res)
	})
	return op, nil
}

// releaseLocks removes the locks that restic may have left when the operation
// was cancelled or ran out of time: restic stopped while it took its lock
// leaves the lock behind, and every later forget and prune fails on it.
func (d *Daemon) releaseLocks(r *running, result service.Result) {
	if result != service.ResultCancelled && result != service.ResultTimeout {
		return
	}
	ctx, cancel := context.WithTimeout(engine.WithHardStop(d.opsCtx, d.hardStop), time.Minute)
	defer cancel()
	d.eng.ReleaseStaleLocks(ctx, d.sink(r))
}

func (d *Daemon) finishBackup(r *running, req service.BackupRequest, attempt int, res engine.BackupResult) {
	run := service.RunResult{
		ID: r.op.ID, Kind: service.KindBackup, Result: res.Result, Message: res.Message, Error: res.Error,
		Snapshot: res.Snapshot, Warnings: res.Warnings, Source: r.op.Source, Started: r.op.Started,
		Duration: service.Duration(res.Duration), Attempt: attempt,
	}

	// bring the state up to date; statistics are never worth a failure
	d.sink(r).Phase("refresh")
	ctx, cancel := context.WithTimeout(d.opsCtx, afterTimeout)
	d.refresh(ctx, false)
	if res.Snapshot != "" || res.Forget.Ran {
		if err := d.refreshSize(ctx); err != nil {
			run.Warnings = append(run.Warnings, "repository statistics could not be gathered: "+err.Error())
			if run.Result == service.ResultSuccess {
				run.Result = service.ResultWarning
			}
		}
	}
	cancel()

	d.mu.Lock()
	if s := res.Summary; s.SnapshotID != "" {
		d.lastBackup = &metrics.BackupStats{
			FilesNew: s.FilesNew, FilesChanged: s.FilesChanged, FilesUnmodified: s.FilesUnmodified,
			BytesProcessed: s.TotalBytesProcessed, BytesAdded: s.DataAdded,
		}
	}
	wasHeld := d.forgetHeld
	switch {
	case res.Forget.Held || res.CopyForget.Held:
		d.forgetHeld = true
	case res.Forget.Ran || res.CopyForget.Ran:
		d.forgetHeld = false
	case worked(res.Forget, d.cfg.AppendOnly) && (!d.eng.HasSecond() || worked(res.CopyForget, d.cfg.Copy.AppendOnly)):
		// the retention was worked out everywhere and has nothing to remove:
		// what was held back before is no longer due, for example after
		// snapshots were marked to be kept
		d.forgetHeld = false
	}
	newlyHeld := d.forgetHeld && !wasHeld
	if d.cfg.AppendOnly {
		switch {
		case res.Suspicious && res.Snapshot != "":
			d.untaggedSuspicious = res.Snapshot
		case res.Result == service.ResultSuccess || res.Result == service.ResultWarning:
			d.untaggedSuspicious = ""
		}
	}

	// A scheduled backup that failed is tried again; any good backup ends the retries.
	scheduled := req.Origin == service.OriginScheduled || req.Origin == service.OriginCatchUp
	switch {
	case scheduled && run.Result == service.ResultFailure && attempt <= d.cfg.Backup.Retries && !d.shuttingDown:
		d.scheduleRetryLocked(req.Origin, attempt+1)
		run.WillRetry = true
	case run.Result.OK():
		d.clearRetryLocked()
	}
	d.mu.Unlock()

	if res.Copy != nil {
		cr := service.RunResult{
			ID: r.op.ID, Kind: service.KindCopy, Result: res.Copy.Result, Error: res.Copy.Error, Snapshot: res.Snapshot,
			Source: r.op.Source, Started: r.op.Started, Duration: service.Duration(res.Duration),
		}
		d.record(cr)
		d.notifyRun(cr)
	}
	d.end(r, run)
	d.notifyRun(run)
	if newlyHeld {
		d.notify(notify.Event{Name: "retention.held", Kind: string(service.KindBackup), Result: string(service.ResultWarning),
			Message: fmt.Sprintf("forget would remove %d of %d snapshots and is held back: approve it if that is intended", res.Forget.Removed, res.Forget.Total)})
	}
	d.drain()
}

// worked reports whether the retention of a repository was worked out in a
// backup, which it is not in an append-only one, after a suspicious backup or
// when the step failed.
func worked(o engine.ForgetOutcome, appendOnly bool) bool {
	return !appendOnly && o.Total > 0 && o.Error == ""
}

// notifyRun sends the notification for a final result. A run that will be
// retried is not reported: only the last attempt counts. A success after a bad
// result is reported as a recovery.
func (d *Daemon) notifyRun(res service.RunResult) {
	if res.WillRetry {
		return
	}
	name := string(res.Kind)
	d.mu.Lock()
	bad := d.prevBad[res.Kind]
	switch res.Result {
	case service.ResultSuccess:
		name += ".succeeded"
		if bad {
			name = string(res.Kind) + ".recovered"
		}
		d.prevBad[res.Kind] = false
	case service.ResultWarning:
		name += ".warning"
	case service.ResultFailure, service.ResultTimeout:
		name += ".failed"
		d.prevBad[res.Kind] = true
	case service.ResultSuspicious:
		name = "backup.suspicious"
		d.prevBad[res.Kind] = true
	default: // skipped and cancelled say nothing
		d.mu.Unlock()
		return
	}
	d.mu.Unlock()
	d.notify(notify.Event{
		Name: name, Kind: string(res.Kind), Result: string(res.Result), Message: res.Message,
		Error: res.Error, Snapshot: res.Snapshot,
	})
}

// StartRestore implements service.Service.
func (d *Daemon) StartRestore(ctx context.Context, req service.RestoreRequest) (service.Operation, error) {
	if d.eng.RestoreNeedsConfirm(req) && (req.Confirm == "" || req.Confirm != req.Snapshot) {
		return service.Operation{}, fmt.Errorf("%w: restoring over the live data needs confirm equal to the snapshot %q", service.ErrConfirm, req.Snapshot)
	}
	return d.launch(ctx, service.KindRestore, req.Snapshot, func(ctx context.Context, sink engine.Sink) outcome {
		res := d.eng.Restore(ctx, req, sink)
		return outcome{result: res.Result, message: res.Message, err: res.Error, snapshot: res.Snapshot, warnings: res.Warnings, refresh: true}
	})
}

// StartVerify implements service.Service.
func (d *Daemon) StartVerify(ctx context.Context, req service.VerifyRequest) (service.Operation, error) {
	if !d.eng.VerifyConfigured() {
		return service.Operation{}, fmt.Errorf("%w: the restore test is not configured (set VERIFY_SCRIPT for pipe mode)", service.ErrUnavailable)
	}
	return d.launch(ctx, service.KindVerify, req.Mode, func(ctx context.Context, sink engine.Sink) outcome {
		res := d.eng.Verify(ctx, req, sink)
		return outcome{result: res.Result, message: res.Message, err: res.Error}
	})
}

// StartPrune implements service.Service.
func (d *Daemon) StartPrune(ctx context.Context) (service.Operation, error) {
	if d.cfg.AppendOnly && (d.cfg.Copy == nil || d.cfg.Copy.AppendOnly) {
		return service.Operation{}, fmt.Errorf("%w: prune removes data", service.ErrAppendOnly)
	}
	return d.startPrune(ctx)
}

// StartCheck implements service.Service.
func (d *Daemon) StartCheck(ctx context.Context) (service.Operation, error) {
	return d.startCheck(ctx)
}

func (d *Daemon) startPrune(ctx context.Context) (service.Operation, error) {
	return d.launch(ctx, service.KindPrune, "", func(ctx context.Context, sink engine.Sink) outcome {
		res := d.eng.Prune(ctx, sink)
		return outcome{result: res.Result, message: maintenanceMessage("prune", res), err: res.Error, refresh: res.Result != service.ResultSkipped}
	})
}

func (d *Daemon) startCheck(ctx context.Context) (service.Operation, error) {
	return d.launch(ctx, service.KindCheck, "", func(ctx context.Context, sink engine.Sink) outcome {
		res := d.eng.Check(ctx, sink)
		return outcome{result: res.Result, message: maintenanceMessage("check", res), err: res.Error}
	})
}

func maintenanceMessage(what string, res engine.MaintenanceResult) string {
	switch res.Result {
	case service.ResultSkipped:
		return what + " skipped: the repositories are append-only"
	case service.ResultSuccess:
		return what + " finished"
	}
	return what + " did not finish cleanly"
}

// ApproveForget implements service.Service.
func (d *Daemon) ApproveForget(ctx context.Context, confirm string) (service.Operation, error) {
	if confirm != service.ConfirmForget {
		return service.Operation{}, fmt.Errorf("%w: confirm with %q", service.ErrConfirm, service.ConfirmForget)
	}
	if d.cfg.AppendOnly {
		return service.Operation{}, service.ErrAppendOnly
	}
	return d.launch(ctx, service.KindForget, "", func(ctx context.Context, sink engine.Sink) outcome {
		res := d.eng.ApproveForget(ctx, sink)
		if res.Result.OK() {
			d.mu.Lock()
			d.forgetHeld = false
			d.mu.Unlock()
		}
		msg := "forget removed " + present.Count(res.Main.Removed, "snapshot")
		return outcome{result: res.Result, message: msg, err: res.Error, refresh: true}
	})
}
