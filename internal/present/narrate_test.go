package present

import (
	"testing"

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
