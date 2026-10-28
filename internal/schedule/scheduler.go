package schedule

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"
)

// Handler receives the scheduler's events. The callbacks run in the
// scheduler's goroutine and must not block.
type Handler struct {
	// Due is called when a schedule fires.
	Due func(kind string, scheduled time.Time)
	// Missed is called for a schedule that fired too late to be run, e.g.
	// because the host was suspended. The scheduler does not run it.
	Missed func(kind string, scheduled time.Time)
	// Resumed is called when the wall clock jumped forward, e.g. after the
	// host was suspended. gap is the size of the jump.
	Resumed func(gap time.Duration)
}

// Options configures a Scheduler. The zero value works.
type Options struct {
	// Now returns the wall clock time, time.Now if nil.
	Now func() time.Time
	// Tick is the longest the scheduler sleeps, 5 s if zero. It bounds how
	// late a suspend or a changed schedule file is noticed.
	Tick time.Duration
	// StaleAfter is how late a schedule may fire and still run, 2 min if zero.
	StaleAfter time.Duration
	// ResumeThreshold is the wall clock jump that counts as a suspend, 1 min if zero.
	ResumeThreshold time.Duration
	Log             *slog.Logger
}

// Scheduler fires named cron schedules ("backup", "prune", ...).
type Scheduler struct {
	o Options

	mu    sync.Mutex
	jobs  map[string]*job
	files map[string]*watch
}

type job struct {
	sched Schedule
	next  time.Time
}

type watch struct {
	path    string
	loc     *time.Location
	modTime time.Time
}

// New creates a Scheduler.
func New(o Options) *Scheduler {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Tick <= 0 {
		o.Tick = 5 * time.Second
	}
	if o.StaleAfter <= 0 {
		o.StaleAfter = 2 * time.Minute
	}
	if o.ResumeThreshold <= 0 {
		o.ResumeThreshold = time.Minute
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	return &Scheduler{o: o, jobs: map[string]*job{}, files: map[string]*watch{}}
}

// Set installs the schedule for kind, replacing an earlier one. A nil
// schedule removes it.
func (s *Scheduler) Set(kind string, sched Schedule) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setLocked(kind, sched)
}

func (s *Scheduler) setLocked(kind string, sched Schedule) {
	if sched == nil {
		delete(s.jobs, kind)
		return
	}
	s.jobs[kind] = &job{sched: sched, next: sched.Next(s.o.Now())}
}

// Schedule returns the schedule installed for kind.
func (s *Scheduler) Schedule(kind string) (Schedule, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[kind]
	if !ok {
		return nil, false
	}
	return j.sched, true
}

// Next returns when kind fires next.
func (s *Scheduler) Next(kind string) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[kind]
	if !ok || j.next.IsZero() {
		return time.Time{}, false
	}
	return j.next, true
}

// WatchFile makes the schedule for kind follow the cron expression in the file
// at path. The file is read now and again whenever its modification time
// changes. An invalid file keeps the previous schedule. A missing file means
// no schedule until it appears.
func (s *Scheduler) WatchFile(kind, path string, loc *time.Location) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w := &watch{path: path, loc: loc}
	s.files[kind] = w
	s.reloadLocked(kind, w)
}

func (s *Scheduler) pollFiles() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for kind, w := range s.files {
		if fi, err := os.Stat(w.path); err == nil && !fi.ModTime().Equal(w.modTime) {
			s.reloadLocked(kind, w)
		}
	}
}

func (s *Scheduler) reloadLocked(kind string, w *watch) {
	fi, err := os.Stat(w.path)
	if err != nil {
		s.o.Log.Debug("schedule file not available", "kind", kind, "path", w.path, "error", err)
		return
	}
	w.modTime = fi.ModTime()
	data, err := os.ReadFile(w.path)
	if err != nil {
		s.o.Log.Warn("cannot read schedule file, keeping the previous schedule", "path", w.path, "error", err)
		return
	}
	sched, err := Parse(strings.TrimSpace(string(data)), w.loc)
	if err != nil {
		s.o.Log.Warn("invalid schedule file, keeping the previous schedule", "path", w.path, "error", err)
		return
	}
	s.setLocked(kind, sched)
	s.o.Log.Info("schedule loaded", "kind", kind, "path", w.path)
}

// Run fires schedules until ctx is done.
func (s *Scheduler) Run(ctx context.Context, h Handler) {
	lastWall, lastMono := s.o.Now().Round(0), time.Now()
	for {
		s.pollFiles()
		timer := time.NewTimer(s.wait())
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}

		now := s.o.Now()
		// A suspended host keeps its monotonic clock but the wall clock moves on.
		gap := now.Round(0).Sub(lastWall) - time.Since(lastMono)
		lastWall, lastMono = now.Round(0), time.Now()
		if gap > s.o.ResumeThreshold && h.Resumed != nil {
			h.Resumed(gap)
		}
		s.fire(now, h)
	}
}

// wait returns how long to sleep until the next schedule or tick.
func (s *Scheduler) wait() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.o.Tick
	now := s.o.Now()
	for _, j := range s.jobs {
		if !j.next.IsZero() {
			d = min(d, max(j.next.Sub(now), 0))
		}
	}
	return d
}

type firing struct {
	kind      string
	scheduled time.Time
	stale     bool
}

func (s *Scheduler) fire(now time.Time, h Handler) {
	var due []firing
	s.mu.Lock()
	for kind, j := range s.jobs {
		if j.next.IsZero() || j.next.After(now) {
			continue
		}
		due = append(due, firing{kind, j.next, now.Sub(j.next) > s.o.StaleAfter})
		j.next = j.sched.Next(now)
	}
	s.mu.Unlock()

	for _, f := range due {
		switch {
		case f.stale:
			s.o.Log.Info("skip missed schedule", "kind", f.kind, "scheduled", f.scheduled)
			if h.Missed != nil {
				h.Missed(f.kind, f.scheduled)
			}
		case h.Due != nil:
			h.Due(f.kind, f.scheduled)
		}
	}
}

// Missed reports whether a scheduled time passed since last, the start time of
// the latest snapshot. Without a snapshot (hasLast false) a backup is always
// missed. It decides whether to catch up on a backup after a start, a suspend
// or a maintenance window.
func Missed(sched Schedule, last time.Time, hasLast bool, now time.Time) bool {
	if !hasLast {
		return true
	}
	next := sched.Next(last)
	return !next.IsZero() && !next.After(now)
}
