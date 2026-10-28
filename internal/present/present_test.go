package present_test

import (
	"strings"
	"testing"
	"time"

	"github.com/noris-network/mcs-backup/v2/internal/present"
	"github.com/noris-network/mcs-backup/v2/internal/service"
)

func TestDuration(t *testing.T) {
	for in, want := range map[time.Duration]string{
		0: "0s", 45 * time.Second: "45s", 5 * time.Minute: "5m", 125 * time.Minute: "2h 5m", 3 * time.Hour: "3h",
		50 * time.Hour: "2d 2h", 48 * time.Hour: "2d", -90 * time.Second: "-1m",
	} {
		if got := present.Duration(in); got != want {
			t.Errorf("Duration(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestBytes(t *testing.T) {
	for in, want := range map[int64]string{0: "0 B", 1023: "1023 B", 1024: "1.0 KiB", 5 << 20: "5.0 MiB", 3 << 40: "3.0 TiB"} {
		if got := present.Bytes(in); got != want {
			t.Errorf("Bytes(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestStatusRows(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	st := service.Status{
		Version: "2.0.0", Host: "app", StartedAt: now.Add(-2 * time.Hour),
		Repository: service.RepoStatus{Up: true, ID: "0123456789abcdef", Snapshots: 4, SizeBytes: 2048, LastSuccess: now.Add(-3 * time.Hour)},
		Schedule:   service.ScheduleStatus{Enabled: true, Backup: "0 2 * * *"},
		Retention:  "the last 7 snapshots",
		Warnings:   []service.Warning{{Code: "x", Severity: service.SeverityWarning, Message: "forget is held back"}},
	}
	var got []string
	for _, r := range present.StatusRows(st, now) {
		got = append(got, r.Label+"|"+r.Text)
	}
	text := strings.Join(got, "\n")
	for _, want := range []string{"Repository|reachable, id 01234567, 4 snapshots, 2.0 KiB", "Schedule|backups enabled (0 2 * * *)",
		"Retention|keeps the last 7 snapshots", "Operation|idle", "Notes|!  forget is held back", "up 2h"} {
		if !strings.Contains(text, want) {
			t.Errorf("rows lack %q:\n%s", want, text)
		}
	}
}

func TestAppendOnlyRetention(t *testing.T) {
	if got := present.RetentionLine(service.Status{AppendOnly: true}); !strings.Contains(got, "append-only") {
		t.Errorf("RetentionLine = %q", got)
	}
}
