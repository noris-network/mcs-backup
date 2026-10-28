package daemon

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/noris-network/mcs-backup/v2/internal/restic"
	"github.com/noris-network/mcs-backup/v2/internal/schedule"
	"github.com/noris-network/mcs-backup/v2/internal/service"
)

// maxMaintenance is the longest maintenance window that can be requested.
const maxMaintenance = 30 * 24 * time.Hour

// retryState is a failed scheduled backup that waits for its next attempt.
type retryState struct {
	origin service.Origin
	// attempt is the number of the attempt that will run next (2 for the first retry).
	attempt int
	at      time.Time
	timer   *time.Timer
	// due: the delay is over but the slot was busy.
	due bool
}

// configureSchedules installs the cron schedules from the configuration.
func (d *Daemon) configureSchedules() error {
	loc := d.cfg.Schedule.Location
	if d.cfg.Schedule.Backup != "" {
		s, err := schedule.Parse(d.cfg.Schedule.Backup, loc)
		if err != nil {
			return fmt.Errorf("CRON_SCHEDULE: %w", err)
		}
		d.sched.Set(string(service.KindBackup), s)
	}
	if f := d.cfg.Schedule.BackupFile; f != "" {
		d.sched.WatchFile(string(service.KindBackup), f, loc)
	}
	for kind, expr := range map[service.Kind]string{
		service.KindPrune: d.cfg.Schedule.Prune, service.KindCheck: d.cfg.Schedule.Check, service.KindVerify: d.cfg.Schedule.Verify,
	} {
		if expr == "" {
			continue
		}
		s, err := schedule.Parse(expr, loc)
		if err != nil {
			return fmt.Errorf("schedule of %s: %w", kind, err)
		}
		d.sched.Set(string(kind), s)
	}
	return nil
}

func (d *Daemon) schedulerHandler() schedule.Handler {
	return schedule.Handler{
		Due: func(kind string, at time.Time) { d.handleDue(service.Kind(kind), at) },
		Missed: func(kind string, at time.Time) {
			d.log.Info("scheduled run missed", "kind", kind, "scheduled", at)
		},
		Resumed: func(gap time.Duration) {
			d.log.Info("the clock jumped, the host was probably suspended", "gap", gap.Round(time.Second))
			d.catchUp("resume")
		},
	}
}

// handleDue starts a scheduled operation. A backup that is due while another
// operation runs is skipped; the maintenance operations wait for the slot,
// otherwise a long backup would push them out for good.
func (d *Daemon) handleDue(kind service.Kind, scheduled time.Time) {
	ctx := service.WithSource(context.Background(), service.SourceSchedule)
	switch kind {
	case service.KindBackup:
		d.mu.Lock()
		active := d.backupsActiveLocked()
		d.mu.Unlock()
		if !active {
			d.log.Info("scheduled backup skipped: backups are disabled or in maintenance", "scheduled", scheduled)
			return
		}
		if _, err := d.startBackup(ctx, service.BackupRequest{Origin: service.OriginScheduled}, 1); err != nil {
			d.log.Warn("scheduled backup not started", "error", err)
		}
	case service.KindPrune, service.KindCheck, service.KindVerify:
		if kind == service.KindVerify && !d.eng.VerifyConfigured() {
			d.log.Debug("restore test is not configured, skipping")
			return
		}
		d.startMaintenance(ctx, kind)
	}
}

// startMaintenance starts prune, check or verify, or queues it if the slot is busy.
func (d *Daemon) startMaintenance(ctx context.Context, kind service.Kind) {
	var err error
	switch kind {
	case service.KindPrune:
		_, err = d.startPrune(ctx)
	case service.KindCheck:
		_, err = d.startCheck(ctx)
	case service.KindVerify:
		_, err = d.StartVerify(ctx, service.VerifyRequest{})
	}
	if busy, ok := errors.AsType[*service.BusyError](err); ok {
		d.mu.Lock()
		d.queued[kind] = true
		d.mu.Unlock()
		d.log.InfoContext(ctx, "operation queued until the running one is done", "kind", kind, "running", busy.Current.Kind)
	} else if err != nil {
		d.log.WarnContext(ctx, "scheduled operation not started", "kind", kind, "error", err)
	}
}

// drain starts what is waiting for the slot: a retry first, then the queued
// maintenance operations. It is called whenever the slot becomes free.
func (d *Daemon) drain() {
	d.mu.Lock()
	if d.op != nil || d.shuttingDown {
		d.mu.Unlock()
		return
	}
	var retry *retryState
	if d.retry != nil && d.retry.due {
		retry = d.retry
	}
	var next service.Kind
	for _, k := range []service.Kind{service.KindPrune, service.KindCheck, service.KindVerify} {
		if d.queued[k] {
			next = k
			delete(d.queued, k)
			break
		}
	}
	d.mu.Unlock()

	ctx := service.WithSource(context.Background(), service.SourceSchedule)
	switch {
	case retry != nil:
		d.startRetry(ctx)
	case next != "":
		d.startMaintenance(ctx, next)
	}
}

