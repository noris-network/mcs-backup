package daemon

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/noris-network/mcs-backup/v2/internal/engine"
	"github.com/noris-network/mcs-backup/v2/internal/service"
)

// start runs the daemon until the returned stop function is called.
func start(t *testing.T, d *testDaemon) (ctx context.Context, stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	return ctx, func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Run() = %v", err)
		}
	}
}

func get(t *testing.T, d *Daemon, path string) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
	d.Handler().ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

func scrape(t *testing.T, d *Daemon) string {
	t.Helper()
	code, body := get(t, d, "/metrics")
	if code != http.StatusOK {
		t.Fatalf("/metrics = %d", code)
	}
	return body
}

func TestStartReadsTheRepository(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := newTestDaemon(t, func(f *fakeEngine) {
			f.setSnaps(
				snap("a", "me", 2*time.Hour, 100), snap("b", "me", time.Hour, 100),
				snap("c", "me", time.Minute, 100, service.TagPreRestore), snap("d", "other", time.Second, 1))
		})
		ctx, stop := start(t, d)
		synctest.Wait()

		st, _ := d.Status(ctx)
		if !st.Ready || !st.Repository.Up || st.Repository.ID != "repo1" || st.Repository.Snapshots != 3 {
			t.Fatalf("status = %+v", st)
		}
		if want := time.Now().Add(-time.Hour); !st.Repository.LastSuccess.Equal(want) {
			t.Errorf("last success = %v, want %v: safety snapshots and foreign hosts do not count", st.Repository.LastSuccess, want)
		}
		if st.Repository.SizeBytes != 4096 || st.Version != "v2-test" || st.Host != "me" {
			t.Errorf("status = %+v", st)
		}
		stop()
	})
}

func TestRestartDoesNotRaiseFalseAlarms(t *testing.T) {
	// A restarted pod has no memory of earlier runs. The metrics must show the
	// last success from the repository at once and must not claim a failed run.
	synctest.Test(t, func(t *testing.T) {
		d := newTestDaemon(t, func(f *fakeEngine) { f.setSnaps(snap("a", "me", 3*time.Hour, 100)) })

		_, body := get(t, d.Daemon, "/metrics")
		for _, absent := range []string{"mcsbackup_last_success_timestamp_seconds", "mcsbackup_snapshots", "mcsbackup_last_run_success"} {
			if strings.Contains(body, absent+" ") || strings.Contains(body, absent+"{") {
				t.Errorf("before the repository answered, %s must be absent", absent)
			}
		}
		if !strings.Contains(body, "mcsbackup_repository_up 0") {
			t.Error("repository_up must be 0 until the repository answered")
		}

		_, stop := start(t, d)
		synctest.Wait()
		body = scrape(t, d.Daemon)
		want := float64(time.Now().Add(-3 * time.Hour).Unix())
		if got, ok := metricValue(body, "mcsbackup_last_success_timestamp_seconds"); !ok || got != want {
			t.Errorf("last_success_timestamp_seconds = %v (present: %v), want %v right after the start", got, ok, want)
		}
		if !strings.Contains(body, "mcsbackup_repository_up 1") || !strings.Contains(body, "mcsbackup_snapshots 1") {
			t.Errorf("repository_up/snapshots wrong:\n%s", grep(body, "repository_up", "mcsbackup_snapshots"))
		}
		for _, absent := range []string{"mcsbackup_last_run_success", "mcsbackup_last_run_timestamp_seconds", "mcsbackup_last_run_duration_seconds"} {
			if strings.Contains(body, absent) {
				t.Errorf("%s must be absent until a run happened since the start", absent)
			}
		}
		stop()
	})
}

