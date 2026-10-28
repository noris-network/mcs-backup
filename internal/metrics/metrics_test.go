package metrics

import (
	"slices"
	"strings"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
)

var t0 = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

// gather returns the series of the mcsbackup_ metrics by name, each as
// "label=value,..." mapped to its value.
func gather(t *testing.T, s State, labels map[string]string) map[string]map[string]float64 {
	t.Helper()
	reg := NewRegistry(func() State { return s }, labels)
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() error: %v", err)
	}
	out := map[string]map[string]float64{}
	for _, f := range families {
		name, ok := strings.CutPrefix(f.GetName(), namespace+"_")
		if !ok {
			continue
		}
		out[name] = map[string]float64{}
		for _, m := range f.GetMetric() {
			out[name][labelString(m)] = value(f.GetType(), m)
		}
	}
	return out
}

func labelString(m *dto.Metric) string {
	var parts []string
	for _, l := range m.GetLabel() {
		parts = append(parts, l.GetName()+"="+l.GetValue())
	}
	slices.Sort(parts)
	return strings.Join(parts, ",")
}

func value(typ dto.MetricType, m *dto.Metric) float64 {
	if typ == dto.MetricType_COUNTER {
		return m.GetCounter().GetValue()
	}
	return m.GetGauge().GetValue()
}

func absent(t *testing.T, got map[string]map[string]float64, names ...string) {
	t.Helper()
	for _, n := range names {
		if _, ok := got[n]; ok {
			t.Errorf("series %s must be absent: %v", n, got[n])
		}
	}
}

func TestRightAfterStartNothingFalsePositive(t *testing.T) {
	// the service just started and has not talked to the repository yet
	got := gather(t, State{Version: "v2.0.0", BackupEnabled: true, Running: map[string]bool{}}, nil)

	absent(t, got,
		"last_success_timestamp_seconds", "snapshots", "repository_size_bytes", "backup_suspicious", "forget_held",
		"last_run_success", "last_run_timestamp_seconds", "last_run_duration_seconds",
		"backup_files", "backup_bytes", "next_run_timestamp_seconds", "copy_repository_up", "copy_last_success_timestamp_seconds")
	if got["repository_up"][""] != 0 {
		t.Error("repository_up should be 0 until the repository answered")
	}
	if got["info"]["repository_id=,version=v2.0.0"] != 1 {
		t.Errorf("info = %v", got["info"])
	}
	if len(got["operation_running"]) != 6 {
		t.Errorf("operation_running = %v", got["operation_running"])
	}
}

func TestRestartKeepsLastSuccessWithoutLastRun(t *testing.T) {
	// the repository was read at start: the last success is known from the
	// snapshots, but no run happened since the start
	got := gather(t, State{
		RepositoryUp: true, RepositoryID: "abc", SnapshotsKnown: true, Snapshots: 12, LastSuccess: t0.Add(-2 * time.Hour),
		BackupEnabled: true,
	}, nil)

	if want := float64(t0.Add(-2 * time.Hour).Unix()); got["last_success_timestamp_seconds"][""] != want {
		t.Errorf("last_success = %v, want %v", got["last_success_timestamp_seconds"], want)
	}
	if got["snapshots"][""] != 12 || got["backup_suspicious"][""] != 0 || got["forget_held"][""] != 0 {
		t.Errorf("snapshots/suspicious/held = %v / %v / %v", got["snapshots"], got["backup_suspicious"], got["forget_held"])
	}
	absent(t, got, "last_run_success", "last_run_timestamp_seconds", "last_run_duration_seconds")
}