// scheduleRetryLocked arranges the next attempt of a failed scheduled backup.
func (d *Daemon) scheduleRetryLocked(origin service.Origin, attempt int) {
	d.clearRetryLocked()
	rs := &retryState{origin: origin, attempt: attempt, at: d.now().Add(d.cfg.Backup.RetryDelay)}
	rs.timer = time.AfterFunc(d.cfg.Backup.RetryDelay, d.retryFire)
	d.retry = rs
}

func (d *Daemon) clearRetryLocked() {
	if d.retry != nil && d.retry.timer != nil {
		d.retry.timer.Stop()
	}
	d.retry = nil
}

// retryFire is called when the delay before a retry is over.
func (d *Daemon) retryFire() {
	d.mu.Lock()
	if d.retry == nil || d.shuttingDown {
		d.mu.Unlock()
		return
	}
	d.retry.due = true
	busy := d.op != nil
	d.mu.Unlock()
	if !busy {
		d.drain()
	}
}

func (d *Daemon) startRetry(ctx context.Context) {
	d.mu.Lock()
	rs := d.retry
	d.mu.Unlock()
	if rs == nil {
		return
	}
	// the slot may have been taken in the meantime: the retry stays due
	if _, err := d.startBackup(ctx, service.BackupRequest{Origin: rs.origin}, rs.attempt); err != nil {
		d.log.InfoContext(ctx, "retry waits for the running operation", "error", err)
	}
}

// catchUp starts one backup if a scheduled backup was missed, for example
// because the service was not running or the host was suspended. A single
// backup is made however many slots were missed.
func (d *Daemon) catchUp(reason string) {
	if !d.cfg.Schedule.CatchUp {
		return
	}
	sched, ok := d.sched.Schedule(string(service.KindBackup))
	if !ok {
		return
	}
	d.mu.Lock()
	if !d.backupsActiveLocked() || !d.repo.up || !d.snapsKnown || d.op != nil || d.retry != nil || d.shuttingDown {
		d.mu.Unlock()
		return
	}
	// safety snapshots taken before a restore are no backups
	own := service.Own(d.snaps, service.OwnHosts(d.cfg.Host, d.snaps))
	own = slices.DeleteFunc(own, func(s restic.Snapshot) bool { return s.HasTag(service.TagPreRestore) })
	last, has := service.Latest(own)
	missed := schedule.Missed(sched, last.Time, has, d.now())
	d.mu.Unlock()
	if !missed {
		return
	}
	d.log.Info("a scheduled backup was missed, catching up", "reason", reason)
	ctx := service.WithSource(context.Background(), service.SourceSchedule)
	if _, err := d.startBackup(ctx, service.BackupRequest{Origin: service.OriginCatchUp}, 1); err != nil {
		d.log.Warn("catch-up backup not started", "error", err)
	}
}

// armMaintenanceLocked sets the timer that ends the maintenance window.
func (d *Daemon) armMaintenanceLocked() {
	if d.maintTimer != nil {
		d.maintTimer.Stop()
	}
	if d.maintUntil.IsZero() {
		return
	}
	d.maintTimer = time.AfterFunc(max(d.maintUntil.Sub(d.now()), 0), d.endMaintenance)
}

// endMaintenance reopens scheduled backups at the end of a maintenance window.
func (d *Daemon) endMaintenance() {
	d.mu.Lock()
	if d.maintUntil.IsZero() || d.now().Before(d.maintUntil) {
		d.mu.Unlock()
		return
	}
	d.maintUntil = time.Time{}
	d.mu.Unlock()
	d.saveState(persisted{})
	d.log.Info("maintenance window ended")
	d.publishStatusChange()
	d.catchUp("maintenance window ended")
}

// SetSchedule implements service.Service.
func (d *Daemon) SetSchedule(ctx context.Context, req service.ScheduleRequest) (service.Status, error) {
	source := service.SourceFrom(ctx)
	switch req.Action {
	case service.ScheduleEnable, service.ScheduleDisable:
		d.mu.Lock()
		d.enabled = req.Action == service.ScheduleEnable
		// an explicit decision replaces a maintenance window
		d.maintUntil = time.Time{}
		d.armMaintenanceLocked()
		d.mu.Unlock()
		d.saveState(persisted{})
		d.log.InfoContext(ctx, "scheduled backups switched", "action", req.Action, "source", source)
		if req.Action == service.ScheduleEnable {
			d.catchUp("enabled")
		}
	case service.ScheduleMaintenance:
		if req.For.Std() <= 0 || req.For.Std() > maxMaintenance {
			return service.Status{}, fmt.Errorf("a maintenance window must last between 1s and %v", maxMaintenance)
		}
		d.mu.Lock()
		d.maintUntil = d.now().Add(req.For.Std())
		until := d.maintUntil
		d.armMaintenanceLocked()
		d.mu.Unlock()
		d.saveState(persisted{MaintenanceUntil: until})
		d.log.InfoContext(ctx, "maintenance window opened", "until", until, "source", source)
	default:
		return service.Status{}, fmt.Errorf("unknown schedule action %q", req.Action)
	}
	d.publishStatusChange()
	return d.Status(ctx)
}

func (d *Daemon) publishStatusChange() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.publishLocked(service.Event{Type: service.EventStatus})
}
