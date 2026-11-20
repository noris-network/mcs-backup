package tui

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/noris-network/mcs-backup/v2/internal/service"
)

// tree is a small snapshot to browse.
func tree(r *rig) *rig {
	r.fake.FilesFn = func(_ context.Context, req service.FilesRequest) (service.FileList, error) {
		list := service.FileList{Snapshot: snap(strings.TrimRight(req.Snapshot, "0"), time.Hour), Path: path.Join("/", req.Path)}
		switch list.Path {
		case "/":
			list.Files = []service.File{{Path: "/my docs", Type: "dir", Mtime: t0}, {Path: "/readme.txt", Type: "file", Size: 2048, Mtime: t0}}
		case "/my docs":
			list.Files = []service.File{{Path: "/my docs/a b.txt", Type: "file", Size: 3 << 20, Mtime: t0}}
		default:
			return list, errors.New("snapshot cccccccc has no " + list.Path)
		}
		return list, nil
	}
	return r
}

func TestBrowseASnapshotAndRestoreAFile(t *testing.T) {
	r := tree(healthy(newRig(t))).load()
	r.press("2", "o") // the files of the newest snapshot
	r.wantScreen("Files in snapshot cccccccc", "> my docs/", "readme.txt", "2.0 KiB")
	if req := r.fake.CallsTo("Files")[0].Args[0].(service.FilesRequest); req.Snapshot != "cccccccc" || req.Path != "" {
		t.Errorf("request = %+v", req)
	}

	r.press("down", "enter") // a file does not open
	if n := len(r.fake.CallsTo("Files")); n != 1 {
		t.Errorf("%d listings after enter on a file", n)
	}
	r.press("up", "enter")
	r.wantScreen("/my docs", "> a b.txt", "3.0 MiB")
	r.press("left")
	r.wantScreen("> my docs/")
	r.press("left") // the top has no parent
	if n := len(r.fake.CallsTo("Files")); n != 3 {
		t.Errorf("%d listings, want 3", n)
	}

	// restore the file: the form is filled, and the path with a space stays one path
	r.press("enter", "r")
	r.wantScreen("Restore", "cccccccc", "/my docs/a b.txt")
	r.fake.RestorePreflightFn = func(_ context.Context, req service.RestoreRequest) (service.RestorePreflight, error) {
		return service.RestorePreflight{Snapshot: snap("cccccccc", time.Hour), Target: "/data", InPlace: true, Mode: req.Mode}, nil
	}
	r.press("enter")
	req := r.fake.CallsTo("RestorePreflight")[0].Args[0].(service.RestoreRequest)
	if req.Snapshot != "cccccccc" || len(req.Includes) != 1 || req.Includes[0] != "/my docs/a b.txt" {
		t.Errorf("preflight request = %+v", req)
	}
}

func TestFindAFileInAllSnapshots(t *testing.T) {
	r := tree(healthy(newRig(t))).load()
	newest := service.SnapshotRef{ID: "bbbbbbbb" + strings.Repeat("0", 56), Time: t0.Add(-24 * time.Hour)}
	r.fake.FindFn = func(_ context.Context, req service.FindRequest) (service.FindResult, error) {
		if req.Pattern == "*" {
			return service.FindResult{}, errors.New("the pattern matches too many files: narrow it or search a single snapshot")
		}
		return service.FindResult{Searched: 3, Versions: []service.FileVersion{
			{Path: "/my docs/a b.txt", Type: "file", Size: 3 << 20, Mtime: t0, Newest: newest, Oldest: newest, Snapshots: 2},
		}}, nil
	}
	r.press("2", "o", "/")
	r.wantScreen("Find in all snapshots")
	r.typeText("q") // typing goes into the pattern, it does not close the dialog
	r.press("backspace")
	r.typeText("*")
	r.press("enter")
	r.wantScreen("matches too many files", "my docs/") // the directory stays

	r.press("/", "backspace")
	r.typeText("a b.txt")
	r.press("enter")
	r.wantScreen("Files matching a b.txt in 3 snapshots", "> /my docs/a b.txt", "in 2, newest bbbbbbbb")
	if req := r.fake.CallsTo("Find")[1].Args[0].(service.FindRequest); req.Pattern != "a b.txt" || req.Snapshot != "" || !req.IgnoreCase {
		t.Errorf("request = %+v", req)
	}

	// restore from the newest snapshot that has this state of the file
	r.press("r")
	r.wantScreen("Restore", "bbbbbbbb", "/my docs/a b.txt")
	r.press("esc")

	// enter shows the file where it lies in that snapshot, esc leaves the search
	r.press("2", "o", "/")
	r.typeText("a b.txt")
	r.press("enter", "enter")
	r.wantScreen("Files in snapshot bbbbbbbb", "/my docs", "> a b.txt")
	r.press("/")
	r.typeText("x")
	r.press("enter", "esc")
	r.wantScreen("Files in snapshot bbbbbbbb")
	r.press("esc")
	if r.m.dlg != nil {
		t.Error("esc does not close the file browser")
	}

	// the sizes stand under each other, also with counts of two widths
	r.fake.FindFn = func(context.Context, service.FindRequest) (service.FindResult, error) {
		return service.FindResult{Searched: 12, Versions: []service.FileVersion{
			{Path: "/a.jpg", Type: "file", Size: 7 << 20, Mtime: t0, Newest: newest, Oldest: newest, Snapshots: 11},
			{Path: "/b.jpg", Type: "file", Size: 20 << 20, Mtime: t0, Newest: newest, Oldest: newest, Snapshots: 9},
		}}, nil
	}
	r.press("2", "o", "/")
	r.typeText("*.jpg")
	r.press("enter")
	end := func(text string) int {
		t.Helper()
		for l := range strings.SplitSeq(r.screen(), "\n") {
			if before, _, ok := strings.Cut(l, text); ok {
				return len([]rune(before)) + len([]rune(text))
			}
		}
		t.Fatalf("%q is not on the screen:\n%s", text, r.screen())
		return 0
	}
	if a, b := end("7.0 MiB"), end("20.0 MiB"); a != b {
		t.Errorf("the sizes end in columns %d and %d:\n%s", a, b, r.screen())
	}
}

