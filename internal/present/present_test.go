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
	for in, want := range map[int64]string{
		0: "0 B", 1023: "1023 B", 1024: "1.0 KiB", 5 << 20: "5.0 MiB", 3 << 40: "3.0 TiB",
		1<<30 - 1<<10: "1.0 GiB", 1023 << 20: "1023.0 MiB", // rounded up to the next unit only where it would read 1024.0
	} {
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

func TestDiffLines(t *testing.T) {
	at := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	d := service.DiffResult{
		From: service.Snapshot{ShortID: "aaaaaaaa", Time: at}, To: service.Snapshot{ShortID: "bbbbbbbb", Time: at.Add(time.Hour)},
		Removed: service.DiffSide{Files: 3, Dirs: 1, Bytes: 2048, Paths: []service.DiffPath{{Path: "/big/", Entries: 4}}, More: 2},
		Added:   service.DiffSide{Files: 1, Paths: []service.DiffPath{{Path: "/new.txt", Entries: 1}}},
		Changed: 7, ChangedPaths: []string{"/docs/d.txt"},
	}
	text := strings.Join(present.DiffLines(d), "\n")
	for _, want := range []string{"From snapshot aaaaaaaa", "to snapshot bbbbbbbb", "removed  3 files, 1 directory (2.0 KiB in the repository)", "added    1 file, 0 directories",
		"changed  7 files", "Removed:\n  /big/  (4 entries)\n  … and 2 more", "Added:\n  /new.txt\n", "Changed:\n  /docs/d.txt\n  … and 6 more"} {
		if !strings.Contains(text, want) {
			t.Errorf("the text lacks %q:\n%s", want, text)
		}
	}
	same := strings.Join(present.DiffLines(service.DiffResult{}), "\n")
	if !strings.Contains(same, "hold the same files") {
		t.Errorf("no difference:\n%s", same)
	}
}

func TestPreflightRowsNameTheIncludedPaths(t *testing.T) {
	p := service.RestorePreflight{Target: "/data", InPlace: true, Includes: []string{"/docs/a.md", "/etc/app"}}
	var text []string
	for _, r := range present.PreflightRows(p, time.Now()) {
		text = append(text, r.Label+"|"+r.Text)
	}
	if got := strings.Join(text, "\n"); !strings.Contains(got, "Target|/data (in place, replaces the current data)\nOnly|/docs/a.md\n|/etc/app") {
		t.Errorf("rows:\n%s", got)
	}
}

func TestPreflightRowsSayTheExtraSpace(t *testing.T) {
	space := func(need, free int64) string {
		for _, r := range present.PreflightRows(service.RestorePreflight{Target: "/data", NeedBytes: need, FreeBytes: free}, time.Now()) {
			if r.Label == "Space" {
				return r.Text
			}
		}
		return ""
	}
	if got := space(0, 3<<30); got != "no extra space needed, 3.0 GiB free" {
		t.Errorf("space = %q", got)
	}
	if got := space(2<<20, 3<<30); got != "about 2.0 MiB more needed, 3.0 GiB free" {
		t.Errorf("space = %q", got)
	}
}

func TestPreflightRowsShowTheChanges(t *testing.T) {
	p := service.RestorePreflight{Target: "/mnt", InPlace: true, Changes: &service.RestoreChanges{
		Created: 7, Overwritten: 3, Deleted: 1, Unchanged: 2, OverwrittenPaths: []string{"/docs/a", "/docs/b"}, DeletedPaths: []string{"/mnt/new.txt"},
	}}
	var text []string
	for _, r := range present.PreflightRows(p, time.Now()) {
		text = append(text, r.Label+"|"+r.Text)
	}
	got := strings.Join(text, "\n")
	for _, want := range []string{"Changes|3 overwritten, 1 deleted, 7 created, 2 unchanged", "Deletes|/mnt/new.txt", "Overwrites|/docs/a\n|/docs/b\n|… and 1 more"} {
		if !strings.Contains(got, want) {
			t.Errorf("the rows lack %q:\n%s", want, got)
		}
	}
}
