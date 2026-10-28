package schedule

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func mustParse(t *testing.T, expr string) Schedule {
	t.Helper()
	s, err := Parse(expr, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// hourStart returns the full hour the test clock starts in. The synctest clock
// starts at midnight local time, so tests derive their expectations from it.
func hourStart() time.Time { return time.Now().UTC().Truncate(time.Hour) }

// at formats the time d after base like the recorder does.
func at(base time.Time, d time.Duration) string { return base.Add(d).Format("15:04") }

type recorder struct {
	mu      sync.Mutex
	due     []string
	missed  []string
	resumed []time.Duration
}

func (r *recorder) handler() Handler {
	return Handler{
		Due: func(kind string, at time.Time) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.due = append(r.due, kind+"@"+at.UTC().Format("15:04"))
		},
		Missed: func(kind string, at time.Time) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.missed = append(r.missed, kind+"@"+at.UTC().Format("15:04"))
		},
		Resumed: func(gap time.Duration) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.resumed = append(r.resumed, gap)
		},
	}
}

func TestRunFiresSchedules(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		base := hourStart()
		s := New(Options{})
		s.Set("backup", mustParse(t, "*/10 * * * *"))
		s.Set("prune", mustParse(t, "45 * * * *"))
		var rec recorder
		ctx, cancel := context.WithCancel(t.Context())
		go s.Run(ctx, rec.handler())

		time.Sleep(61 * time.Minute)
		cancel()
		synctest.Wait()

		want := []string{
			"backup@" + at(base, 10*time.Minute), "backup@" + at(base, 20*time.Minute),
			"backup@" + at(base, 30*time.Minute), "backup@" + at(base, 40*time.Minute),
			"prune@" + at(base, 45*time.Minute),
			"backup@" + at(base, 50*time.Minute), "backup@" + at(base, 60*time.Minute),
		}
		got := slices.Clone(rec.due)
		slices.SortStableFunc(got, func(a, b string) int { return compareTime(a, b) })
		if !slices.Equal(got, want) {
			t.Errorf("due = %q, want %q", got, want)
		}
		if len(rec.missed) != 0 || len(rec.resumed) != 0 {
			t.Errorf("missed = %q, resumed = %v", rec.missed, rec.resumed)
		}
	})
}

// compareTime orders "kind@HH:MM" strings by time, then kind.
func compareTime(a, b string) int {
	ta, tb := a[len(a)-5:], b[len(b)-5:]
	switch {
	case ta < tb:
		return -1
	case ta > tb:
		return 1
	}
	return 0
}

func TestRunNextAndRemove(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		base := hourStart()
		s := New(Options{})
		if _, ok := s.Next("backup"); ok {
			t.Error("Next() without a schedule")
		}
		s.Set("backup", mustParse(t, "30 * * * *"))
		next, ok := s.Next("backup")
		if !ok || !next.Equal(base.Add(30*time.Minute)) {
			t.Errorf("Next() = %v, %v", next, ok)
		}
		s.Set("backup", nil)
		if _, ok := s.Next("backup"); ok {
			t.Error("the schedule was not removed")
		}
	})
}

func TestRunDetectsSuspend(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		base := hourStart()
		var offset atomic.Int64 // simulated suspend: the wall clock jumps, timers do not
		now := func() time.Time { return time.Now().Add(time.Duration(offset.Load())) }
		s := New(Options{Now: now})
		s.Set("backup", mustParse(t, "0 * * * *")) // hourly
		var rec recorder
		ctx, cancel := context.WithCancel(t.Context())
		go s.Run(ctx, rec.handler())

		time.Sleep(10 * time.Minute)
		offset.Store(int64(5*time.Hour + 20*time.Minute)) // wakes up 5h30m after the start
		time.Sleep(10 * time.Second)
		synctest.Wait()

		if len(rec.resumed) != 1 || rec.resumed[0] < 5*time.Hour {
			t.Errorf("resumed = %v, want one jump of more than 5h", rec.resumed)
		}
		if want := []string{"backup@" + at(base, time.Hour)}; !slices.Equal(rec.missed, want) {
			t.Errorf("missed = %q, want %q: runs missed during the suspend must not start late", rec.missed, want)
		}
		if len(rec.due) != 0 {
			t.Errorf("due = %q", rec.due)
		}
		if next, _ := s.Next("backup"); !next.Equal(base.Add(6 * time.Hour)) {
			t.Errorf("next = %v, want %v on the new clock", next, base.Add(6*time.Hour))
		}

		// regular operation continues afterwards
		time.Sleep(31 * time.Minute)
		cancel()
		synctest.Wait()
		if want := []string{"backup@" + at(base, 6*time.Hour)}; !slices.Equal(rec.due, want) {
			t.Errorf("due = %q, want %q", rec.due, want)
		}
	})
}

