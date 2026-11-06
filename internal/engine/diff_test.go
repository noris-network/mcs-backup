package engine

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/noris-network/mcs-backup/v2/internal/restic"
	"github.com/noris-network/mcs-backup/v2/internal/resticfake"
	"github.com/noris-network/mcs-backup/v2/internal/service"
)

func change(modifier, path string) string {
	return fmt.Sprintf(`{"message_type":"change","path":%q,"modifier":%q}`+"\n", path, modifier)
}

func TestDiffTellsWhatASuspiciousBackupLost(t *testing.T) {
	r := newRig(t, false)
	good := snap("good", "me", 48*time.Hour, 9, 900, rootPaths(r), "mcs-backup")
	safety := snap("safety", "me", 30*time.Hour, 9, 900, rootPaths(r), "mcs-backup", service.TagPreRestore)
	bad := snap("bad", "me", 24*time.Hour, 2, 100, rootPaths(r), "mcs-backup", service.TagSuspicious)
	later := snap("later", "me", time.Minute, 9, 900, rootPaths(r), "mcs-backup", service.TagPreRestore)
	r.main.On(t, "snapshots", resticfake.Response{Stdout: snapsJSON(t, good, safety, bad, later)})
	r.main.On(t, "diff", resticfake.Response{Stdout: change("-", "/big/") + change("-", "/big/1") + change("-", "/big/x/") + change("-", "/big/x/2") +
		change("-", "/bigger.bin") + change("M", "/docs/") + change("M", "/docs/d.txt") + change("U", "/docs/meta") + change("+", "/new.txt") +
		`{"message_type":"statistics","changed_files":1,"added":{"files":1,"dirs":0,"bytes":10},"removed":{"files":3,"dirs":2,"bytes":2973}}` + "\n"})

	d, err := r.e.Diff(t.Context(), service.DiffRequest{})
	if err != nil {
		t.Fatal(err)
	}
	// the newest backup, suspicious or not, against the last regular one before it
	if d.From.ID != "good" || d.To.ID != "bad" {
		t.Errorf("compared %s with %s", d.From.ID, d.To.ID)
	}
	if got := r.main.CallsTo(t, "diff")[0]; !slices.Equal(got[len(got)-2:], []string{"good", "bad"}) {
		t.Errorf("args = %q", got)
	}
	// a directory that is gone is listed once, with what it stood for; the largest first
	want := []service.DiffPath{{Path: "/big/", Entries: 4}, {Path: "/bigger.bin", Entries: 1}}
	if !slices.Equal(d.Removed.Paths, want) || d.Removed.Files != 3 || d.Removed.Dirs != 2 || d.Removed.Bytes != 2973 {
		t.Errorf("removed = %+v", d.Removed)
	}
	if len(d.Added.Paths) != 1 || d.Added.Paths[0].Path != "/new.txt" {
		t.Errorf("added = %+v", d.Added)
	}
	if d.Changed != 1 || !slices.Equal(d.ChangedPaths, []string{"/docs/d.txt"}) {
		t.Errorf("changed = %d %q: directories and changes of metadata only are left out", d.Changed, d.ChangedPaths)
	}
}

func TestDiffPicksTheSnapshots(t *testing.T) {
	r := newRig(t, false)
	a := snap("aaa", "me", 72*time.Hour, 9, 900, rootPaths(r), "mcs-backup")
	b := snap("bbb", "me", 48*time.Hour, 9, 900, rootPaths(r), "mcs-backup")
	c := snap("ccc", "me", 24*time.Hour, 9, 900, rootPaths(r), "mcs-backup")
	r.main.On(t, "snapshots", resticfake.Response{Stdout: snapsJSON(t, a, b, c)})
	r.main.On(t, "diff", resticfake.Response{Stdout: `{"message_type":"statistics"}` + "\n"})

	for _, tt := range []struct{ to, from, wantFrom, wantTo string }{
		{"", "", "bbb", "ccc"},
		{"bb", "", "aaa", "bbb"}, // one snapshot: against the regular one before it
		{"ccc", "aaa", "aaa", "ccc"},
	} {
		d, err := r.e.Diff(t.Context(), service.DiffRequest{To: tt.to, From: tt.from})
		if err != nil || d.From.ID != tt.wantFrom || d.To.ID != tt.wantTo {
			t.Errorf("diff %q %q: %s to %s, %v", tt.to, tt.from, d.From.ID, d.To.ID, err)
		}
	}
	if _, err := r.e.Diff(t.Context(), service.DiffRequest{To: "aaa"}); err == nil || !strings.Contains(err.Error(), "no regular snapshot before") {
		t.Errorf("the oldest snapshot has nothing before it: %v", err)
	}
	if _, err := r.e.Diff(t.Context(), service.DiffRequest{To: "zzz"}); err == nil {
		t.Error("an unknown snapshot is an error")
	}
}

func TestDiffListsTheLargestPathsOnly(t *testing.T) {
	var d diffSide
	for i := range diffPaths + 30 {
		d.add(fmt.Sprintf("/f%03d", i))
	}
	d.add("/dir/")
	d.add("/dir/a")
	d.add("/dir/b/")
	d.add("/dir/b/c")
	d.add("/dirx") // not inside /dir/
	side := d.result(restic.DiffCount{})
	if len(side.Paths) != diffPaths || side.More != 32 || side.Paths[0] != (service.DiffPath{Path: "/dir/", Entries: 4}) {
		t.Errorf("%d paths, %d more, first %+v", len(side.Paths), side.More, side.Paths[0])
	}
}
