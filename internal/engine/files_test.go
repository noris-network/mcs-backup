package engine

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/noris-network/mcs-backup/v2/internal/restic"
	"github.com/noris-network/mcs-backup/v2/internal/resticfake"
	"github.com/noris-network/mcs-backup/v2/internal/service"
)

func dir(path string) restic.Node { return restic.Node{Path: path, Type: "dir"} }

func TestFilesListsADirectory(t *testing.T) {
	r := restoreRig(t)
	r.main.On(t, "ls", resticfake.Response{Stdout: lsOutput(dir("/docs"), file("/docs/a.txt", 3), dir("/docs/sub"))})

	// a relative path counts from the top, the default snapshot is the latest
	list, err := r.e.Files(t.Context(), service.FilesRequest{Path: "docs"})
	if err != nil {
		t.Fatal(err)
	}
	if list.Snapshot.ID != "snap1" || list.Path != "/docs" {
		t.Errorf("list = %+v", list)
	}
	if len(list.Files) != 2 || list.Files[0].Path != "/docs/a.txt" || list.Files[0].Size != 3 || list.Files[1].Type != "dir" {
		t.Errorf("files = %+v: the directory itself is not an entry", list.Files)
	}
	if got := r.main.CallsTo(t, "ls")[0]; !slices.Equal(got, []string{"ls", "--json", "--no-lock", "--", "snap1", "/docs"}) {
		t.Errorf("args = %q", got)
	}

	// without a path the top of the snapshot is listed, which may be empty
	r.main.On(t, "ls", resticfake.Response{Stdout: lsOutput(dir("/docs"))})
	list, err = r.e.Files(t.Context(), service.FilesRequest{Snapshot: "snap"})
	if err != nil || list.Path != "/" || len(list.Files) != 1 || list.Files[0].Path != "/docs" {
		t.Errorf("top = %+v, %v", list, err)
	}
	r.main.On(t, "ls", resticfake.Response{Stdout: lsOutput()})
	if list, err = r.e.Files(t.Context(), service.FilesRequest{}); err != nil || len(list.Files) != 0 {
		t.Errorf("empty top = %+v, %v", list, err)
	}
}

func TestFilesTakesThePathInTheFileSystemToo(t *testing.T) {
	r := restoreRig(t)
	// restic has nothing under BACKUP_ROOT/docs, the snapshot has it as /docs
	r.main.On(t, "ls", resticfake.Response{Stdout: lsOutput()},
		resticfake.Response{Stdout: lsOutput(dir("/docs"), file("/docs/a.txt", 3))})
	list, err := r.e.Files(t.Context(), service.FilesRequest{Path: filepath.Join(r.root, "docs")})
	if err != nil || list.Path != "/docs" || len(list.Files) != 1 {
		t.Fatalf("list = %+v, %v", list, err)
	}

	// a path outside of the root is only tried as it is
	r.main.On(t, "ls", resticfake.Response{Stdout: lsOutput(file("/etc/app.conf", 9))})
	list, err = r.e.Files(t.Context(), service.FilesRequest{Path: "/etc/app.conf"})
	if err != nil || len(list.Files) != 1 || list.Files[0].Type != "file" {
		t.Errorf("a file lists itself: %+v, %v", list, err)
	}
	calls := len(r.main.CallsTo(t, "ls"))
	r.main.On(t, "ls", resticfake.Response{Stdout: lsOutput()})
	if _, err := r.e.Files(t.Context(), service.FilesRequest{Path: "/nosuch"}); err == nil || !strings.Contains(err.Error(), "has no /nosuch") {
		t.Errorf("err = %v", err)
	}
	if n := len(r.main.CallsTo(t, "ls")) - calls; n != 1 {
		t.Errorf("restic ls ran %d times for a path outside of the root", n)
	}
	if _, err := r.e.Files(t.Context(), service.FilesRequest{Snapshot: "zzz"}); err == nil {
		t.Error("an unknown snapshot is an error")
	}
}

