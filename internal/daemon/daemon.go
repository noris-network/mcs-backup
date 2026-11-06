// Package daemon is the long-running mcs-backup service. It owns the
// operation slot, runs scheduled and requested operations through the engine,
// keeps the state that metrics and clients report, and implements
// service.Service.
package daemon

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/noris-network/mcs-backup/v2/internal/config"
	"github.com/noris-network/mcs-backup/v2/internal/engine"
	"github.com/noris-network/mcs-backup/v2/internal/metrics"
	"github.com/noris-network/mcs-backup/v2/internal/notify"
	"github.com/noris-network/mcs-backup/v2/internal/redact"
	"github.com/noris-network/mcs-backup/v2/internal/restic"
	"github.com/noris-network/mcs-backup/v2/internal/schedule"
	"github.com/noris-network/mcs-backup/v2/internal/service"
)

// Engine is what the daemon needs from the engine.
type Engine interface {
	CheckVersion(ctx context.Context) (restic.Version, error)
	OpenMain(ctx context.Context) (engine.RepoInfo, error)
	OpenSecond(ctx context.Context) (engine.RepoInfo, error)
	HasSecond() bool

	Snapshots(ctx context.Context) ([]restic.Snapshot, error)
	SecondSnapshots(ctx context.Context) ([]restic.Snapshot, error)
	RepoSize(ctx context.Context) (restic.Stats, error)
	Retention(ctx context.Context) (service.RetentionInfo, error)
	Locks(ctx context.Context) ([]service.Lock, error)
	Files(ctx context.Context, req service.FilesRequest) (service.FileList, error)
	Find(ctx context.Context, req service.FindRequest) (service.FindResult, error)
	RestorePreflight(ctx context.Context, req service.RestoreRequest) (service.RestorePreflight, error)
	VerifyConfigured() bool
	// RestoreNeedsConfirm: the restore replaces the live data.
	RestoreNeedsConfirm(req service.RestoreRequest) bool

	Backup(ctx context.Context, req service.BackupRequest, sink engine.Sink) engine.BackupResult
	Restore(ctx context.Context, req service.RestoreRequest, sink engine.Sink) engine.RestoreResult
	Verify(ctx context.Context, req service.VerifyRequest, sink engine.Sink) engine.VerifyResult
	Prune(ctx context.Context, sink engine.Sink) engine.MaintenanceResult
	Check(ctx context.Context, sink engine.Sink) engine.MaintenanceResult
	ApproveForget(ctx context.Context, sink engine.Sink) engine.ApproveResult

	Keep(ctx context.Context, ref string) (service.TagResult, error)
	Unkeep(ctx context.Context, ref string) (service.TagResult, error)
	Acknowledge(ctx context.Context, ref string) (service.TagResult, error)
	Unlock(ctx context.Context, all bool) error
}

// Notifier delivers events to the operator; *notify.Notifier implements it.
type Notifier interface {
	Start(ctx context.Context)
	Stop(ctx context.Context)
	Notify(ev notify.Event)
	Send(ctx context.Context, ev notify.Event) error
	Enabled() bool
	Errors() int64
	LastError() string
}

// WebUI controls the optional web UI. The daemon only forwards to it.
type WebUI interface {
	Status() service.WebUIStatus
	Enable(addr string, d time.Duration) (service.WebUIStatus, error)
	Disable() (service.WebUIStatus, error)
	LoginLink() (string, error)
}

// Options configures a Daemon.
type Options struct {
	Config *config.Config
	Engine Engine
	// Notifier delivers notifications, nil disables them.
	Notifier Notifier
	// WebUI is nil if the web UI is not part of the build.
	WebUI WebUI
	// Env provides the restic environment for clients, nil if unavailable.
	Env     func(reveal bool) (service.EnvInfo, error)
	Logger  *slog.Logger
	Version string
	// Now returns the current time, time.Now if nil.
	Now func() time.Time
	// HTTPAddr is where /metrics, /healthz and /readyz are served, empty for none.
	HTTPAddr string
	// MinBackoff and MaxBackoff bound the wait between attempts to reach the
	// repository, 10 s and 5 min by default.
	MinBackoff, MaxBackoff time.Duration
	// MonitorInterval is how often an idle service re-reads the repository, 5 min by default.
	MonitorInterval time.Duration
	// DownGrace is how long the repository must be down before the
	// notification is sent, 2 min by default.
	DownGrace time.Duration
}

