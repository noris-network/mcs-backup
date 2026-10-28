package daemon

import (
	"context"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/noris-network/mcs-backup/v2/internal/engine"
	"github.com/noris-network/mcs-backup/v2/internal/restic"
	"github.com/noris-network/mcs-backup/v2/internal/service"
)

// failing makes the first n backups fail.
func failing(n int, result service.Result) func(context.Context, service.BackupRequest, engine.Sink) engine.BackupResult {
	var calls atomic.Int32
	return func(context.Context, service.BackupRequest, engine.Sink) engine.BackupResult {
		if int(calls.Add(1)) <= n {
			return engine.BackupResult{Result: result, Error: "disk full"}
		}
		return engine.BackupResult{Result: service.ResultSuccess, Snapshot: "new1"}
	}
}

func TestScheduledBackupIsRetried(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := newTestDaemon(t, func(f *fakeEngine) { f.backup = failing(99, service.ResultFailure) })
		ctx, stop := start(t, d)
		synctest.Wait()

		d.handleDue(service.KindBackup, time.Now())
		synctest.Wait()

		st, _ := d.Status(ctx)
		if st.Retry == nil || st.Retry.Attempt != 1 || st.Retry.Max != 2 || !st.Retry.At.Equal(time.Now().Add(5*time.Minute)) {
			t.Fatalf("retry = %+v", st.Retry)
		}
		if r := st.LastRuns[service.KindBackup]; !r.WillRetry || r.Attempt != 1 {
			t.Errorf("run = %+v", r)
		}
		if !hasWarning(st, "backup_retry") {
			t.Errorf("warnings = %+v", st.Warnings)
		}
		if names := d.notes.names(); len(names) != 0 {
			t.Fatalf("notifications = %q: only the last attempt is reported", names)
		}

		// the slot is free while the retry waits
		if op, err := d.StartRestore(ctx, service.RestoreRequest{Snapshot: "latest"}); err != nil {
			t.Errorf("restore while a retry waits: %v (%+v)", err, op)
		}
		synctest.Wait()

		time.Sleep(5 * time.Minute)
		synctest.Wait()
		if d.eng.called("backup") != 2 {
			t.Fatalf("backups = %d, want the first retry after 5 minutes", d.eng.called("backup"))
		}
		if st, _ = d.Status(ctx); st.Retry == nil || st.Retry.Attempt != 2 {
			t.Errorf("retry = %+v", st.Retry)
		}

		time.Sleep(5 * time.Minute)
		synctest.Wait()
		if d.eng.called("backup") != 3 {
			t.Fatalf("backups = %d", d.eng.called("backup"))
		}
		st, _ = d.Status(ctx)
		if st.Retry != nil || st.LastRuns[service.KindBackup].WillRetry {
			t.Errorf("no retries are left: %+v", st.Retry)
		}
		if got := backupNames(d.notes.names()); !slices.Equal(got, []string{"backup.failed"}) {
			t.Errorf("notifications = %q, want exactly one failure after the last attempt", got)
		}
		for _, req := range d.eng.backupReqs {
			if req.Origin != service.OriginScheduled {
				t.Errorf("origin = %q", req.Origin)
			}
		}

		time.Sleep(time.Hour)
		synctest.Wait()
		if d.eng.called("backup") != 3 {
			t.Error("a backup ran after the retries were used up")
		}
		stop()
	})
}

// backupNames keeps the backup notifications.
func backupNames(names []string) []string {
	return slices.DeleteFunc(slices.Clone(names), func(n string) bool { return !strings.HasPrefix(n, "backup.") })
}

func TestRetrySucceeds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := newTestDaemon(t, func(f *fakeEngine) { f.backup = failing(1, service.ResultFailure) })
		ctx, stop := start(t, d)
		synctest.Wait()

		d.handleDue(service.KindBackup, time.Now())
		synctest.Wait()
		time.Sleep(5 * time.Minute)
		synctest.Wait()

		st, _ := d.Status(ctx)
		if r := st.LastRuns[service.KindBackup]; r.Result != service.ResultSuccess || r.Attempt != 2 || st.Retry != nil {
			t.Errorf("run = %+v, retry = %+v", r, st.Retry)
		}
		if got := d.notes.names(); !slices.Equal(got, []string{"backup.succeeded"}) {
			t.Errorf("notifications = %q: the failed first attempt is not reported", got)
		}
		stop()
	})
}