func TestFilesAreLimited(t *testing.T) {
	r := restoreRig(t)
	nodes := []restic.Node{dir("/big")}
	for i := range maxFiles + 10 {
		nodes = append(nodes, file(fmt.Sprintf("/big/%05d", i), 1))
	}
	r.main.On(t, "ls", resticfake.Response{Stdout: lsOutput(nodes...)})
	list, err := r.e.Files(t.Context(), service.FilesRequest{Path: "/big"})
	if err != nil || len(list.Files) != maxFiles || !list.Truncated {
		t.Errorf("%d files, truncated %v, %v", len(list.Files), list.Truncated, err)
	}
}

func TestFindMergesTheSnapshotsIntoVersions(t *testing.T) {
	r := newRig(t, false)
	old := snap("old", "me", 72*time.Hour, 3, 100, rootPaths(r), "mcs-backup")
	mid := snap("mid", "me", 48*time.Hour, 3, 100, rootPaths(r), "mcs-backup")
	new1 := snap("new", "me", time.Hour, 3, 100, rootPaths(r), "mcs-backup")
	other := snap("other", "someone-else", time.Hour, 3, 100, rootPaths(r))
	r.main.On(t, "snapshots", resticfake.Response{Stdout: snapsJSON(t, old, mid, new1, other)})

	t1, t2 := testNow.Add(-100*time.Hour), testNow.Add(-50*time.Hour)
	v1 := restic.Node{Path: "/d/a.txt", Type: "file", Size: 10, Mtime: t1}
	v2 := restic.Node{Path: "/d/a.txt", Type: "file", Size: 12, Mtime: t2}
	gone := restic.Node{Path: "/d/gone.txt", Type: "file", Size: 1, Mtime: t1}
	groups := []restic.FindGroup{
		{Snapshot: "new", Matches: []restic.Node{v2}},
		{Snapshot: "mid", Matches: []restic.Node{v1, gone}},
		{Snapshot: "old", Matches: []restic.Node{v1, gone}},
		{Snapshot: "unknown", Matches: []restic.Node{v1}},
	}
	r.main.On(t, "find", resticfake.Response{Stdout: findJSON(t, groups)})

	res, err := r.e.Find(t.Context(), service.FindRequest{Pattern: "*.txt", IgnoreCase: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Searched != 3 || len(res.Versions) != 3 {
		t.Fatalf("result = %+v", res)
	}
	// sorted by path, the newest version first
	a2, a1, g := res.Versions[0], res.Versions[1], res.Versions[2]
	if a2.Size != 12 || a2.Newest.ID != "new" || a2.Oldest.ID != "new" || a2.Snapshots != 1 {
		t.Errorf("current version = %+v", a2)
	}
	if a1.Size != 10 || a1.Newest.ID != "mid" || a1.Oldest.ID != "old" || a1.Snapshots != 2 || !a1.Newest.Time.Equal(mid.Time) {
		t.Errorf("previous version = %+v", a1)
	}
	if g.Path != "/d/gone.txt" || g.Newest.ID != "mid" {
		t.Errorf("deleted file = %+v: its newest snapshot is the one to restore it from", g)
	}
	// only the snapshots of this instance are searched
	want := []string{"find", "--json", "--no-lock", "--ignore-case", "--host", "me", "--", "*.txt"}
	if got := r.main.CallsTo(t, "find")[0]; !slices.Equal(got, want) {
		t.Errorf("args = %q", got)
	}

	res, err = r.e.Find(t.Context(), service.FindRequest{Pattern: "a.txt", Snapshot: "mi"})
	if err != nil || res.Searched != 1 {
		t.Errorf("one snapshot: %+v, %v", res, err)
	}
	if got := r.main.CallsTo(t, "find")[1]; !slices.Contains(got, "--snapshot") || !slices.Contains(got, "mid") {
		t.Errorf("args = %q", got)
	}
}

func TestFindRefusesWhatItCannotAnswer(t *testing.T) {
	r := restoreRig(t)
	if _, err := r.e.Find(t.Context(), service.FindRequest{}); err == nil {
		t.Error("a search without a pattern is an error")
	}
	if len(r.main.CallsTo(t, "find")) != 0 {
		t.Error("restic ran without a pattern")
	}
	r.main.On(t, "find", resticfake.Response{Stdout: strings.Repeat(" ", 17<<20)})
	if _, err := r.e.Find(t.Context(), service.FindRequest{Pattern: "*"}); err == nil || !strings.Contains(err.Error(), "narrow it") {
		t.Errorf("err = %v", err)
	}
}