func TestFileBrowserShowsWhatIsWrong(t *testing.T) {
	r := healthy(newRig(t)).load()
	r.fake.FilesFn = func(context.Context, service.FilesRequest) (service.FileList, error) {
		return service.FileList{}, errors.New("repository not reachable")
	}
	r.press("2", "o")
	r.wantScreen("Files", "repository not reachable")
	r.press("down", "enter", "r", "left") // nothing to act on
	r.press("esc")
	if r.m.dlg != nil {
		t.Error("the dialog stays open")
	}

	r.fake.FilesFn = func(context.Context, service.FilesRequest) (service.FileList, error) {
		return service.FileList{Snapshot: snap("cccccccc", time.Hour), Path: "/", Truncated: true, Files: []service.File{{Path: "/a", Type: "file"}}}, nil
	}
	r.press("o")
	r.wantScreen("Only the first 1 entries")
}

func TestWhatChangedInASnapshot(t *testing.T) {
	r := healthy(newRig(t)).load()
	r.fake.DiffFn = func(_ context.Context, req service.DiffRequest) (service.DiffResult, error) {
		if !strings.HasPrefix(req.To, "cccccccc") {
			return service.DiffResult{}, errors.New("there is no regular snapshot before aaaaaaaa to compare it with")
		}
		return service.DiffResult{From: snap("bbbbbbbb", 24*time.Hour), To: snap("cccccccc", time.Hour),
			Removed: service.DiffSide{Files: 3, Dirs: 1, Paths: []service.DiffPath{{Path: "/big/", Entries: 4}}}}, nil
	}
	r.press("2", "d")
	r.wantScreen("What changed", "From snapshot bbbbbbbb", "/big/  (4 entries)")
	r.press("esc", "down", "down", "d") // the oldest snapshot
	r.wantScreen("no regular snapshot before aaaaaaaa")
	if r.m.dlg != nil {
		t.Error("a failed comparison opens a dialog")
	}
}

func TestRestoreDialogShowsWhatWouldChange(t *testing.T) {
	r := healthy(newRig(t)).load()
	r.fake.RestorePreflightFn = func(_ context.Context, req service.RestoreRequest) (service.RestorePreflight, error) {
		p := service.RestorePreflight{Snapshot: snap("cccccccc", time.Hour), Target: "/data", InPlace: true, Mode: req.Mode}
		if req.Preview {
			p.Changes = &service.RestoreChanges{Overwritten: 3, Deleted: 1, DeletedPaths: []string{"/data/new.txt"}}
		}
		return p, nil
	}
	r.press("2", "enter", "enter")
	r.wantScreen("p what would change")
	r.wantNoScreen("overwritten")
	r.press("p")
	r.wantScreen("3 overwritten, 1 deleted", "/data/new.txt")
	r.press("p") // once is enough
	if n := len(r.fake.CallsTo("RestorePreflight")); n != 2 {
		t.Errorf("%d preflights, want 2", n)
	}
	r.press("enter")
	r.wantScreen("Type cccccccc to confirm")
}