func TestMetricsAfterARun(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := newTestDaemon(t, func(f *fakeEngine) {
			f.setSnaps(snap("a", "me", time.Hour, 100))
			f.backup = func(_ context.Context, _ service.BackupRequest, _ engine.Sink) engine.BackupResult {
				time.Sleep(90 * time.Second)
				return engine.BackupResult{Result: service.ResultSuccess, Snapshot: "new1", Duration: 90 * time.Second}
			}
		})
		ctx, stop := start(t, d)
		synctest.Wait()

		if _, err := d.StartBackup(ctx, service.BackupRequest{}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if body := scrape(t, d.Daemon); !strings.Contains(body, `mcsbackup_operation_running{operation="backup"} 1`) {
			t.Errorf("operation_running:\n%s", grep(body, "operation_running"))
		}
		time.Sleep(2 * time.Minute) // the backup takes 90 seconds
		synctest.Wait()

		body := scrape(t, d.Daemon)
		for _, want := range []string{
			`mcsbackup_last_run_success{kind="backup"} 1`,
			`mcsbackup_last_run_duration_seconds{kind="backup"} 90`,
			`mcsbackup_operation_running{operation="backup"} 0`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("missing %q:\n%s", want, grep(body, "last_run", "operation_running"))
			}
		}
		stop()
	})
}

func TestRepositoryDownAtStart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := newTestDaemon(t, func(f *fakeEngine) {
			f.openMain = func(call int) (engine.RepoInfo, error) {
				if call < 3 {
					return engine.RepoInfo{}, errors.New("connection refused")
				}
				return engine.RepoInfo{ID: "repo1"}, nil
			}
		})
		ctx, stop := start(t, d)
		synctest.Wait()

		// the service is ready although the repository is down
		if code, _ := get(t, d.Daemon, "/readyz"); code != http.StatusOK {
			t.Errorf("/readyz = %d: readiness must not depend on the repository", code)
		}
		if code, _ := get(t, d.Daemon, "/healthz"); code != http.StatusOK {
			t.Errorf("/healthz = %d", code)
		}
		st, _ := d.Status(ctx)
		if st.Repository.Up || !strings.Contains(st.Repository.Error, "connection refused") || st.Ready {
			t.Errorf("status = %+v", st)
		}
		if !hasWarning(st, "repository_down") {
			t.Errorf("warnings = %+v", st.Warnings)
		}
		if body := scrape(t, d.Daemon); !strings.Contains(body, "mcsbackup_repository_up 0") {
			t.Error("repository_up must be 0")
		}

		// retried with growing pauses: 10s, then 20s
		time.Sleep(31 * time.Second)
		synctest.Wait()
		if d.eng.openMains != 3 {
			t.Errorf("open attempts = %d, want 3", d.eng.openMains)
		}
		st, _ = d.Status(ctx)
		if !st.Repository.Up || !st.Ready {
			t.Errorf("status after recovery = %+v", st)
		}
		if names := d.notes.names(); len(names) != 0 {
			t.Errorf("notifications = %q: a short outage during the start must not alarm anybody", names)
		}
		stop()
	})
}

func TestRepositoryDownNotificationAfterGrace(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var healthy atomic.Bool
		d := newTestDaemon(t, func(f *fakeEngine) {
			f.openMain = func(int) (engine.RepoInfo, error) {
				if healthy.Load() {
					return engine.RepoInfo{ID: "repo1"}, nil
				}
				return engine.RepoInfo{}, errors.New("connection refused")
			}
		})
		_, stop := start(t, d)
		time.Sleep(5 * time.Minute) // longer than the 2 minute grace period
		synctest.Wait()
		if got := d.notes.names(); len(got) != 1 || got[0] != "repository.down" {
			t.Fatalf("notifications = %q, want one repository.down", got)
		}
		if ev := d.notes.events[0]; ev.Repository != "main" || !strings.Contains(ev.Error, "connection refused") {
			t.Errorf("event = %+v", ev)
		}

		time.Sleep(10 * time.Minute) // still down: no repeated alarm
		synctest.Wait()
		if got := d.notes.names(); len(got) != 1 {
			t.Errorf("notifications = %q", got)
		}

		healthy.Store(true)
		time.Sleep(2 * time.Minute)
		synctest.Wait()
		if got := d.notes.names(); len(got) != 2 || got[1] != "repository.up" {
			t.Errorf("notifications = %q, want repository.down then repository.up", got)
		}
		stop()
	})
}

