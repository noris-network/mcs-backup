//go:build integration

package integration

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/noris-network/mcs-backup/v2/internal/service"
)

// A file that was deleted is found in the snapshots that still have it, and
// can be restored from there by its path.
func TestFindAndRestoreADeletedFile(t *testing.T) {
	i := newInst(t)
	i.write("docs/a.txt", "first")
	i.write("docs/sub/b.txt", "b")
	i.write("-odd/c.txt", "c")
	i.start()
	first := i.mustBackup("", false)
	i.write("docs/a.txt", "second, longer")
	second := i.mustBackup("", false)
	i.remove("docs/a.txt")
	i.write("docs/new.txt", "as large as what was removed, so the backup does not look shrunk")
	third := i.mustBackup("", false)

	list, err := i.d.Files(i.ctx(), service.FilesRequest{Path: "docs"})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range list.Files {
		names = append(names, filepath.Base(f.Path)+":"+f.Type)
	}
	if list.Snapshot.ID != third.Snapshot || !slices.Equal(names, []string{"new.txt:file", "sub:dir"}) {
		t.Errorf("latest docs = %q of snapshot %s", names, list.Snapshot.ShortID)
	}
	// the top of a snapshot, and a directory whose name looks like a flag
	if top, err := i.d.Files(i.ctx(), service.FilesRequest{Snapshot: first.Snapshot}); err != nil || len(top.Files) != 2 {
		t.Errorf("top = %+v, %v", top.Files, err)
	}
	if odd, err := i.d.Files(i.ctx(), service.FilesRequest{Path: "-odd"}); err != nil || len(odd.Files) != 1 {
		t.Errorf("-odd = %+v, %v", odd.Files, err)
	}
	if abs, err := i.d.Files(i.ctx(), service.FilesRequest{Path: filepath.Join(i.root, "docs", "sub")}); err != nil || len(abs.Files) != 1 || abs.Path != "/docs/sub" {
		t.Errorf("the path in the file system: %+v, %v", abs, err)
	}
	if _, err := i.d.Files(i.ctx(), service.FilesRequest{Path: "nosuch"}); err == nil {
		t.Error("a path the snapshot does not have is an error")
	}

	res, err := i.d.Find(i.ctx(), service.FindRequest{Pattern: "A.TXT", IgnoreCase: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Searched != 3 || len(res.Versions) != 2 {
		t.Fatalf("find = %+v", res)
	}
	newer, older := res.Versions[0], res.Versions[1]
	if newer.Newest.ID != second.Snapshot || newer.Size != int64(len("second, longer")) || newer.Snapshots != 1 {
		t.Errorf("newer version = %+v", newer)
	}
	if older.Newest.ID != first.Snapshot || older.Size != int64(len("first")) {
		t.Errorf("older version = %+v", older)
	}
	if none, err := i.d.Find(i.ctx(), service.FindRequest{Pattern: "a.txt", Snapshot: "latest"}); err != nil || len(none.Versions) != 0 || none.Searched != 1 {
		t.Errorf("the latest snapshot no longer has the file: %+v, %v", none, err)
	}
	if all, err := i.d.Find(i.ctx(), service.FindRequest{Pattern: "*.txt"}); err != nil || len(all.Versions) != 5 {
		t.Errorf("*.txt = %+v, %v", all.Versions, err)
	}

	// the path that find reports is what restore --include takes
	r := i.restore(service.RestoreRequest{Snapshot: newer.Newest.ID, Confirm: newer.Newest.ID, Includes: []string{newer.Path}, Mode: service.ModeMerge})
	if !r.Result.OK() {
		t.Fatalf("restore: %s %s", r.Result, r.Error)
	}
	if got := i.read("docs/a.txt"); got != "second, longer" {
		t.Errorf("restored content = %q", got)
	}
	if !i.exists("docs/new.txt") {
		t.Error("a merge restore of one file removed another")
	}
}

// A backup that lost a directory is flagged, and the comparison names what is gone.
func TestDiffNamesWhatASuspiciousBackupLost(t *testing.T) {
	i := newInst(t)
	i.write("big/one.bin", strings.Repeat("x", 4000))
	i.write("big/sub/two.bin", strings.Repeat("y", 4000))
	i.write("docs/a.txt", "first")
	i.start()
	good := i.mustBackup("", false)
	i.remove("big")
	i.write("docs/a.txt", "second")
	i.write("docs/new.txt", "n")
	bad := i.backup("", false)
	if bad.Result != service.ResultSuspicious {
		t.Fatalf("the shrunken backup ended as %s", bad.Result)
	}

	d, err := i.d.Diff(i.ctx(), service.DiffRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if d.From.ID != good.Snapshot || !d.To.Suspicious {
		t.Errorf("compared %s with %s (suspicious %v)", d.From.ShortID, d.To.ShortID, d.To.Suspicious)
	}
	if len(d.Removed.Paths) != 1 || d.Removed.Paths[0] != (service.DiffPath{Path: "/big/", Entries: 4}) || d.Removed.Files != 2 || d.Removed.Dirs != 2 {
		t.Errorf("removed = %+v", d.Removed)
	}
	if len(d.Added.Paths) != 1 || d.Added.Paths[0].Path != "/docs/new.txt" {
		t.Errorf("added = %+v", d.Added)
	}
	if d.Changed != 1 || !slices.Equal(d.ChangedPaths, []string{"/docs/a.txt"}) {
		t.Errorf("changed = %d %q", d.Changed, d.ChangedPaths)
	}
}

// The preview of a restore counts what it would do and changes nothing.
func TestRestorePreviewChangesNothing(t *testing.T) {
	i := newInst(t)
	i.write("docs/a.txt", "first")
	i.write("docs/keep.txt", "same")
	i.start()
	i.mustBackup("", false)
	i.write("docs/a.txt", "changed since")
	i.write("docs/new.txt", "not in the snapshot")
	i.remove("docs/keep.txt")

	p, err := i.d.RestorePreflight(i.ctx(), service.RestoreRequest{Snapshot: "latest", Preview: true})
	if err != nil || p.Changes == nil {
		t.Fatalf("preflight = %+v, %v", p, err)
	}
	c := p.Changes
	if c.Overwritten != 1 || !slices.Equal(c.OverwrittenPaths, []string{"/docs/a.txt"}) {
		t.Errorf("overwritten = %d %q", c.Overwritten, c.OverwrittenPaths)
	}
	if c.Deleted != 1 || len(c.DeletedPaths) != 1 || !strings.HasSuffix(c.DeletedPaths[0], "/docs/new.txt") {
		t.Errorf("deleted = %d %q", c.Deleted, c.DeletedPaths)
	}
	// keep.txt comes back; the directories are there already
	if c.Created != 1 {
		t.Errorf("created = %d: only keep.txt would come back", c.Created)
	}
	if i.read("docs/a.txt") != "changed since" || !i.exists("docs/new.txt") || i.exists("docs/keep.txt") {
		t.Error("the preview changed the data")
	}
	if n := len(i.snapshots()); n != 1 {
		t.Errorf("%d snapshots: a preview takes no safety snapshot", n)
	}
}
