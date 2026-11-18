package daemon

import (
	"encoding/json/v2"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/noris-network/mcs-backup/v2/internal/metrics"
	"github.com/noris-network/mcs-backup/v2/internal/restic"
	"github.com/noris-network/mcs-backup/v2/internal/service"
)

// persisted is the small amount of state that survives a restart: a
// maintenance window has an end, so forgetting it would start backups in the
// middle of the maintenance. The repository has the rest, except what the
// last runs said, which runs.json keeps.
type persisted struct {
	MaintenanceUntil time.Time `json:"maintenance_until,omitzero"`
}

// persistedRuns is the result and the log of the last run of each kind: a
// restart, such as a new version being deployed, must not lose what the last
// backup, prune or check said.
type persistedRuns struct {
	LastRuns map[service.Kind]service.RunResult `json:"last_runs"`
	Logs     map[service.Kind]persistedLog      `json:"logs"`
}

type persistedLog struct {
	Start   time.Time `json:"start"`
	Lines   []string  `json:"lines"`
	Dropped int       `json:"dropped,omitzero"`
}

func (d *Daemon) stateFile() string {
	return filepath.Join(d.cfg.Server.StateDir, "state.json")
}

func (d *Daemon) runsFile() string {
	return filepath.Join(d.cfg.Server.StateDir, "runs.json")
}

// loadRuns restores the last runs and their logs.
func (d *Daemon) loadRuns() {
	data, err := os.ReadFile(d.runsFile())
	if err != nil {
		if !os.IsNotExist(err) {
			d.log.Warn("cannot read the last runs", "error", err)
		}
		return
	}
	var p persistedRuns
	if err := json.Unmarshal(data, &p); err != nil {
		d.log.Warn("ignoring an unreadable file of the last runs", "error", err)
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	maps.Copy(d.lastRuns, p.LastRuns)
	for kind, l := range p.Logs {
		d.logs[kind] = &runLog{start: l.Start, lines: l.Lines, dropped: l.Dropped}
	}
}

// saveRuns writes the last runs and their logs; it is called when a run ends.
// The texts are stored as clients see them, with the secrets hidden.
func (d *Daemon) saveRuns() {
	d.mu.Lock()
	p := persistedRuns{LastRuns: maps.Clone(d.lastRuns), Logs: map[service.Kind]persistedLog{}}
	for kind, l := range d.logs {
		if d.op != nil && d.op.op.Kind == kind {
			continue // still being written
		}
		p.Logs[kind] = persistedLog{Start: l.start, Lines: slices.Clone(l.lines), Dropped: l.dropped}
	}
	d.mu.Unlock()
	data, err := json.Marshal(p)
	if err == nil {
		err = writeFile(d.runsFile(), data)
	}
	if err != nil {
		d.log.Warn("cannot save the last runs, a restart will forget them", "error", err)
	}
}

// writeFile replaces a file of the state directory in one step.
func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
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
		err = writeFile(path, data)
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
// repository, and the results of the last runs from runs.json.
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
		s.LastRuns[string(kind)] = metrics.LastRun{Success: res.Result == service.ResultSuccess || res.Result == service.ResultWarning, At: res.Started, Duration: res.Duration.Std()}
	}
	return s
}