func TestRetryWaitsForTheSlot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		var calls atomic.Int32
		d := newTestDaemon(t, func(f *fakeEngine) {
			f.backup = func(ctx context.Context, req service.BackupRequest, sink engine.Sink) engine.BackupResult {
				if calls.Add(1) == 1 {
					return engine.BackupResult{Result: service.ResultFailure, Error: "boom"}
				}
				return blockingBackup(release)(ctx, req, sink)
			}
		})
		ctx, stop := start(t, d)
		synctest.Wait()

		d.handleDue(service.KindBackup, time.Now())
		synctest.Wait()
		// a restore occupies the slot when the retry is due
		d.eng.restore = func(_ context.Context, _ service.RestoreRequest, _ engine.Sink) engine.RestoreResult {
			<-release
			return engine.RestoreResult{Result: service.ResultSuccess}
		}
		if _, err := d.StartRestore(ctx, service.RestoreRequest{Snapshot: "latest"}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		time.Sleep(6 * time.Minute)
		synctest.Wait()
		if d.eng.called("backup") != 1 {
			t.Fatal("the retry must wait for the slot")
		}

		close(release)
		synctest.Wait()
		if d.eng.called("backup") != 2 {
			t.Errorf("backups = %d: the retry runs as soon as the slot is free", d.eng.called("backup"))
		}
		stop()
	})
}

func TestRetryIsDiscarded(t *testing.T) {
	setup := func(t *testing.T) (*testDaemon, context.Context, func()) {
		d := newTestDaemon(t, func(f *fakeEngine) { f.backup = failing(1, service.ResultFailure) })
		ctx, stop := start(t, d)
		synctest.Wait()
		d.handleDue(service.KindBackup, time.Now())
		synctest.Wait()
		if st, _ := d.Status(ctx); st.Retry == nil {
			t.Fatal("no retry pending")
		}
		return d, ctx, stop
	}

	t.Run("by cancel", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			d, ctx, stop := setup(t)
			_ = d.Cancel(ctx)
			if st, _ := d.Status(ctx); st.Retry != nil {
				t.Error("retry still pending")
			}
			time.Sleep(10 * time.Minute)
			synctest.Wait()
			if d.eng.called("backup") != 1 {
				t.Errorf("backups = %d", d.eng.called("backup"))
			}
			stop()
		})
	})

	t.Run("by a successful manual backup", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			d, ctx, stop := setup(t)
			if _, err := d.StartBackup(ctx, service.BackupRequest{}); err != nil {
				t.Fatal(err)
			}
			synctest.Wait()
			if st, _ := d.Status(ctx); st.Retry != nil {
				t.Error("retry still pending")
			}
			time.Sleep(10 * time.Minute)
			synctest.Wait()
			if d.eng.called("backup") != 2 {
				t.Errorf("backups = %d", d.eng.called("backup"))
			}
			stop()
		})
	})
}

func TestNoRetry(t *testing.T) {
	for name, tc := range map[string]struct {
		result service.Result
		origin service.Origin
	}{
		"timeout":    {service.ResultTimeout, service.OriginScheduled},
		"suspicious": {service.ResultSuspicious, service.OriginScheduled},
		"cancelled":  {service.ResultCancelled, service.OriginScheduled},
		"manual":     {service.ResultFailure, service.OriginManual},
	} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				d := newTestDaemon(t, func(f *fakeEngine) { f.backup = failing(99, tc.result) })
				ctx, stop := start(t, d)
				synctest.Wait()
				if _, err := d.startBackup(ctx, service.BackupRequest{Origin: tc.origin}, 1); err != nil {
					t.Fatal(err)
				}
				synctest.Wait()
				if st, _ := d.Status(ctx); st.Retry != nil {
					t.Errorf("retry = %+v", st.Retry)
				}
				time.Sleep(time.Hour)
				synctest.Wait()
				if d.eng.called("backup") != 1 {
					t.Errorf("backups = %d", d.eng.called("backup"))
				}
				stop()
			})
		})
	}
}