func TestStaleScheduleIsSkipped(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		base := hourStart()
		// a long tick makes the scheduler sleep far past the due time
		s := New(Options{Tick: time.Hour, StaleAfter: time.Minute})
		s.Set("backup", mustParse(t, "5 * * * *"))
		s.mu.Lock()
		s.jobs["backup"].next = base.Add(5 * time.Minute)
		s.mu.Unlock()
		var rec recorder
		ctx, cancel := context.WithCancel(t.Context())
		go s.Run(ctx, rec.handler())
		time.Sleep(10 * time.Minute) // wakes exactly when the schedule is due: not stale
		cancel()
		synctest.Wait()
		if want := []string{"backup@" + at(base, 5*time.Minute)}; !slices.Equal(rec.due, want) {
			t.Errorf("due = %q, missed = %q", rec.due, rec.missed)
		}
	})
}

func TestWatchFile(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "cron")
		s := New(Options{})
		s.WatchFile("backup", path, time.UTC) // does not exist yet
		if _, ok := s.Next("backup"); ok {
			t.Fatal("a missing file must not schedule anything")
		}

		write := func(content string, mtime time.Time) {
			t.Helper()
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(path, mtime, mtime); err != nil {
				t.Fatal(err)
			}
		}
		var rec recorder
		ctx, cancel := context.WithCancel(t.Context())
		go s.Run(ctx, rec.handler())

		write("20 * * * *\n", time.Unix(1000, 0))
		time.Sleep(10 * time.Second)
		if next, ok := s.Next("backup"); !ok || next.Minute() != 20 {
			t.Errorf("after creating the file: %v, %v", next, ok)
		}

		write("not a cron expression", time.Unix(2000, 0))
		time.Sleep(10 * time.Second)
		if next, ok := s.Next("backup"); !ok || next.Minute() != 20 {
			t.Errorf("an invalid file must keep the previous schedule: %v, %v", next, ok)
		}

		write("40 * * * *", time.Unix(3000, 0))
		time.Sleep(10 * time.Second)
		if next, ok := s.Next("backup"); !ok || next.Minute() != 40 {
			t.Errorf("after the change: %v, %v", next, ok)
		}
		cancel()
		synctest.Wait()
	})
}

func TestMissed(t *testing.T) {
	daily := mustParse(t, "0 2 * * *")
	at := func(day, h, m int) time.Time { return time.Date(2026, 1, day, h, m, 0, 0, time.UTC) }
	tests := []struct {
		name    string
		last    time.Time
		hasLast bool
		now     time.Time
		want    bool
	}{
		{"no snapshot yet", time.Time{}, false, at(5, 1, 0), true},
		{"snapshot after the latest slot", at(5, 2, 0), true, at(5, 12, 0), false},
		{"slot passed since the snapshot", at(4, 2, 0), true, at(5, 3, 0), true},
		{"slot not reached yet", at(4, 2, 0), true, at(5, 1, 59), false},
		{"exactly at the slot", at(4, 2, 0), true, at(5, 2, 0), true},
		{"manual backup after the slot", at(5, 9, 0), true, at(6, 1, 0), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Missed(daily, tt.last, tt.hasLast, tt.now); got != tt.want {
				t.Errorf("Missed() = %v, want %v", got, tt.want)
			}
		})
	}
}