func TestFullState(t *testing.T) {
	s := State{
		Version: "v2.0.0", RepositoryID: "abc",
		RepositoryUp: true, CopyConfigured: true, CopyUp: false,
		SnapshotsKnown: true, Snapshots: 7, LastSuccess: t0, CopyLastSuccess: t0.Add(-time.Hour),
		SizeKnown: true, SizeBytes: 12345, Suspicious: true, ForgetHeld: true,
		BackupEnabled: false, MaintenanceUntil: t0.Add(time.Hour),
		Next:    map[string]time.Time{"backup": t0.Add(time.Minute), "prune": t0.Add(time.Hour), "check": {}},
		Running: map[string]bool{"backup": true},
		LastRuns: map[string]LastRun{
			"backup": {Success: true, At: t0, Duration: 90 * time.Second},
			"verify": {Success: false, At: t0.Add(-time.Hour), Duration: time.Minute},
		},
		LastBackup:  &BackupStats{FilesNew: 1, FilesChanged: 2, FilesUnmodified: 3, BytesProcessed: 400, BytesAdded: 50},
		StatsErrors: 2, NotifyErrors: 3,
		WebUIEnabled: true, WebUIUntil: t0.Add(30 * time.Minute),
	}
	got := gather(t, s, nil)

	checks := []struct {
		name, labels string
		want         float64
	}{
		{"repository_up", "", 1},
		{"copy_repository_up", "", 0},
		{"copy_last_success_timestamp_seconds", "", float64(t0.Add(-time.Hour).Unix())},
		{"repository_size_bytes", "", 12345},
		{"backup_suspicious", "", 1},
		{"forget_held", "", 1},
		{"backup_enabled", "", 0},
		{"maintenance_until_timestamp_seconds", "", float64(t0.Add(time.Hour).Unix())},
		{"next_run_timestamp_seconds", "kind=backup", float64(t0.Add(time.Minute).Unix())},
		{"next_run_timestamp_seconds", "kind=prune", float64(t0.Add(time.Hour).Unix())},
		{"operation_running", "operation=backup", 1},
		{"operation_running", "operation=restore", 0},
		{"last_run_success", "kind=backup", 1},
		{"last_run_success", "kind=verify", 0},
		{"last_run_duration_seconds", "kind=backup", 90},
		{"last_run_timestamp_seconds", "kind=verify", float64(t0.Add(-time.Hour).Unix())},
		{"backup_files", "state=changed", 2},
		{"backup_bytes", "type=added", 50},
		{"stats_errors_total", "", 2},
		{"notify_errors_total", "", 3},
		{"webui_enabled", "", 1},
		{"webui_until_timestamp_seconds", "", float64(t0.Add(30 * time.Minute).Unix())},
	}
	for _, c := range checks {
		if v, ok := got[c.name][c.labels]; !ok || v != c.want {
			t.Errorf("%s{%s} = %v (present: %v), want %v", c.name, c.labels, v, ok, c.want)
		}
	}
	if _, ok := got["next_run_timestamp_seconds"]["kind=check"]; ok {
		t.Error("a kind without a next run must have no series")
	}
}

func TestConstantLabels(t *testing.T) {
	got := gather(t, State{RepositoryUp: true, Running: map[string]bool{}, Next: map[string]time.Time{"backup": t0}},
		map[string]string{"team": "storage", "env": "prod"})
	if _, ok := got["repository_up"]["env=prod,team=storage"]; !ok {
		t.Errorf("repository_up = %v: METRICS_LABELS must appear on every series", got["repository_up"])
	}
	if _, ok := got["next_run_timestamp_seconds"]["env=prod,kind=backup,team=storage"]; !ok {
		t.Errorf("next_run_timestamp_seconds = %v", got["next_run_timestamp_seconds"])
	}
}

func TestEveryNameIsPrefixed(t *testing.T) {
	reg := NewRegistry(func() State { return State{Running: map[string]bool{}} }, nil)
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	var ours int
	for _, f := range families {
		if strings.HasPrefix(f.GetName(), "mcsbackup_") {
			ours++
		}
	}
	if ours < 8 {
		t.Errorf("only %d mcsbackup_ families", ours)
	}
	// runtime metrics come along
	if !slices.ContainsFunc(families, func(f *dto.MetricFamily) bool { return f.GetName() == "go_goroutines" }) {
		t.Error("Go runtime metrics are missing")
	}
}
