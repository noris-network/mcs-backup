// Package metrics exposes the state of mcs-backup to Prometheus.
//
// The collector reads a State snapshot when it is scraped and emits only what
// is known. A series that has no value yet (for example the result of a
// backup run before the first run after a restart) is absent instead of zero,
// so that alerts on "== 0" cannot fire by mistake.
package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

const namespace = "mcsbackup"

// State is everything the metrics are made of.
type State struct {
	Version      string
	RepositoryID string

	RepositoryUp   bool
	CopyConfigured bool
	CopyUp         bool

	// SnapshotsKnown: the repository was read at least once.
	SnapshotsKnown  bool
	Snapshots       int
	LastSuccess     time.Time // zero: no regular snapshot
	CopyLastSuccess time.Time
	SizeKnown       bool
	SizeBytes       int64
	Suspicious      bool
	ForgetHeld      bool

	BackupEnabled    bool
	MaintenanceUntil time.Time
	// Next holds when each scheduled kind runs next.
	Next map[string]time.Time
	// Running says for each operation whether it is running.
	Running map[string]bool

	// LastRuns holds the latest result of each kind since the service started.
	LastRuns map[string]LastRun
	// LastBackup is the statistics of the latest backup, nil before the first.
	LastBackup *BackupStats

	StatsErrors  int64
	NotifyErrors int64

	WebUIEnabled bool
	WebUIUntil   time.Time
}

// LastRun is the outcome of the latest run of one kind.
type LastRun struct {
	Success  bool
	At       time.Time
	Duration time.Duration
}

// BackupStats is the statistics of one backup run.
type BackupStats struct {
	FilesNew, FilesChanged, FilesUnmodified int64
	BytesProcessed, BytesAdded              int64
}

// Operations lists the operations operation_running reports on.
func Operations() []string {
	return []string{"backup", "restore", "prune", "check", "verify", "forget"}
}

// Collector turns a State into Prometheus metrics.
type Collector struct {
	source func() State
	d      descs
}

type descs struct {
	info, repoUp, copyUp, lastSuccess, copyLastSuccess, snapshots, size,
	suspicious, forgetHeld, enabled, maintenance, next, running,
	lastRunSuccess, lastRunTime, lastRunDuration, files, bytes,
	statsErrors, notifyErrors, webEnabled, webUntil *prometheus.Desc
}

func desc(name, help string, labels ...string) *prometheus.Desc {
	return prometheus.NewDesc(prometheus.BuildFQName(namespace, "", name), help, labels, nil)
}

// NewCollector creates a collector that reads source on every scrape.
func NewCollector(source func() State) *Collector {
	return &Collector{source: source, d: descs{
		info:            desc("info", "Version and repository of this instance.", "version", "repository_id"),
		repoUp:          desc("repository_up", "1 if the repository is reachable."),
		copyUp:          desc("copy_repository_up", "1 if the secondary repository is reachable."),
		lastSuccess:     desc("last_success_timestamp_seconds", "Time of the newest regular snapshot."),
		copyLastSuccess: desc("copy_last_success_timestamp_seconds", "Time of the newest regular snapshot in the secondary repository."),
		snapshots:       desc("snapshots", "Number of snapshots of this instance."),
		size:            desc("repository_size_bytes", "Raw data size of the repository."),
		suspicious:      desc("backup_suspicious", "1 if a snapshot is tagged suspicious and not yet acknowledged."),
		forgetHeld:      desc("forget_held", "1 if forget is held back by the retention guard."),
		enabled:         desc("backup_enabled", "1 if scheduled backups are enabled."),
		maintenance:     desc("maintenance_until_timestamp_seconds", "End of the maintenance window, 0 if there is none."),
		next:            desc("next_run_timestamp_seconds", "When a scheduled operation runs next.", "kind"),
		running:         desc("operation_running", "1 while the operation runs.", "operation"),
		lastRunSuccess:  desc("last_run_success", "1 if the latest run since start succeeded.", "kind"),
		lastRunTime:     desc("last_run_timestamp_seconds", "When the latest run since start began.", "kind"),
		lastRunDuration: desc("last_run_duration_seconds", "Duration of the latest run since start.", "kind"),
		files:           desc("backup_files", "Files of the latest backup by state.", "state"),
		bytes:           desc("backup_bytes", "Bytes of the latest backup by type.", "type"),
		statsErrors:     desc("stats_errors_total", "Failed attempts to gather repository statistics."),
		notifyErrors:    desc("notify_errors_total", "Failed notifications."),
		webEnabled:      desc("webui_enabled", "1 if the web UI is on."),
		webUntil:        desc("webui_until_timestamp_seconds", "When the web UI switches itself off, 0 if it stays on."),
	}}
}

