package service

import (
	"encoding/json/v2"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/noris-network/mcs-backup/v2/internal/restic"
)

func TestResultWorse(t *testing.T) {
	order := []Result{ResultSkipped, ResultSuccess, ResultWarning, ResultSuspicious, ResultCancelled, ResultTimeout, ResultFailure}
	for i, a := range order {
		for j, b := range order {
			want := order[max(i, j)]
			if got := a.Worse(b); got != want {
				t.Errorf("%s.Worse(%s) = %s, want %s", a, b, got, want)
			}
		}
	}
	for _, r := range []Result{ResultSuccess, ResultSkipped, ResultWarning} {
		if !r.OK() {
			t.Errorf("%s should be OK", r)
		}
	}
	for _, r := range []Result{ResultFailure, ResultSuspicious, ResultTimeout, ResultCancelled} {
		if r.OK() {
			t.Errorf("%s should not be OK", r)
		}
	}
}

func TestNoteTag(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", ""},
		{"   ", ""},
		{"before upgrade", "note:before upgrade"},
		{"a, b,c", "note:a; b;c"},
		{"line1\nline2\ttab", "note:line1 line2 tab"},
		{"  spaced   out  ", "note:spaced out"},
		{strings.Repeat("ä", 300), "note:" + strings.Repeat("ä", 200)},
	}
	for _, tt := range tests {
		if got := NoteTag(tt.in); got != tt.want {
			t.Errorf("NoteTag(%q) = %q, want %q", tt.in, got, tt.want)
		}
		if strings.Contains(NoteTag(tt.in), ",") {
			t.Errorf("NoteTag(%q) contains a comma", tt.in)
		}
	}
}

func TestTagHelpers(t *testing.T) {
	tags := []string{"mcs-backup", "scheduled", "keep", "note:why"}
	if NoteOf(tags) != "why" || OriginOf(tags) != OriginScheduled {
		t.Errorf("note/origin = %q/%q", NoteOf(tags), OriginOf(tags))
	}
	if NoteOf([]string{"mcs-backup"}) != "" || OriginOf([]string{"mcs-backup"}) != "" {
		t.Error("expected empty note and origin")
	}
	for tags, want := range map[string]bool{
		"mcs-backup,manual": true, "mcs-backup,keep": true, "suspicious": false, "pre-restore,mcs-backup": false, "": true,
	} {
		if got := IsRegular(strings.Split(tags, ",")); got != want {
			t.Errorf("IsRegular(%q) = %v, want %v", tags, got, want)
		}
	}
}

func snap(id, host string, age time.Duration, tags ...string) restic.Snapshot {
	return restic.Snapshot{ID: id, Hostname: host, Time: time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC).Add(-age), Tags: tags}
}

func TestSnapshotSelection(t *testing.T) {
	snaps := []restic.Snapshot{
		snap("new", "me", 0),
		snap("old", "me", 48*time.Hour),
		snap("legacy", LegacyHost, 72*time.Hour),
		snap("foreign", "other", time.Hour),
		snap("bad", "me", time.Hour, TagSuspicious),
		snap("safety", "me", 2*time.Hour, TagPreRestore),
	}
	hosts := OwnHosts("me", snaps)
	if !slices.Equal(hosts, []string{"me", LegacyHost}) {
		t.Errorf("OwnHosts = %q", hosts)
	}
	if got := OwnHosts("me", snaps[:2]); !slices.Equal(got, []string{"me"}) {
		t.Errorf("OwnHosts without legacy snapshots = %q", got)
	}
	if got := OwnHosts(LegacyHost, snaps); !slices.Equal(got, []string{LegacyHost}) {
		t.Errorf("OwnHosts(legacy) = %q", got)
	}

	own := Own(snaps, hosts)
	ids := func(s []restic.Snapshot) []string {
		var out []string
		for _, x := range s {
			out = append(out, x.ID)
		}
		return out
	}
	if got := ids(own); !slices.Equal(got, []string{"legacy", "old", "safety", "bad", "new"}) {
		t.Errorf("Own = %q (foreign hosts excluded, oldest first)", got)
	}
	if got := ids(Regular(own)); !slices.Equal(got, []string{"legacy", "old", "new"}) {
		t.Errorf("Regular = %q", got)
	}
	if l, ok := Latest(Regular(own)); !ok || l.ID != "new" {
		t.Errorf("Latest = %v, %v", l.ID, ok)
	}
	if _, ok := Latest(nil); ok {
		t.Error("Latest(nil) found a snapshot")
	}
}

func TestFromRestic(t *testing.T) {
	s := FromRestic(restic.Snapshot{
		ID: "0123456789abcdef", Hostname: "me", Tags: []string{"mcs-backup", "manual", "keep", "note:x"}, Original: "orig",
		Summary: &restic.SnapshotSummary{TotalFilesProcessed: 3, TotalBytesProcessed: 14, DataAdded: 5},
	})
	if s.ShortID != "01234567" || s.Origin != OriginManual || s.Note != "x" || !s.Keep || s.Suspicious || s.PreRestore ||
		!s.HasSummary || s.Files != 3 || s.Bytes != 14 || s.AddedBytes != 5 {
		t.Errorf("FromRestic = %+v", s)
	}
	if FromRestic(restic.Snapshot{ID: "x"}).HasSummary {
		t.Error("a snapshot without summary must not claim one")
	}
}

func TestDurationJSON(t *testing.T) {
	type wrap struct {
		D Duration `json:"d"`
	}
	data, err := json.Marshal(wrap{D: Duration(90 * time.Minute)})
	if err != nil || string(data) != `{"d":"1h30m0s"}` {
		t.Fatalf("Marshal() = %s, %v", data, err)
	}
	for in, want := range map[string]time.Duration{`{"d":"2h"}`: 2 * time.Hour, `{"d":"1h30m0s"}`: 90 * time.Minute, `{"d":"250ms"}`: 250 * time.Millisecond} {
		var w wrap
		if err := json.Unmarshal([]byte(in), &w); err != nil || w.D.Std() != want {
			t.Errorf("Unmarshal(%s) = %v, %v; want %v", in, w.D, err, want)
		}
	}
	var w wrap
	if err := json.Unmarshal([]byte(`{"d":"soon"}`), &w); err == nil {
		t.Error("an invalid duration was accepted")
	}
}