func TestScheduleFiresBackups(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := newTestDaemon(t, func(f *fakeEngine) { f.setSnaps(snap("a", "me", 0, 100)) }, "CRON_SCHEDULE=*/10 * * * *")
		ctx, stop := start(t, d)
		synctest.Wait()

		st, _ := d.Status(ctx)
		next, ok := st.Schedule.Next[service.KindBackup]
		if !ok || next.Before(time.Now()) || time.Until(next) > 10*time.Minute {
			t.Errorf("next = %v, %v", next, ok)
		}
		if body := scrape(t, d.Daemon); grep(body, "next_run_timestamp_seconds") == "" {
			t.Error("next_run_timestamp_seconds missing")
		}
		time.Sleep(25 * time.Minute)
		synctest.Wait()
		if got := d.eng.called("backup"); got != 2 && got != 3 {
			t.Errorf("backups = %d", got)
		}
		if d.eng.backupReqs[0].Origin != service.OriginScheduled {
			t.Errorf("origin = %q", d.eng.backupReqs[0].Origin)
		}
		stop()
	})
}

func TestBackupDueWhileBusyIsSkipped(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		d := newTestDaemon(t, func(f *fakeEngine) { f.backup = blockingBackup(release) })
		ctx, stop := start(t, d)
		synctest.Wait()
		_, _ = d.StartBackup(ctx, service.BackupRequest{})
		synctest.Wait()

		d.handleDue(service.KindBackup, time.Now())
		synctest.Wait()
		close(release)
		synctest.Wait()
		if d.eng.called("backup") != 1 {
			t.Errorf("backups = %d: a due backup is skipped, not queued", d.eng.called("backup"))
		}
		stop()
	})
}

func TestMaintenanceOperationsAreQueued(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		d := newTestDaemon(t, func(f *fakeEngine) { f.backup = blockingBackup(release) })
		ctx, stop := start(t, d)
		synctest.Wait()
		_, _ = d.StartBackup(ctx, service.BackupRequest{})
		synctest.Wait()

		// all three fall due during the backup; a second prune is not queued twice
		for _, k := range []service.Kind{service.KindVerify, service.KindPrune, service.KindCheck, service.KindPrune} {
			d.handleDue(k, time.Now())
		}
		synctest.Wait()
		if d.eng.called("prune")+d.eng.called("check")+d.eng.called("verify") != 0 {
			t.Fatal("maintenance ran during the backup")
		}

		close(release)
		synctest.Wait()
		want := []string{"backup", "prune", "check", "verify"}
		got := slices.DeleteFunc(slices.Clone(d.eng.calls), func(c string) bool { return c == "size" })
		if !slices.Equal(got[len(got)-4:], want) {
			t.Errorf("calls = %q, want the queued operations in the order prune, check, verify after the backup", got)
		}
		if d.eng.called("prune") != 1 {
			t.Errorf("prune ran %d times", d.eng.called("prune"))
		}
		stop()
	})
}

func TestVerifyIsSkippedWhenNotConfigured(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := newTestDaemon(t, func(f *fakeEngine) { f.verifyConfigured = false })
		_, stop := start(t, d)
		synctest.Wait()
		d.handleDue(service.KindVerify, time.Now())
		synctest.Wait()
		if d.eng.called("verify") != 0 {
			t.Error("verify ran although it is not configured")
		}
		stop()
	})
}

func TestMaintenanceWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := newTestDaemon(t, func(f *fakeEngine) { f.setSnaps(snap("a", "me", time.Hour, 100)) })
		ctx, stop := start(t, d)
		synctest.Wait()

		st, err := d.SetSchedule(service.WithSource(ctx, service.SourceCLI), service.ScheduleRequest{Action: service.ScheduleMaintenance, For: service.Duration(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		until := time.Now().Add(time.Hour)
		if !st.Schedule.MaintenanceUntil.Equal(until) || !hasWarning(st, "maintenance") {
			t.Errorf("status = %+v", st.Schedule)
		}
		if body := scrape(t, d.Daemon); !strings.Contains(body, "mcsbackup_backup_enabled 0") {
			t.Error("backup_enabled must be 0 during maintenance")
		}
		var saved persisted
		data, err := os.ReadFile(filepath.Join(d.stateDir, "state.json"))
		if err != nil || json.Unmarshal(data, &saved) != nil || !saved.MaintenanceUntil.Equal(until) {
			t.Errorf("state file: %s, %v", data, err)
		}

		d.handleDue(service.KindBackup, time.Now())
		synctest.Wait()
		if d.eng.called("backup") != 0 {
			t.Error("a scheduled backup ran during the maintenance window")
		}
		// a manual backup is still possible
		if _, err := d.StartBackup(ctx, service.BackupRequest{}); err != nil {
			t.Error(err)
		}
		synctest.Wait()

		time.Sleep(61 * time.Minute)
		synctest.Wait()
		st, _ = d.Status(ctx)
		if !st.Schedule.MaintenanceUntil.IsZero() {
			t.Errorf("the window should be over: %+v", st.Schedule)
		}
		if _, err := os.Stat(filepath.Join(d.stateDir, "state.json")); !os.IsNotExist(err) {
			t.Errorf("state file still there: %v", err)
		}
		d.handleDue(service.KindBackup, time.Now())
		synctest.Wait()
		if d.eng.called("backup") != 2 {
			t.Errorf("backups = %d, scheduled backups work again", d.eng.called("backup"))
		}
		stop()
	})

	t.Run("invalid", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			d := newTestDaemon(t, nil)
			for _, req := range []service.ScheduleRequest{
				{Action: service.ScheduleMaintenance}, {Action: service.ScheduleMaintenance, For: service.Duration(-time.Hour)},
				{Action: service.ScheduleMaintenance, For: service.Duration(365 * 24 * time.Hour)}, {Action: "explode"},
			} {
				if _, err := d.SetSchedule(t.Context(), req); err == nil {
					t.Errorf("%+v was accepted", req)
				}
			}
		})
	})
}

func TestMaintenanceWindowSurvivesARestart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		first := newTestDaemon(t, nil)
		ctx, stop := start(t, first)
		synctest.Wait()
		if _, err := first.SetSchedule(ctx, service.ScheduleRequest{Action: service.ScheduleMaintenance, For: service.Duration(2 * time.Hour)}); err != nil {
			t.Fatal(err)
		}
		stop()

		// the pod restarts: same state directory, new process
		second := newTestDaemon(t, func(f *fakeEngine) { f.setSnaps(snap("a", "me", 48*time.Hour, 100)) },
			"MCS_BACKUP_STATE_DIR="+first.stateDir, "CRON_SCHEDULE=0 3 * * *")
		ctx, stop = start(t, second)
		synctest.Wait()
		st, _ := second.Status(ctx)
		if !st.Schedule.MaintenanceUntil.After(time.Now()) {
			t.Fatalf("the maintenance window was forgotten: %+v", st.Schedule)
		}
		if second.eng.called("backup") != 0 {
			t.Error("a restart during maintenance must not start a catch-up backup")
		}

		time.Sleep(2*time.Hour + time.Minute)
		synctest.Wait()
		if second.eng.called("backup") != 1 || second.eng.backupReqs[0].Origin != service.OriginCatchUp {
			t.Errorf("backups = %d %v: the missed backup is caught up when the window ends", second.eng.called("backup"), second.eng.backupReqs)
		}
		stop()
	})
}

func TestDisableDoesNotSurviveARestart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		first := newTestDaemon(t, nil)
		ctx, stop := start(t, first)
		synctest.Wait()
		st, err := first.SetSchedule(ctx, service.ScheduleRequest{Action: service.ScheduleDisable})
		if err != nil || st.Schedule.Enabled {
			t.Fatalf("status = %+v, %v", st.Schedule, err)
		}
		stop()

		second := newTestDaemon(t, nil, "MCS_BACKUP_STATE_DIR="+first.stateDir)
		ctx, stop = start(t, second)
		synctest.Wait()
		if st, _ := second.Status(ctx); !st.Schedule.Enabled {
			t.Error("a forgotten disable must end with the restart")
		}
		stop()
	})
}

func TestDisableEndsAMaintenanceWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := newTestDaemon(t, func(f *fakeEngine) { f.setSnaps(snap("a", "me", 0, 100)) }, "CRON_SCHEDULE=0 3 * * *")
		ctx, stop := start(t, d)
		synctest.Wait()
		_, _ = d.SetSchedule(ctx, service.ScheduleRequest{Action: service.ScheduleMaintenance, For: service.Duration(time.Hour)})
		st, _ := d.SetSchedule(ctx, service.ScheduleRequest{Action: service.ScheduleDisable})
		if st.Schedule.Enabled || !st.Schedule.MaintenanceUntil.IsZero() {
			t.Errorf("schedule = %+v", st.Schedule)
		}
		time.Sleep(2 * time.Hour)
		synctest.Wait()
		if d.eng.called("backup") != 0 {
			t.Error("the window's end must not re-enable backups after an explicit disable")
		}
		if _, err := os.Stat(filepath.Join(d.stateDir, "state.json")); !os.IsNotExist(err) {
			t.Error("state file still there")
		}
		stop()
	})
}

func TestCatchUp(t *testing.T) {
	tests := []struct {
		name    string
		env     []string
		snaps   func() []restic.Snapshot
		prepare func(d *testDaemon)
		want    int
	}{
		{
			name:  "missed while the service was down",
			env:   []string{"CRON_SCHEDULE=0 3 * * *"},
			snaps: func() []restic.Snapshot { return []restic.Snapshot{snap("a", "me", 30*time.Hour, 100)} },
			want:  1,
		},
		{
			name:  "nothing missed",
			env:   []string{"CRON_SCHEDULE=0 3 * * *"},
			snaps: func() []restic.Snapshot { return []restic.Snapshot{snap("a", "me", time.Hour, 100)} },
			want:  0,
		},
		{
			name:  "a fresh installation gets its first backup",
			env:   []string{"CRON_SCHEDULE=0 3 * * *"},
			snaps: func() []restic.Snapshot { return nil },
			want:  1,
		},
		{
			name: "a safety snapshot is no backup",
			env:  []string{"CRON_SCHEDULE=0 3 * * *"},
			snaps: func() []restic.Snapshot {
				return []restic.Snapshot{snap("a", "me", 30*time.Hour, 100), snap("b", "me", time.Minute, 100, service.TagPreRestore)}
			},
			want: 1,
		},
		{
			name:  "switched off",
			env:   []string{"CRON_SCHEDULE=0 3 * * *", "BACKUP_CATCH_UP=false"},
			snaps: func() []restic.Snapshot { return []restic.Snapshot{snap("a", "me", 30*time.Hour, 100)} },
			want:  0,
		},
		{
			name:  "no schedule, no catch-up",
			snaps: func() []restic.Snapshot { return []restic.Snapshot{snap("a", "me", 30*time.Hour, 100)} },
			want:  0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				d := newTestDaemon(t, func(f *fakeEngine) { f.setSnaps(tt.snaps()...) }, tt.env...)
				_, stop := start(t, d)
				synctest.Wait()
				if got := d.eng.called("backup"); got != tt.want {
					t.Errorf("backups = %d, want %d", got, tt.want)
				}
				if tt.want > 0 && d.eng.backupReqs[0].Origin != service.OriginCatchUp {
					t.Errorf("origin = %q", d.eng.backupReqs[0].Origin)
				}
				stop()
			})
		})
	}
}

func TestCatchUpHappensOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// many slots were missed, one backup catches up
		d := newTestDaemon(t, func(f *fakeEngine) { f.setSnaps(snap("a", "me", 300*time.Hour, 100)) }, "CRON_SCHEDULE=0 * * * *")
		_, stop := start(t, d)
		synctest.Wait()
		if got := d.eng.called("backup"); got != 1 {
			t.Errorf("backups = %d", got)
		}
		stop()
	})
}

func TestNoCatchUpWhileDisabledOrBusy(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := newTestDaemon(t, func(f *fakeEngine) { f.setSnaps(snap("a", "me", 300*time.Hour, 100)) }, "CRON_SCHEDULE=0 3 * * *")
		ctx, stop := start(t, d)
		synctest.Wait()
		base := d.eng.called("backup")

		_, _ = d.SetSchedule(ctx, service.ScheduleRequest{Action: service.ScheduleDisable})
		d.catchUp("test")
		synctest.Wait()
		if d.eng.called("backup") != base {
			t.Error("caught up although backups are disabled")
		}
		stop()
	})
}
