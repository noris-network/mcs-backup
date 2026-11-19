package daemon

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/noris-network/mcs-backup/v2/internal/notify"
	"github.com/noris-network/mcs-backup/v2/internal/restic"
	"github.com/noris-network/mcs-backup/v2/internal/service"
)

// Status implements service.Service.
func (d *Daemon) Status(context.Context) (service.Status, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.now()

	st := service.Status{
		Version: d.o.Version, Host: d.cfg.Host, StartedAt: d.started, Ready: d.ready && d.snapsKnown,
		Retention: d.cfg.Retention.Describe(), AppendOnly: d.cfg.AppendOnly, PipeMode: d.cfg.PipeMode(), Source: d.cfg.Source(),
		ForgetHeld: d.forgetHeld, VerifyConfigured: d.eng.VerifyConfigured(),
		LastRuns: maps.Clone(d.lastRuns),
		Repository: service.RepoStatus{
			Up: d.repo.up, Connecting: d.repo.connecting(), Error: d.repo.err, ID: d.repo.id, Snapshots: -1, SizeBytes: -1,
		},
		Schedule: service.ScheduleStatus{
			Enabled: d.enabled, Backup: d.cfg.Schedule.Backup, CatchUp: d.cfg.Schedule.CatchUp,
			Next: map[service.Kind]time.Time{},
		},
	}
	if st.Schedule.Backup == "" {
		st.Schedule.Backup = d.cfg.Schedule.BackupFile
	}
	if d.maintUntil.After(now) {
		st.Schedule.MaintenanceUntil = d.maintUntil
	}
	for _, kind := range []service.Kind{service.KindBackup, service.KindPrune, service.KindCheck, service.KindVerify} {
		if next, ok := d.sched.Next(string(kind)); ok {
			st.Schedule.Next[kind] = next
		}
	}
	if d.snapsKnown {
		own := service.Own(d.snaps, service.OwnHosts(d.cfg.Host, d.snaps))
		st.Repository.Snapshots = len(own)
		st.Repository.LastSuccess = d.latestSuccess(d.snaps)
		st.Suspicious = slices.ContainsFunc(own, func(s restic.Snapshot) bool { return s.HasTag(service.TagSuspicious) })
	}
	if d.sizeKnown {
		st.Repository.SizeBytes = d.size
	}
	if d.eng.HasSecond() {
		c := service.RepoStatus{Up: d.second.up, Connecting: d.second.connecting(), Error: d.second.err, ID: d.second.id, Snapshots: -1, SizeBytes: -1}
		if d.secondSnaps != nil {
			c.Snapshots = len(service.Own(d.secondSnaps, service.OwnHosts(d.cfg.Host, d.secondSnaps)))
			c.LastSuccess = d.latestSuccess(d.secondSnaps)
		}
		st.Copy = &c
	}
	if d.op != nil {
		op := d.op.snapshotOp()
		st.Operation = &op
	}
	if d.retry != nil {
		st.Retry = &service.RetryInfo{Attempt: d.retry.attempt - 1, Max: d.cfg.Backup.Retries, At: d.retry.at}
	}
	if d.o.WebUI != nil {
		st.WebUI = d.o.WebUI.Status()
	}
	st.Warnings = d.warningsLocked(st, now)
	return st, nil
}