func TestMonitorDetectsAnOutage(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := newTestDaemon(t, func(f *fakeEngine) { f.setSnaps(snap("a", "me", time.Hour, 100)) })
		ctx, stop := start(t, d)
		synctest.Wait()
		if st, _ := d.Status(ctx); !st.Repository.Up {
			t.Fatal("not up")
		}

		d.eng.mu.Lock()
		d.eng.snapsErr = errors.New("timeout")
		d.eng.mu.Unlock()
		time.Sleep(6 * time.Minute) // the idle monitor looks every 5 minutes
		synctest.Wait()
		if st, _ := d.Status(ctx); st.Repository.Up || !strings.Contains(st.Repository.Error, "timeout") {
			t.Errorf("the outage was not noticed: %+v", st.Repository)
		}
		stop()
	})
}

func TestStatusWarnings(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := newTestDaemon(t, func(f *fakeEngine) {
			f.setSnaps(snap("a", "me", 100*time.Hour, 100), snap("b", "me", time.Hour, 5, service.TagSuspicious))
			f.locks = []service.Lock{{ID: "l1", Stale: true}, {ID: "l2"}}
			f.verifyConfigured = false
		}, "CRON_SCHEDULE=0 * * * *")
		ctx, stop := start(t, d)
		synctest.Wait()
		if _, err := d.SetSchedule(ctx, service.ScheduleRequest{Action: service.ScheduleDisable}); err != nil {
			t.Fatal(err)
		}
		st, _ := d.Status(ctx)
		for _, code := range []string{"backup_suspicious", "stale_locks", "backups_disabled", "verify_not_configured"} {
			if !hasWarning(st, code) {
				t.Errorf("no %s warning: %+v", code, st.Warnings)
			}
		}
		if !st.Suspicious {
			t.Error("Status.Suspicious")
		}

		if _, err := d.SetSchedule(ctx, service.ScheduleRequest{Action: service.ScheduleEnable}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		st, _ = d.Status(ctx)
		if !hasWarning(st, "backup_old") {
			t.Errorf("a backup 100h old for an hourly schedule should warn: %+v", st.Warnings)
		}
		stop()
	})
}

func TestSnapshotsListsOnlyOwnNewestFirst(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := newTestDaemon(t, func(f *fakeEngine) {
			f.setSnaps(snap("old", "me", 3*time.Hour, 1), snap("legacy", service.LegacyHost, 2*time.Hour, 1),
				snap("new", "me", time.Hour, 1, "keep", "note:x"), snap("foreign", "other", time.Minute, 1))
		})
		got, err := d.Snapshots(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, s := range got {
			ids = append(ids, s.ID)
		}
		if strings.Join(ids, ",") != "new,legacy,old" {
			t.Errorf("ids = %v", ids)
		}
		if !got[0].Keep || got[0].Note != "x" {
			t.Errorf("snapshot = %+v", got[0])
		}
	})
}

func hasWarning(st service.Status, code string) bool {
	for _, w := range st.Warnings {
		if w.Code == code {
			return true
		}
	}
	return false
}

func grep(body string, needles ...string) string {
	var out []string
	for line := range strings.SplitSeq(body, "\n") {
		for _, n := range needles {
			if strings.Contains(line, n) && !strings.HasPrefix(line, "#") {
				out = append(out, line)
			}
		}
	}
	return strings.Join(out, "\n")
}

// metricValue returns the value of a series without labels.
func metricValue(body, name string) (float64, bool) {
	for line := range strings.SplitSeq(body, "\n") {
		if rest, ok := strings.CutPrefix(line, name+" "); ok {
			v, err := strconv.ParseFloat(strings.TrimSpace(rest), 64)
			return v, err == nil
		}
	}
	return 0, false
}
