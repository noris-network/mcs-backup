package daemon

import (
	"context"
	"time"

	"github.com/noris-network/mcs-backup/v2/internal/notify"
)

// connectLoop opens the repositories, with growing pauses while they are
// unreachable, and then keeps watching them. The service stays up when the
// repository is down: schedules keep running and fail with a clear message.
func (d *Daemon) connectLoop(ctx context.Context) {
	backoff := d.o.MinBackoff
	for {
		err := d.connect(ctx)
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			return
		}
		d.log.WarnContext(ctx, "repository not reachable, retrying", "error", err, "in", backoff)
		if !sleep(ctx, backoff) {
			return
		}
		backoff = min(backoff*2, d.o.MaxBackoff)
	}
	d.afterConnect(ctx)
	d.monitor(ctx)
}

// sleep waits for d or until ctx ends; it reports whether the time passed.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// connect opens the main repository (creating it if it does not exist) and
// the secondary one. A secondary repository that is down does not hold the
// service back.
func (d *Daemon) connect(ctx context.Context) error {
	info, err := d.eng.OpenMain(ctx)
	if err != nil {
		d.markDown(false, err)
		return err
	}
	if info.Created {
		d.log.InfoContext(ctx, "repository created", "id", info.ID)
	}
	d.markUp(false, info.ID)

	if d.eng.HasSecond() {
		d.connectSecond(ctx)
	}
	return nil
}

func (d *Daemon) connectSecond(ctx context.Context) {
	info, err := d.eng.OpenSecond(ctx)
	if err != nil {
		d.markDown(true, err)
		return
	}
	if info.Created {
		d.log.InfoContext(ctx, "secondary repository created", "id", info.ID)
	}
	d.markUp(true, info.ID)
}

// afterConnect reads the repository so that the metrics are filled right after
// the start, and then checks whether a backup has to be caught up.
func (d *Daemon) afterConnect(ctx context.Context) {
	d.refresh(ctx, true)
	d.catchUp("start")
}

// monitor re-reads the repository from time to time while the service is idle,
// notices outages and reconnects.
func (d *Daemon) monitor(ctx context.Context) {
	backoff := d.o.MinBackoff
	for {
		wait := d.o.MonitorInterval
		d.mu.Lock()
		down := !d.repo.up
		d.mu.Unlock()
		if down {
			wait = backoff
		}
		if !sleep(ctx, wait) {
			return
		}
		d.mu.Lock()
		busy := d.op != nil
		down = !d.repo.up
		secondDown := d.eng.HasSecond() && !d.second.up
		d.mu.Unlock()

		switch {
		case down:
			if err := d.connect(ctx); err != nil {
				backoff = min(backoff*2, d.o.MaxBackoff)
				continue
			}
			backoff = d.o.MinBackoff
			d.refresh(ctx, true)
			d.catchUp("repository reachable again")
		case busy:
			// the operation updates the state itself
		default:
			if secondDown {
				d.connectSecond(ctx)
			}
			d.refresh(ctx, false)
		}
	}
}

// refresh reads the snapshots (and, with withSize, the repository size) into
// the state. It marks the repository down if it cannot be read.
func (d *Daemon) refresh(ctx context.Context, withSize bool) {
	snaps, err := d.eng.Snapshots(ctx)
	if err != nil {
		if ctx.Err() == nil {
			d.markDown(false, err)
		}
		return
	}
	d.mu.Lock()
	d.snaps, d.snapsKnown = snaps, true
	d.mu.Unlock()
	d.markUp(false, "")

	if d.eng.HasSecond() {
		d.mu.Lock()
		secondUp := d.second.up
		d.mu.Unlock()
		if secondUp {
			if ss, err := d.eng.SecondSnapshots(ctx); err != nil {
				d.markDown(true, err)
			} else {
				d.mu.Lock()
				d.secondSnaps = ss
				d.mu.Unlock()
			}
		}
	}

	if withSize {
		_ = d.refreshSize(ctx) // counted and logged there
	}
	if locks, err := d.eng.Locks(ctx); err == nil {
		stale := 0
		for _, l := range locks {
			if l.Stale {
				stale++
			}
		}
		d.mu.Lock()
		d.staleLocks = stale
		d.mu.Unlock()
	}
}

// refreshSize reads the raw data size. A failure is counted and logged, it
// never turns an operation into a failure.
func (d *Daemon) refreshSize(ctx context.Context) error {
	st, err := d.eng.RepoSize(ctx)
	d.mu.Lock()
	defer d.mu.Unlock()
	if err != nil {
		d.statsErrors++
		d.log.WarnContext(ctx, "cannot read the repository size", "error", err)
		return err
	}
	d.size, d.sizeKnown = st.TotalSize, true
	return nil
}

// markDown records that a repository is unreachable. The notification is sent
// only if it stays down for DownGrace, so that a slow start of the storage or
// a brief network outage does not alarm anybody.
func (d *Daemon) markDown(second bool, err error) {
	now := d.now()
	d.mu.Lock()
	st := &d.repo
	name := "main"
	if second {
		st, name = &d.second, "copy"
	}
	wasUp := st.up
	st.up, st.err = false, d.redact.String(err.Error())
	if wasUp || st.since.IsZero() {
		st.since = now
	}
	send := !st.notified && now.Sub(st.since) >= d.o.DownGrace
	if send {
		st.notified = true
	}
	d.mu.Unlock()

	if wasUp {
		d.log.Warn("repository went down", "repository", name, "error", err)
	}
	if send {
		d.notify(notify.Event{Name: "repository.down", Repository: name, Message: "the " + name + " repository is not reachable", Error: err.Error()})
	}
}

// markUp records that a repository is reachable.
func (d *Daemon) markUp(second bool, id string) {
	d.mu.Lock()
	st := &d.repo
	name := "main"
	if second {
		st, name = &d.second, "copy"
	}
	wasDown := !st.up
	notified := st.notified
	st.up, st.err, st.since, st.notified = true, "", time.Time{}, false
	if id != "" {
		st.id = id
	}
	d.mu.Unlock()

	if wasDown {
		d.log.Info("repository reachable", "repository", name)
	}
	if notified {
		d.notify(notify.Event{Name: "repository.up", Repository: name, Message: "the " + name + " repository is reachable again"})
	}
}
