package daemon

import (
	"cmp"
	"context"
	"errors"
	"iter"
	"net/http"
	"slices"
	"time"
	"uuid"

	"github.com/noris-network/mcs-backup/v2/internal/engine"
	"github.com/noris-network/mcs-backup/v2/internal/service"
)

// progressInterval limits how often progress events are published.
const progressInterval = 250 * time.Millisecond

// running is the operation that holds the operation slot.
type running struct {
	op           service.Operation
	cancel       context.CancelFunc
	done         chan struct{}
	lastProgress time.Time
}

// snapshotOp returns a copy of the operation that does not alias r.
func (r *running) snapshotOp() service.Operation {
	op := r.op
	if r.op.Progress != nil {
		p := *r.op.Progress
		op.Progress = &p
	}
	return op
}

func (d *Daemon) publishLocked(ev service.Event) {
	ev.Time = d.now()
	d.events.publish(ev)
}

// begin takes the operation slot. It fails with a *service.BusyError if
// another operation runs.
func (d *Daemon) begin(ctx context.Context, kind service.Kind, origin service.Origin, detail string, attempt int) (*running, context.Context, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.shuttingDown {
		return nil, nil, errShuttingDown
	}
	if d.op != nil {
		return nil, nil, &service.BusyError{Current: d.op.snapshotOp()}
	}
	opCtx, cancel := context.WithCancel(engine.WithHardStop(d.opsCtx, d.hardStop))
	r := &running{
		op: service.Operation{
			ID: uuid.NewV7().String(), Kind: kind, Source: service.SourceFrom(ctx), Origin: origin,
			Detail: detail, Started: d.now(), Attempt: attempt,
		},
		cancel: cancel,
		done:   make(chan struct{}),
	}
	d.op = r
	d.logs[kind] = &runLog{start: r.op.Started}
	op := r.snapshotOp()
	d.publishLocked(service.Event{Type: service.EventStarted, Operation: &op})
	d.log.InfoContext(ctx, "operation started", "kind", kind, "source", op.Source, "origin", origin, "id", op.ID)
	return r, opCtx, nil
}

// end releases the operation slot and publishes the result.
func (d *Daemon) end(r *running, res service.RunResult) {
	res = d.redactResult(res)
	d.mu.Lock()
	d.op = nil
	d.lastRuns[res.Kind] = res
	delete(d.restored, res.Kind)
	d.publishLocked(service.Event{Type: service.EventFinished, Result: &res})
	d.mu.Unlock()
	d.saveRuns()
	d.log.Info("operation finished", "kind", res.Kind, "result", res.Result, "duration", res.Duration.Std().Round(time.Millisecond),
		"id", res.ID, "error", res.Error)
	r.cancel()
	close(r.done)
}

// record stores the result of a part of an operation that has its own kind,
// such as the copy phase of a backup.
func (d *Daemon) record(res service.RunResult) {
	res = d.redactResult(res)
	d.mu.Lock()
	d.lastRuns[res.Kind] = res
	delete(d.restored, res.Kind)
	d.mu.Unlock()
	d.saveRuns()
}

// redactResult hides the secrets in the texts of a result, which quote the
// output of restic and of the scripts, before clients can see it.
func (d *Daemon) redactResult(res service.RunResult) service.RunResult {
	res.Message, res.Error = d.redact.String(res.Message), d.redact.String(res.Error)
	res.Warnings = slices.Clone(res.Warnings)
	for i, w := range res.Warnings {
		res.Warnings[i] = d.redact.String(w)
	}
	return res
}

// sink connects an engine operation to the log, the events and the status.
func (d *Daemon) sink(r *running) engine.Sink {
	return engine.Sink{
		Phase: func(name string) {
			d.mu.Lock()
			defer d.mu.Unlock()
			r.op.Phase, r.op.Progress = name, nil
			op := r.snapshotOp()
			d.publishLocked(service.Event{Type: service.EventPhase, Operation: &op})
		},
		Progress: func(p service.Progress) {
			d.mu.Lock()
			defer d.mu.Unlock()
			r.op.Progress = &p
			if now := d.now(); now.Sub(r.lastProgress) >= progressInterval || p.Percent >= 1 {
				r.lastProgress = now
				op := r.snapshotOp()
				d.publishLocked(service.Event{Type: service.EventProgress, Operation: &op})
			}
		},
		Log: func(line string) { d.logLine(r.op.Kind, line) },
	}
}