func TestFoundPathsKeepTheirFileName(t *testing.T) {
	r := tree(healthy(newRig(t))).load()
	r.m.width = 80
	long := "/var/lib/postgresql/data/base/16384/archive/2026/november/exports/report-2026-final.sql"
	newest := service.SnapshotRef{ID: "bbbbbbbb" + strings.Repeat("0", 56), Time: t0.Add(-24 * time.Hour)}
	r.fake.FindFn = func(context.Context, service.FindRequest) (service.FindResult, error) {
		return service.FindResult{Searched: 3, Versions: []service.FileVersion{
			{Path: long, Type: "file", Size: 3 << 20, Mtime: t0, Newest: newest, Oldest: newest, Snapshots: 12},
		}}, nil
	}
	r.press("2", "o", "/")
	r.typeText("report*")
	r.press("enter")
	r.wantScreen("…", "exports/report-2026-final.sql", "in 12, newest bbbbbbbb")

	// a wide terminal gives the list the room
	r.m.width = 200
	r.wantScreen(long)
	for l := range strings.SplitSeq(r.screen(), "\n") {
		if strings.HasPrefix(l, "╭") && len([]rune(l)) <= formWidth {
			t.Errorf("the file browser stays %d wide on a terminal of 200", len([]rune(l)))
		}
	}
}

func TestTheCursorLandsWhereTheUserCameFrom(t *testing.T) {
	r := healthy(newRig(t)).load()
	r.fake.FilesFn = func(_ context.Context, req service.FilesRequest) (service.FileList, error) {
		list := service.FileList{Snapshot: snap("cccccccc", time.Hour), Path: path.Join("/", req.Path)}
		switch list.Path {
		case "/":
			for _, n := range []string{"aaa", "mmm", "zzz"} {
				list.Files = append(list.Files, service.File{Path: "/" + n, Type: "dir", Mtime: t0})
			}
		case "/zzz":
			for _, n := range []string{"a.txt", "b.txt", "c.txt"} {
				list.Files = append(list.Files, service.File{Path: "/zzz/" + n, Type: "file", Size: 1, Mtime: t0})
			}
		}
		return list, nil
	}
	r.press("2", "o", "down", "down", "enter")
	r.wantScreen("/zzz", "> a.txt")
	r.press("left")
	r.wantScreen("> zzz/") // back on the directory it came out of

	r.fake.FindFn = func(context.Context, service.FindRequest) (service.FindResult, error) {
		return service.FindResult{Searched: 1, Versions: []service.FileVersion{{Path: "/zzz/c.txt", Type: "file", Size: 1, Mtime: t0, Newest: service.SnapshotRef{ID: "cccccccc" + strings.Repeat("0", 56), Time: t0}, Snapshots: 1}}}, nil
	}
	r.press("/")
	r.typeText("c.txt")
	r.press("enter", "enter")
	r.wantScreen("/zzz", "> c.txt") // the found file, not the top of its directory
}

func TestListsShowWhereTheCursorIs(t *testing.T) {
	r := healthy(newRig(t)).load()
	r.m.height = 24
	r.fake.FilesFn = func(context.Context, service.FilesRequest) (service.FileList, error) {
		list := service.FileList{Snapshot: snap("cccccccc", time.Hour), Path: "/"}
		for i := range 50 {
			list.Files = append(list.Files, service.File{Path: fmt.Sprintf("/f%02d", i), Type: "file", Size: 1, Mtime: t0})
		}
		return list, nil
	}
	r.press("2", "o", "down")
	r.wantScreen("2 of 50 ·")
	r.press("esc")

	var many []service.Snapshot
	for i := range 40 {
		many = append(many, snap(fmt.Sprintf("%08d", i), time.Duration(i+1)*time.Hour))
	}
	r.fake.SnapshotsFn = func(context.Context) ([]service.Snapshot, error) { return many, nil }
	r.press("r", "end")
	if r.m.cursor[tabSnapshots] != 39 {
		t.Errorf("end: cursor %d", r.m.cursor[tabSnapshots])
	}
	r.screen() // drawn, as Bubble Tea does after every key
	r.press("home", "pgdown")
	if c := r.m.cursor[tabSnapshots]; c != r.m.listRows || c < 5 {
		t.Errorf("pgdown: cursor %d, %d rows in view", c, r.m.listRows)
	}
}