type repoState struct {
	up    bool
	err   string
	id    string
	since time.Time // when it went down
	// notified: a repository.down notification was sent and no repository.up followed.
	notified bool
}

// Daemon implements service.Service.
type Daemon struct {
	o      Options
	cfg    *config.Config
	eng    Engine
	log    *slog.Logger
	now    func() time.Time
	redact *redact.Redactor
	events *broadcaster
	sched  *schedule.Scheduler
	reg    *prometheus.Registry

	// opsCtx is the parent of all operations; it ends at shutdown, after the
	// operations were asked to stop.
	opsCtx    context.Context
	cancelOps context.CancelFunc
	hardStop  chan struct{}
	stopOnce  sync.Once
	wg        sync.WaitGroup

	mu           sync.Mutex // guards everything below
	started      time.Time
	httpAddr     string
	ready        bool
	shuttingDown bool

	repo, second repoState
	snaps        []restic.Snapshot
	secondSnaps  []restic.Snapshot
	snapsKnown   bool
	sizeKnown    bool
	size         int64
	statsErrors  int64
	staleLocks   int
	forgetHeld   bool
	lastBackup   *metrics.BackupStats

	op       *running
	lastRuns map[service.Kind]service.RunResult
	logs     map[service.Kind]*runLog
	// prevBad: the last final result of a kind was bad, so that the next
	// success is reported as a recovery.
	prevBad map[service.Kind]bool

	enabled    bool
	maintUntil time.Time
	maintTimer *time.Timer
	queued     map[service.Kind]bool
	// backupDue: a scheduled backup fell due while another operation ran.
	backupDue bool
	retry     *retryState
}

// New creates a Daemon. Call Run to start it.
func New(o Options) *Daemon {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.MinBackoff <= 0 {
		o.MinBackoff = 10 * time.Second
	}
	if o.MaxBackoff <= 0 {
		o.MaxBackoff = 5 * time.Minute
	}
	if o.MonitorInterval <= 0 {
		o.MonitorInterval = 5 * time.Minute
	}
	if o.DownGrace <= 0 {
		o.DownGrace = 2 * time.Minute
	}
	d := &Daemon{
		o: o, cfg: o.Config, eng: o.Engine, log: o.Logger, now: o.Now,
		redact:   redact.New(o.Config.Environ()),
		events:   newBroadcaster(),
		hardStop: make(chan struct{}),
		lastRuns: map[service.Kind]service.RunResult{},
		logs:     map[service.Kind]*runLog{},
		prevBad:  map[service.Kind]bool{},
		queued:   map[service.Kind]bool{},
		enabled:  true,
	}
	// operations run on a context of their own; it ends when the service
	// shuts down, after the operations were asked to stop
	d.opsCtx, d.cancelOps = context.WithCancel(context.Background())
	d.sched = schedule.New(schedule.Options{Now: o.Now, Log: o.Logger})
	d.reg = metrics.NewRegistry(d.metricsState, o.Config.MetricsLabels)
	return d
}

// Run starts the service and blocks until ctx ends. It returns an error if
// the service cannot start (unsupported restic, port in use); problems with
// the repository are not fatal, they are retried.
func (d *Daemon) Run(ctx context.Context) error {
	defer d.cancelOps()

	d.mu.Lock()
	d.started = d.now()
	d.mu.Unlock()
	d.loadState()

	v, err := d.eng.CheckVersion(ctx)
	if err != nil {
		return err
	}
	d.log.InfoContext(ctx, "restic found", "version", v.String())
	if err := d.configureSchedules(); err != nil {
		return err
	}

	var srv *http.Server
	if d.o.HTTPAddr != "" {
		var err error
		if srv, err = d.serveHTTP(ctx); err != nil {
			return err
		}
	}
	if n := d.o.Notifier; n != nil {
		// the notifications of the last operations still go out at shutdown,
		// which stops the notifier last
		n.Start(context.WithoutCancel(ctx))
	}

	d.mu.Lock()
	d.ready = true
	d.mu.Unlock()

	d.wg.Go(func() { d.sched.Run(ctx, d.schedulerHandler()) })
	d.wg.Go(func() { d.connectLoop(ctx) })

	<-ctx.Done()
	d.shutdown(srv)
	return nil
}

// Handler returns the HTTP handler for /metrics, /healthz and /readyz.
func (d *Daemon) Handler() http.Handler { return d.handler() }

var errShuttingDown = errors.New("the service is shutting down")
