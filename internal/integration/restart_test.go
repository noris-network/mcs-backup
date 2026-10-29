//go:build integration

package integration

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/noris-network/mcs-backup/v2/internal/service"
)

// oldSnapshot puts a snapshot of a previous run into the repository.
func (i *inst) oldSnapshot(when string) {
	i.t.Helper()
	i.restic("init")
	i.restic("backup", "--host", "mcs-backup", "--tag", "mcs-backup,scheduled", "--time", when, i.root)
}

func (i *inst) metrics() string {
	i.t.Helper()
	req, err := http.NewRequestWithContext(i.t.Context(), http.MethodGet, "http://"+i.d.Addr()+"/metrics", nil)
	if err != nil {
		i.t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		i.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

// metricValue returns the line of a series, empty if it does not exist.
func metricValue(metrics, name string) string {
	for line := range strings.Lines(metrics) {
		if strings.HasPrefix(line, name+" ") || strings.HasPrefix(line, name+"{") {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

func TestRestartKnowsTheLastBackupAtOnce(t *testing.T) {
	i := newInst(t)
	i.write("a.txt", "a")
	i.start()
	i.mustBackup("", false)
	i.stop()

	// a new process (the pod restarted): the series must be there as soon as it is ready
	i.start()
	m := i.metrics()
	for _, name := range []string{"mcsbackup_last_success_timestamp_seconds", "mcsbackup_snapshots", "mcsbackup_repository_up"} {
		if metricValue(m, name) == "" {
			t.Errorf("%s is missing right after the restart:\n%s", name, m)
		}
	}
	if got := metricValue(m, "mcsbackup_snapshots"); !strings.HasSuffix(got, " 1") {
		t.Errorf("snapshots: %q", got)
	}
	// run results are only known after a run: no series, so an alert on == 0 cannot fire
	if got := metricValue(m, "mcsbackup_last_run_success"); got != "" {
		t.Errorf("last_run_success exists before any run: %q", got)
	}
}

func TestMissedBackupIsCaughtUpAfterTheStart(t *testing.T) {
	i := newInst(t, "CRON_SCHEDULE=0 0 * * *")
	i.write("a.txt", "a")
	i.oldSnapshot("2020-01-01 00:00:00")
	i.start()

	i.waitFor("the catch-up backup", 60*time.Second, func() bool {
		for _, s := range i.snapshots() {
			if s.Origin == service.OriginCatchUp {
				return true
			}
		}
		return false
	})
	st := i.status()
	if st.LastRuns[service.KindBackup].Result == service.ResultFailure {
		t.Errorf("the catch-up failed: %+v", st.LastRuns[service.KindBackup])
	}
}

func TestNoCatchUpWithoutASchedule(t *testing.T) {
	i := newInst(t)
	i.write("a.txt", "a")
	i.oldSnapshot("2020-01-01 00:00:00")
	i.start()
	time.Sleep(2 * time.Second)
	if n := len(i.snapshots()); n != 1 {
		t.Errorf("snapshots = %d: without a schedule nothing is missed", n)
	}
}

func TestMaintenanceWindowSurvivesARestart(t *testing.T) {
	i := newInst(t, "CRON_SCHEDULE=0 0 * * *")
	i.write("a.txt", "a")
	i.oldSnapshot("2020-01-01 00:00:00")
	i.set("BACKUP_CATCH_UP=false")
	i.start()
	if _, err := i.d.SetSchedule(i.ctx(), service.ScheduleRequest{Action: service.ScheduleMaintenance, For: service.Duration(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	i.stop()

	i.env = i.env[:len(i.env)-1] // catch-up is on again: only the maintenance window may stop it
	i.start()
	if st := i.status(); st.Schedule.MaintenanceUntil.IsZero() {
		t.Fatalf("the maintenance window is gone after the restart: %+v", st.Schedule)
	}
	time.Sleep(2 * time.Second)
	if n := len(i.snapshots()); n != 1 {
		t.Errorf("snapshots = %d: no catch-up backup may run during a maintenance window", n)
	}
}

func TestDisabledBackupsAreOnAgainAfterARestart(t *testing.T) {
	i := newInst(t, "CRON_SCHEDULE=0 0 * * *", "BACKUP_CATCH_UP=false")
	i.write("a.txt", "a")
	i.start()
	if _, err := i.d.SetSchedule(i.ctx(), service.ScheduleRequest{Action: service.ScheduleDisable}); err != nil {
		t.Fatal(err)
	}
	if i.status().Schedule.Enabled {
		t.Fatal("still enabled after disable")
	}
	i.stop()
	i.start()
	if !i.status().Schedule.Enabled {
		t.Error("a forgotten disable must end with the restart")
	}
}
