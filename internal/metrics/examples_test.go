package metrics

import (
	"os"
	"regexp"
	"slices"
	"testing"
	"time"
)

// TestExamplesUseExistingMetrics keeps the shipped alerts and the dashboard in
// step with the metrics: every mcsbackup_ name they query must be exported.
func TestExamplesUseExistingMetrics(t *testing.T) {
	s := State{
		Version: "v2.0.0", RepositoryID: "abc",
		RepositoryUp: true, CopyConfigured: true, CopyUp: true,
		SnapshotsKnown: true, Snapshots: 1, LastSuccess: t0, CopyLastSuccess: t0,
		SizeKnown: true, SizeBytes: 1, BackupEnabled: true,
		Next:         map[string]time.Time{"backup": t0},
		Running:      map[string]bool{},
		LastRuns:     map[string]LastRun{"backup": {Success: true, At: t0}},
		LastBackup:   &BackupStats{},
		WebUIEnabled: true,
	}
	exported := gather(t, s, nil)

	name := regexp.MustCompile(`mcsbackup_[a-z0-9_]+`)
	for _, file := range []string{"prometheusrule.yaml", "grafana-dashboard.json"} {
		data, err := os.ReadFile("../../docs/examples/monitoring/" + file)
		if err != nil {
			t.Fatal(err)
		}
		used := name.FindAllString(string(data), -1)
		if len(used) == 0 {
			t.Errorf("%s uses no mcsbackup_ metric", file)
		}
		for _, n := range slices.Compact(slices.Sorted(slices.Values(used))) {
			if _, ok := exported[n[len(namespace)+1:]]; !ok {
				t.Errorf("%s queries %s, which mcs-backup does not export", file, n)
			}
		}
	}
}
