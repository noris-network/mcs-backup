//go:build integration

package integration

import (
	"path/filepath"
	"slices"
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