// warningsLocked lists what an operator should look at.
func (d *Daemon) warningsLocked(st service.Status, now time.Time) []service.Warning {
	var w []service.Warning
	// a warning can quote an error of restic or of the notification command
	add := func(code string, sev service.Severity, format string, args ...any) {
		w = append(w, service.Warning{Code: code, Severity: sev, Message: d.redact.String(fmt.Sprintf(format, args...))})
	}

	if !st.Repository.Up && !st.Repository.Connecting {
		msg := "the repository is not reachable"
		if st.Repository.Error != "" {
			msg += ": " + st.Repository.Error
		}
		add("repository_down", service.SeverityError, "%s", msg)
	}
	if st.Copy != nil && !st.Copy.Up && !st.Copy.Connecting {
		msg := "the secondary repository is not reachable"
		if st.Copy.Error != "" {
			msg += ": " + st.Copy.Error
		}
		add("copy_down", service.SeverityWarning, "%s", msg)
	}
	if d.snapsKnown && st.Repository.LastSuccess.IsZero() {
		add("no_backup", service.SeverityWarning, "there is no backup yet")
	}
	if age := d.backupAgeLocked(st.Repository.LastSuccess, now); age > 0 {
		add("backup_old", service.SeverityWarning, "the last backup is %s old, older than the schedule suggests", age.Round(time.Minute))
	}
	if st.Suspicious {
		add("backup_suspicious", service.SeverityError, "a snapshot is tagged suspicious: check it and acknowledge it, forget is paused until then")
	}
	if st.ForgetHeld {
		add("forget_held", service.SeverityWarning, "forget would delete many snapshots and is held back: approve it if that is intended")
	}
	if d.staleLocks > 0 {
		add("stale_locks", service.SeverityWarning, "%d stale locks in the repository", d.staleLocks)
	}
	switch {
	case st.Schedule.MaintenanceUntil.After(now):
		add("maintenance", service.SeverityInfo, "scheduled backups are paused until %s", st.Schedule.MaintenanceUntil.Local().Format(time.DateTime))
	case !st.Schedule.Enabled:
		add("backups_disabled", service.SeverityWarning, "scheduled backups are disabled until they are enabled again or the service restarts")
	}
	if st.AppendOnly {
		add("append_only", service.SeverityInfo, "the repository is append-only: nothing is forgotten, pruned or retagged")
	}
	if r, ok := st.LastRuns[service.KindBackup]; ok && (r.Result == service.ResultFailure || r.Result == service.ResultTimeout) && !r.WillRetry {
		add("backup_failed", service.SeverityError, "the last backup failed: %s", r.Error)
	}
	if r, ok := st.LastRuns[service.KindBackup]; ok && r.Result == service.ResultWarning {
		// a forget that failed, a file that could not be read: the snapshot
		// is there, but something needs a look before it happens every time
		var ws []string
		for _, w := range r.Warnings {
			if st.ForgetHeld && strings.Contains(w, "is held back") { // forget_held says it
				continue
			}
			ws = append(ws, w)
		}
		if len(ws) > 0 {
			add("backup_warning", service.SeverityWarning, "the last backup has a warning: %s", strings.Join(ws, "; "))
		}
	}
	if r, ok := st.LastRuns[service.KindPrune]; ok && !r.Result.OK() && r.Result != service.ResultCancelled {
		add("prune_failed", service.SeverityWarning, "the last prune failed: %s", r.Error)
	}
	if r, ok := st.LastRuns[service.KindCheck]; ok && !r.Result.OK() && r.Result != service.ResultCancelled {
		add("check_failed", service.SeverityError, "the last check of the repository failed: %s", r.Error)
	}
	if r, ok := st.LastRuns[service.KindCopy]; ok && !r.Result.OK() && r.Result != service.ResultCancelled {
		add("copy_failed", service.SeverityWarning, "the last copy to the secondary repository failed: %s", r.Error)
	}
	if r, ok := st.LastRuns[service.KindVerify]; ok && !r.Result.OK() && r.Result != service.ResultCancelled {
		add("verify_failed", service.SeverityError, "the last restore test failed: %s", r.Error)
	}
	if !st.VerifyConfigured {
		add("verify_not_configured", service.SeverityInfo, "restore test not configured: set VERIFY_SCRIPT to test restores in pipe mode")
	}
	if n := d.o.Notifier; n != nil && n.Errors() > 0 {
		add("notify_failed", service.SeverityWarning, "notifications failed: %s", n.LastError())
	}
	if st.Retry != nil {
		add("backup_retry", service.SeverityInfo, "retry %d/%d at %s", st.Retry.Attempt, st.Retry.Max, st.Retry.At.Format("15:04"))
	}
	return w
}

// backupAgeLocked returns the age of the last backup if it is clearly older
// than the backup schedule suggests, otherwise 0.
func (d *Daemon) backupAgeLocked(last, now time.Time) time.Duration {
	sched, ok := d.sched.Schedule(string(service.KindBackup))
	if !ok || last.IsZero() || !d.backupsActiveLocked() {
		return 0
	}
	first := sched.Next(now)
	if first.IsZero() {
		return 0
	}
	interval := sched.Next(first).Sub(first)
	if age := now.Sub(last); interval > 0 && age > 2*interval+time.Hour {
		return age
	}
	return 0
}

// Snapshots implements service.Service: the snapshots of this instance, newest first.
func (d *Daemon) Snapshots(ctx context.Context) ([]service.Snapshot, error) {
	snaps, err := d.eng.Snapshots(ctx)
	if err != nil {
		return nil, d.redact.Error(err)
	}
	own := service.Own(snaps, service.OwnHosts(d.cfg.Host, snaps))
	out := make([]service.Snapshot, len(own))
	for i, s := range own {
		out[len(own)-1-i] = service.FromRestic(s)
	}
	return out, nil
}

// Retention implements service.Service.
func (d *Daemon) Retention(ctx context.Context) (service.RetentionInfo, error) {
	info, err := d.eng.Retention(ctx)
	return info, d.redact.Error(err)
}

// Locks implements service.Service.
func (d *Daemon) Locks(ctx context.Context) ([]service.Lock, error) {
	locks, err := d.eng.Locks(ctx)
	return locks, d.redact.Error(err)
}

// Files implements service.Service.
func (d *Daemon) Files(ctx context.Context, req service.FilesRequest) (service.FileList, error) {
	list, err := d.eng.Files(ctx, req)
	return list, d.redact.Error(err)
}

// Find implements service.Service.
func (d *Daemon) Find(ctx context.Context, req service.FindRequest) (service.FindResult, error) {
	res, err := d.eng.Find(ctx, req)
	return res, d.redact.Error(err)
}