// logLine adds a line to the log of the running kind and publishes it.
func (d *Daemon) logLine(kind service.Kind, line string) {
	line = d.redact.String(line)
	d.mu.Lock()
	defer d.mu.Unlock()
	if l := d.logs[kind]; l != nil {
		l.add(line)
	}
	d.publishLocked(service.Event{Type: service.EventLog, Line: line})
}

// exclusive runs a quick administrative action while holding the operation slot.
func (d *Daemon) exclusive(ctx context.Context, kind service.Kind, detail string, fn func(context.Context) error) error {
	r, opCtx, err := d.begin(ctx, kind, "", detail, 0)
	if err != nil {
		return err
	}
	start := d.now()
	err = d.redact.Error(fn(opCtx))
	res := service.RunResult{ID: r.op.ID, Kind: kind, Result: service.ResultSuccess, Source: r.op.Source, Started: start, Duration: service.Duration(d.now().Sub(start))}
	if err != nil {
		res.Result, res.Error = service.ResultFailure, err.Error()
	}
	d.end(r, res)
	d.drain()
	return err
}

// Operation implements service.Service.
func (d *Daemon) Operation(context.Context) (*service.Operation, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.op == nil {
		return nil, nil
	}
	op := d.op.snapshotOp()
	return &op, nil
}

// Log implements service.Service. The copy phase belongs to the backup log.
func (d *Daemon) Log(_ context.Context, kind service.Kind) (service.Log, error) {
	if kind == service.KindCopy {
		kind = service.KindBackup
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if l := d.logs[kind]; l != nil {
		return l.snapshot(kind), nil
	}
	return service.Log{Kind: kind, Lines: []string{}}, nil
}

// errDropped ends an event stream whose consumer fell too far behind.
var errDropped = errors.New("event stream dropped: the consumer was too slow")

// Events implements service.Service.
func (d *Daemon) Events(ctx context.Context) iter.Seq2[service.Event, error] {
	return func(yield func(service.Event, error) bool) {
		ch, unsubscribe := d.events.subscribe()
		defer unsubscribe()
		if op, _ := d.Operation(ctx); op != nil {
			if !yield(service.Event{Type: service.EventStarted, Time: d.now(), Operation: op}, nil) {
				return
			}
		}
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-ch:
				if !ok {
					yield(service.Event{}, errDropped)
					return
				}
				if !yield(ev, nil) {
					return
				}
			}
		}
	}
}

// Cancel implements service.Service.
func (d *Daemon) Cancel(ctx context.Context) error {
	d.mu.Lock()
	hadRetry := d.retry != nil
	d.clearRetryLocked()
	r := d.op
	d.mu.Unlock()
	if r != nil {
		d.log.InfoContext(ctx, "cancel requested", "source", service.SourceFrom(ctx), "kind", r.op.Kind)
		// the log of the run says why it ends here
		d.logLine(r.op.Kind, "cancel requested by "+cmp.Or(service.SourceFrom(ctx), "an unknown client"))
		r.cancel()
	} else if hadRetry {
		d.log.InfoContext(ctx, "pending retry discarded", "source", service.SourceFrom(ctx))
	}
	return nil
}

// shutdown stops the service within the shutdown budget: a running operation
// is cancelled, gets the budget to finish (and run its post-hook), and is
// then stopped hard. The notifications still queued get what is left of the
// budget, but at least a few seconds.
func (d *Daemon) shutdown(srv *http.Server) {
	begin := time.Now()
	d.mu.Lock()
	d.shuttingDown = true
	d.clearRetryLocked()
	if d.maintTimer != nil {
		d.maintTimer.Stop()
	}
	r := d.op
	d.mu.Unlock()
	d.log.Info("shutting down")

	if r != nil {
		d.logLine(r.op.Kind, "the service is shutting down: the operation is cancelled")
		r.cancel()
		timer := time.NewTimer(d.cfg.Timeouts.Shutdown)
		select {
		case <-r.done:
			timer.Stop()
		case <-timer.C:
			d.log.Warn("shutdown budget used up, stopping hard", "budget", d.cfg.Timeouts.Shutdown)
			d.stopOnce.Do(func() { close(d.hardStop) })
			select {
			case <-r.done:
			case <-time.After(5 * time.Second):
			}
		}
	}
	d.cancelOps()

	// the server goroutine only ends once the server is shut down
	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = srv.Shutdown(ctx)
		cancel()
	}

	done := make(chan struct{})
	go func() { d.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}

	if n := d.o.Notifier; n != nil {
		left := max(d.cfg.Timeouts.Shutdown-time.Since(begin), 3*time.Second)
		ctx, cancel := context.WithTimeout(context.Background(), left)
		n.Stop(ctx)
		cancel()
	}
}
