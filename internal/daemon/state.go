package daemon

import (
	"encoding/json/v2"
	"os"
	"path/filepath"
	"time"

	"github.com/noris-network/mcs-backup/v2/internal/metrics"
	"github.com/noris-network/mcs-backup/v2/internal/restic"
	"github.com/noris-network/mcs-backup/v2/internal/service"
)

// persisted is the small amount of state that survives a restart: a
// maintenance window has an end, so forgetting it would start backups in the
// middle of the maintenance. Everything else is rebuilt from the repository.
type persisted struct {
	MaintenanceUntil time.Time `json:"maintenance_until,omitzero"`
}

func (d *Daemon) stateFile() string {
	return filepath.Join(d.cfg.Server.StateDir, "state.json")
}

// loadState restores a maintenance window that is still open.
func (d *Daemon) loadState() {
	data, err := os.ReadFile(d.stateFile())
	if err != nil {
		if !os.IsNotExist(err) {
			d.log.Warn("cannot read the state file", "error", err)
		}
		return
	}
	var p persisted
	if err := json.Unmarshal(data, &p); err != nil {
		d.log.Warn("ignoring an unreadable state file", "error", err)
		return
	}
	now := d.now()
	if !p.MaintenanceUntil.After(now) {
		return
	}
	d.mu.Lock()
	d.maintUntil = p.MaintenanceUntil
	d.armMaintenanceLocked()
	d.mu.Unlock()
	d.log.Info("maintenance window continues after the restart", "until", p.MaintenanceUntil)
}

// saveState writes the persistent state, or removes the file if there is none.
func (d *Daemon) saveState(p persisted) {
	path := d.stateFile()
	if p.MaintenanceUntil.IsZero() {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			d.log.Warn("cannot remove the state file", "error", err)
		}
		return
	}
	data, err := json.Marshal(p)
	if err == nil {
		err = os.MkdirAll(filepath.Dir(path), 0o700)
	}
	if err == nil {
		tmp := path + ".tmp"
		if err = os.WriteFile(tmp, data, 0o600); err == nil {
			err = os.Rename(tmp, path)
		}
	}
	if err != nil {
		d.log.Warn("cannot save the state, a maintenance window will not survive a restart", "error", err)
	}
}

// latestSuccess returns the time of the newest regular snapshot of this
// instance.
func (d *Daemon) latestSuccess(snaps []restic.Snapshot) time.Time {
	own := service.Own(snaps, service.OwnHosts(d.cfg.Host, snaps))
	if s, ok := service.Latest(service.Regular(own)); ok {
		return s.Time
	}
	return time.Time{}
}

// backupsActiveLocked reports whether scheduled backups may run.
func (d *Daemon) backupsActiveLocked() bool {
	return d.enabled && !d.now().Before(d.maintUntil)
}

// metricsState builds the snapshot the Prometheus collector reads. Only what
// is known is filled in: right after a restart the last success comes from the
// repository, and the result of the last run stays absent until a run happens.
func (d *Daemon) metricsState() metrics.State {
	d.mu.Lock()
	defer d.mu.Unlock()

	s := metrics.State{
		Version: d.o.Version, RepositoryID: d.repo.id, RepositoryUp: d.repo.up,
		CopyConfigured: d.eng.HasSecond(), CopyUp: d.second.up,
		SnapshotsKnown: d.snapsKnown, SizeKnown: d.sizeKnown, SizeBytes: d.size,
		ForgetHeld: d.forgetHeld, LastBackup: d.lastBackup,
		BackupEnabled: d.backupsActiveLocked(), StatsErrors: d.statsErrors,
		Next: map[string]time.Time{}, Running: map[string]bool{}, LastRuns: map[string]metrics.LastRun{},
	}
	if !d.maintUntil.Before(d.now()) {
		s.MaintenanceUntil = d.maintUntil
	}
	if d.snapsKnown {
		own := service.Own(d.snaps, service.OwnHosts(d.cfg.Host, d.snaps))
		s.Snapshots = len(own)
		s.LastSuccess = d.latestSuccess(d.snaps)
		for _, sn := range own {
			if sn.HasTag(service.TagSuspicious) {
				s.Suspicious = true
			}
		}
	}
	s.CopyLastSuccess = d.latestSuccess(d.secondSnaps)
	if n := d.o.Notifier; n != nil {
		s.NotifyErrors = n.Errors()
	}
	if d.o.WebUI != nil {
		w := d.o.WebUI.Status()
		s.WebUIEnabled, s.WebUIUntil = w.Enabled, w.Until
	}

	for _, kind := range []service.Kind{service.KindBackup, service.KindPrune, service.KindCheck, service.KindVerify} {
		if next, ok := d.sched.Next(string(kind)); ok {
			s.Next[string(kind)] = next
		}
	}
	if d.op != nil {
		s.Running[string(d.op.op.Kind)] = true
	}
	for kind, res := range d.lastRuns {
		// a skipped or cancelled run says nothing about whether the job works
		if res.Result == service.ResultSkipped || res.Result == service.ResultCancelled || kind == service.KindTag || kind == service.KindUnlock {
			continue
		}
		s.LastRuns[string(kind)] = metrics.LastRun{Success: res.Result == service.ResultSuccess || res.Result == service.ResultWarning, At: res.Started, Duration: res.Duration}
	}
	return s
}