// Diff implements service.Service.
func (d *Daemon) Diff(ctx context.Context, req service.DiffRequest) (service.DiffResult, error) {
	res, err := d.eng.Diff(ctx, req)
	return res, d.redact.Error(err)
}

// RestorePreflight implements service.Service.
func (d *Daemon) RestorePreflight(ctx context.Context, req service.RestoreRequest) (service.RestorePreflight, error) {
	pre, err := d.eng.RestorePreflight(ctx, req)
	return pre, d.redact.Error(err)
}

// Env implements service.Service.
func (d *Daemon) Env(_ context.Context, reveal bool) (service.EnvInfo, error) {
	if d.o.Env == nil {
		return service.EnvInfo{}, service.ErrUnavailable
	}
	return d.o.Env(reveal)
}

// Keep implements service.Service.
func (d *Daemon) Keep(ctx context.Context, ref string) (service.TagResult, error) {
	return d.tag(ctx, "keep "+ref, func(ctx context.Context) (service.TagResult, error) { return d.eng.Keep(ctx, ref) })
}

// Unkeep implements service.Service.
func (d *Daemon) Unkeep(ctx context.Context, ref string) (service.TagResult, error) {
	return d.tag(ctx, "unkeep "+ref, func(ctx context.Context) (service.TagResult, error) { return d.eng.Unkeep(ctx, ref) })
}

// Acknowledge implements service.Service.
func (d *Daemon) Acknowledge(ctx context.Context, ref, confirm string) (service.TagResult, error) {
	if confirm != service.ConfirmAcknowledge {
		return service.TagResult{}, fmt.Errorf("%w: confirm with %q", service.ErrConfirm, service.ConfirmAcknowledge)
	}
	return d.tag(ctx, "acknowledge "+ref, func(ctx context.Context) (service.TagResult, error) { return d.eng.Acknowledge(ctx, ref) })
}

// tag changes tags while holding the operation slot and refreshes the state afterwards.
func (d *Daemon) tag(ctx context.Context, detail string, fn func(context.Context) (service.TagResult, error)) (service.TagResult, error) {
	var res service.TagResult
	err := d.exclusive(ctx, service.KindTag, detail, func(opCtx context.Context) error {
		var err error
		res, err = fn(opCtx)
		return err
	})
	if err == nil {
		d.refresh(ctx, false)
		d.publishStatusChange()
	}
	return res, err
}

// Unlock implements service.Service.
func (d *Daemon) Unlock(ctx context.Context, all bool, confirm string) error {
	if all {
		if confirm != service.ConfirmRemoveAll {
			return fmt.Errorf("%w: confirm with %q", service.ErrConfirm, service.ConfirmRemoveAll)
		}
		if d.cfg.AppendOnly {
			return service.ErrAppendOnly
		}
	}
	err := d.exclusive(ctx, service.KindUnlock, "", func(opCtx context.Context) error { return d.eng.Unlock(opCtx, all) })
	if err == nil {
		d.refresh(ctx, false)
	}
	return err
}

// NotifyTest implements service.Service.
func (d *Daemon) NotifyTest(ctx context.Context) error {
	n := d.o.Notifier
	if n == nil || !n.Enabled() {
		return fmt.Errorf("%w: NOTIFY_COMMAND is not configured", service.ErrUnavailable)
	}
	d.log.InfoContext(ctx, "sending a test notification", "source", service.SourceFrom(ctx))
	return d.redact.Error(n.Send(ctx, notify.Event{Name: "test", Message: "This is a test notification from mcs-backup.", Time: d.now()}))
}

var errNoWebUI = fmt.Errorf("%w: the web UI is not part of this build", service.ErrUnavailable)

// WebUI implements service.Service.
func (d *Daemon) WebUI(context.Context) (service.WebUIStatus, error) {
	if d.o.WebUI == nil {
		return service.WebUIStatus{}, errNoWebUI
	}
	return d.o.WebUI.Status(), nil
}

// SetWebUI implements service.Service.
func (d *Daemon) SetWebUI(ctx context.Context, req service.WebUIRequest) (service.WebUIStatus, error) {
	if d.o.WebUI == nil {
		return service.WebUIStatus{}, errNoWebUI
	}
	d.log.InfoContext(ctx, "web UI switched", "enable", req.Enable, "addr", req.Addr, "for", req.For.Std(), "source", service.SourceFrom(ctx))
	var (
		st  service.WebUIStatus
		err error
	)
	if req.Enable {
		st, err = d.o.WebUI.Enable(req.Addr, req.For.Std())
	} else {
		st, err = d.o.WebUI.Disable()
	}
	if err == nil {
		d.publishStatusChange()
	}
	return st, err
}

// LoginLink implements service.Service.
func (d *Daemon) LoginLink(ctx context.Context) (string, error) {
	if d.o.WebUI == nil {
		return "", errNoWebUI
	}
	d.log.InfoContext(ctx, "login link requested", "source", service.SourceFrom(ctx))
	return d.o.WebUI.LoginLink()
}

var _ service.Service = (*Daemon)(nil)
