package present

import (
	"testing"
	"time"

	"github.com/noris-network/mcs-backup/v2/internal/service"
)

func TestLogLine(t *testing.T) {
	for _, tc := range []struct {
		in, want string
		keep     bool
	}{
		{"using snapshot 7c534454 as parent", "incremental to snapshot 7c534454", true},
		{"forget on the main repository removed 1 snapshots", "removed 1 snapshot from the main repository", true},
		{"forget on the copy repository removed 3 snapshots", "removed 3 snapshots from the secondary repository", true},
		{"snapshot a4a4e24d of [/data] at 2026-10-29 08:54:07.124627729 +0100 CET by me@host", "copying snapshot a4a4e24d", true},
		{"snapshot 63565d3f saved", "saved as snapshot 63565d3f", true},
		{"  copy started, this may take a while...", "", false},
		{"[0:00]          0 packs copied", "", false},
		{"[0:07]         12 packs copied", "[0:07]         12 packs copied", true},
		{"", "", false},
		{"   ", "", false},
		{"pre-backup: dumping the database", "pre-backup: dumping the database", true},
	} {
		got, keep := LogLine(tc.in)
		if got != tc.want || keep != tc.keep {
			t.Errorf("LogLine(%q) = %q, %v; want %q, %v", tc.in, got, keep, tc.want, tc.keep)
		}
	}
}

func TestPhaseTitleAndResultMark(t *testing.T) {
	if got := PhaseTitle("forget copy"); got != "Applying the retention on the secondary repository" {
		t.Errorf("PhaseTitle = %q", got)
	}
	if got := PhaseTitle("something new"); got != "Something new" {
		t.Errorf("an unknown phase = %q", got)
	}
	for r, want := range map[service.Result]string{service.ResultSuccess: "✓", service.ResultWarning: "!", service.ResultSkipped: "-", service.ResultFailure: "✗", service.ResultSuspicious: "✗"} {
		if got := ResultMark(r); got != want {
			t.Errorf("ResultMark(%s) = %q, want %q", r, got, want)
		}
	}
}

func TestResultLinesDoNotRepeatTheSnapshot(t *testing.T) {
	r := service.RunResult{Kind: service.KindBackup, Result: service.ResultSuccess, Snapshot: "abcdef0123", Message: "snapshot abcdef01 created: 5 files"}
	if got := ResultLines(r); len(got) != 2 {
		t.Errorf("lines = %q", got)
	}
	r.Message = "5 files"
	if got := ResultLines(r); len(got) != 3 || got[1] != "  snapshot abcdef01" {
		t.Errorf("lines = %q", got)
	}
}

func TestResultLinesIndentTheLinesOfAnError(t *testing.T) {
	r := service.RunResult{Kind: service.KindBackup, Result: service.ResultFailure, Error: "context canceled\nrestic backup: exit code 130\n"}
	got := ResultLines(r)
	if len(got) != 3 || got[1] != "  error: context canceled" || got[2] != "    restic backup: exit code 130" {
		t.Errorf("lines = %q", got)
	}
}

func TestCards(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.Local)
	st := service.Status{
		Repository: service.RepoStatus{Up: true, ID: "0123456789abcdef", Snapshots: 3, SizeBytes: 1 << 20, LastSuccess: now.Add(-3 * time.Hour)},
		Schedule:   service.ScheduleStatus{Enabled: true, Next: map[service.Kind]time.Time{service.KindBackup: now.Add(14 * time.Hour)}},
		Copy:       &service.RepoStatus{Up: false, Error: "connection refused", Snapshots: -1, SizeBytes: -1},
	}
	cards := Cards(st, now)
	if len(cards) != 4 {
		t.Fatalf("%d cards", len(cards))
	}
	want := []Card{
		{Title: "Repository", Value: "reachable", Sub: "3 snapshots · 1.0 MiB", Level: LevelOK, Dot: true},
		{Title: "Last backup", Value: "3h ago", Sub: Stamp(now.Add(-3 * time.Hour))},
		{Title: "Next backup", Value: "in 14h", Sub: Stamp(now.Add(14 * time.Hour))},
		{Title: "Secondary copy", Value: "unreachable", Sub: "connection refused", Level: LevelBad, Dot: true},
	}
	for i := range want {
		if cards[i] != want[i] {
			t.Errorf("card %d = %+v, want %+v", i, cards[i], want[i])
		}
	}
	st.Schedule.MaintenanceUntil = now.Add(time.Hour)
	if c := Cards(st, now)[2]; c.Value != "paused" || c.Level != LevelWarn {
		t.Errorf("maintenance: %+v", c)
	}
	st.Schedule = service.ScheduleStatus{}
	if c := Cards(st, now)[2]; c.Value != "disabled" || c.Level != LevelWarn {
		t.Errorf("disabled: %+v", c)
	}
	st.Schedule.Enabled = true
	if c := Cards(st, now)[2]; c.Value != "manual" {
		t.Errorf("no schedule: %+v", c)
	}
	st.Repository.LastSuccess = time.Time{}
	if c := Cards(st, now)[1]; c.Value != "never" || c.Level != LevelWarn {
		t.Errorf("no backup yet: %+v", c)
	}
}

func TestHealth(t *testing.T) {
	up := service.RepoStatus{Up: true}
	for name, tc := range map[string]struct {
		st    service.Status
		label string
		level Level
	}{
		"healthy":       {service.Status{Repository: up}, "healthy", LevelOK},
		"working":       {service.Status{Repository: up, Operation: &service.Operation{}}, "working", LevelNone},
		"warning":       {service.Status{Repository: up, Warnings: []service.Warning{{Severity: service.SeverityWarning}}}, "warnings", LevelWarn},
		"error":         {service.Status{Repository: up, Warnings: []service.Warning{{Severity: service.SeverityWarning}, {Severity: service.SeverityError}}}, "needs attention", LevelBad},
		"error first":   {service.Status{Repository: up, Warnings: []service.Warning{{Severity: service.SeverityError}, {Severity: service.SeverityWarning}}}, "needs attention", LevelBad},
		"repo down":     {service.Status{Repository: service.RepoStatus{Up: false}}, "repository down", LevelBad},
		"info only":     {service.Status{Repository: up, Warnings: []service.Warning{{Severity: service.SeverityInfo}}}, "healthy", LevelOK},
		"warning works": {service.Status{Repository: up, Operation: &service.Operation{}, Warnings: []service.Warning{{Severity: service.SeverityWarning}}}, "warnings", LevelWarn},
	} {
		if label, level := Health(tc.st); label != tc.label || level != tc.level {
			t.Errorf("%s: Health = %q, %q; want %q, %q", name, label, level, tc.label, tc.level)
		}
	}
}