// Describe implements prometheus.Collector.
func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	d := c.d
	for _, x := range []*prometheus.Desc{
		d.info, d.repoUp, d.copyUp, d.lastSuccess, d.copyLastSuccess, d.snapshots, d.size, d.suspicious,
		d.forgetHeld, d.enabled, d.maintenance, d.next, d.running, d.lastRunSuccess, d.lastRunTime,
		d.lastRunDuration, d.files, d.bytes, d.statsErrors, d.notifyErrors, d.webEnabled, d.webUntil,
	} {
		ch <- x
	}
}

func flag(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func unix(t time.Time) float64 {
	if t.IsZero() {
		return 0
	}
	return float64(t.UnixNano()) / 1e9
}

// Collect implements prometheus.Collector.
func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	s, d := c.source(), c.d
	gauge := func(desc *prometheus.Desc, v float64, labels ...string) {
		ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, v, labels...)
	}

	gauge(d.info, 1, s.Version, s.RepositoryID)
	gauge(d.repoUp, flag(s.RepositoryUp))
	if s.CopyConfigured {
		gauge(d.copyUp, flag(s.CopyUp))
		if !s.CopyLastSuccess.IsZero() {
			gauge(d.copyLastSuccess, unix(s.CopyLastSuccess))
		}
	}
	if !s.LastSuccess.IsZero() {
		gauge(d.lastSuccess, unix(s.LastSuccess))
	}
	if s.SnapshotsKnown {
		gauge(d.snapshots, float64(s.Snapshots))
		gauge(d.suspicious, flag(s.Suspicious))
		gauge(d.forgetHeld, flag(s.ForgetHeld))
	}
	if s.SizeKnown {
		gauge(d.size, float64(s.SizeBytes))
	}

	gauge(d.enabled, flag(s.BackupEnabled))
	gauge(d.maintenance, unix(s.MaintenanceUntil))
	for kind, t := range s.Next {
		if !t.IsZero() {
			gauge(d.next, unix(t), kind)
		}
	}
	for _, op := range Operations() {
		gauge(d.running, flag(s.Running[op]), op)
	}

	for kind, r := range s.LastRuns {
		gauge(d.lastRunSuccess, flag(r.Success), kind)
		gauge(d.lastRunTime, unix(r.At), kind)
		gauge(d.lastRunDuration, r.Duration.Seconds(), kind)
	}
	if b := s.LastBackup; b != nil {
		gauge(d.files, float64(b.FilesNew), "new")
		gauge(d.files, float64(b.FilesChanged), "changed")
		gauge(d.files, float64(b.FilesUnmodified), "unmodified")
		gauge(d.bytes, float64(b.BytesProcessed), "processed")
		gauge(d.bytes, float64(b.BytesAdded), "added")
	}

	ch <- prometheus.MustNewConstMetric(d.statsErrors, prometheus.CounterValue, float64(s.StatsErrors))
	ch <- prometheus.MustNewConstMetric(d.notifyErrors, prometheus.CounterValue, float64(s.NotifyErrors))
	gauge(d.webEnabled, flag(s.WebUIEnabled))
	gauge(d.webUntil, unix(s.WebUIUntil))
}

// NewRegistry returns a registry with the collector, the Go runtime and
// process metrics. labels are added to every series.
func NewRegistry(source func() State, labels map[string]string) *prometheus.Registry {
	reg := prometheus.NewRegistry()
	wrapped := prometheus.WrapRegistererWith(prometheus.Labels(labels), reg)
	wrapped.MustRegister(
		NewCollector(source),
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return reg
}
