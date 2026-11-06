package restic

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/noris-network/mcs-backup/v2/internal/resticfake"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func newRepo(t *testing.T, f *resticfake.Fake, mod ...func(*Options)) *Repo {
	t.Helper()
	o := Options{Bin: f.Bin, Env: append(os.Environ(), "RESTIC_REPOSITORY=/repo", "RESTIC_PASSWORD=pw"), Dir: t.TempDir()}
	for _, m := range mod {
		m(&o)
	}
	return New(o)
}

func TestParseVersion(t *testing.T) {
	v, err := ParseVersion("restic 0.17.3 compiled with go1.23.3 on linux/amd64\n")
	if err != nil || v != (Version{0, 17, 3}) {
		t.Fatalf("ParseVersion() = %v, %v", v, err)
	}
	if !v.AtLeast(0, 17) || v.AtLeast(0, 18) || !v.AtLeast(0, 1) {
		t.Errorf("AtLeast wrong for %v", v)
	}
	if v.String() != "0.17.3" {
		t.Errorf("String() = %q", v)
	}
	if _, err := ParseVersion("something else"); err == nil {
		t.Error("expected an error for garbage")
	}
}

func TestVersionAndConfig(t *testing.T) {
	f := resticfake.New(t)
	f.On(t, "version", resticfake.Response{Stdout: "restic 0.19.1 compiled with go1.26.4 on linux/amd64\n"})
	f.On(t, "cat config", resticfake.Response{Stdout: fixture(t, "cat_config.json")})
	r := newRepo(t, f)

	v, err := r.Version(t.Context())
	if err != nil || v != (Version{0, 19, 1}) {
		t.Fatalf("Version() = %v, %v", v, err)
	}
	c, err := r.Config(t.Context())
	if err != nil || c.ID != "f77dce529410d21e367a125e22ec301c937898fc97cee1a429d94832fe99d744" || c.Version != 2 {
		t.Fatalf("Config() = %+v, %v", c, err)
	}
	if got := f.CallsTo(t, "cat")[0]; !slices.Equal(got, []string{"cat", "config", "--no-lock"}) {
		t.Errorf("args = %q", got)
	}
}

func TestExitErrors(t *testing.T) {
	tests := []struct {
		name   string
		stderr string
		exit   int
		is     error
		not    []error
		want   string
	}{
		{"repository missing", fixture(t, "err_repo_missing.txt"), 10, ErrRepoMissing, []error{ErrLocked}, "repository does not exist"},
		{"wrong password", fixture(t, "err_wrong_password.txt"), 12, ErrWrongPassword, []error{ErrRepoMissing}, "wrong password"},
		{"locked", fixture(t, "err_locked.txt"), 11, ErrLocked, []error{ErrWrongPassword}, "already locked by PID"},
		{"interrupted", "", 130, ErrInterrupted, []error{ErrLocked}, "exit code 130"},
		{"incomplete", "Warning: at least one source file could not be read", 3, ErrIncomplete, nil, "at least one source file"},
		{"other", "Fatal: boom", 1, nil, []error{ErrLocked, ErrIncomplete, ErrRepoMissing, ErrWrongPassword, ErrInterrupted}, "boom"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := resticfake.New(t)
			f.On(t, "cat config", resticfake.Response{Stderr: tt.stderr, Exit: tt.exit})
			_, err := newRepo(t, f).Config(t.Context())

			ee, ok := errors.AsType[*ExitError](err)
			if !ok || ee.Code != tt.exit || ee.Command != "cat" {
				t.Fatalf("err = %v, want ExitError with code %d", err, tt.exit)
			}
			if tt.is != nil && !errors.Is(err, tt.is) {
				t.Errorf("errors.Is(err, %v) = false", tt.is)
			}
			for _, n := range tt.not {
				if errors.Is(err, n) {
					t.Errorf("errors.Is(err, %v) = true", n)
				}
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not contain %q", err, tt.want)
			}
		})
	}
}

func TestSnapshots(t *testing.T) {
	f := resticfake.New(t)
	f.On(t, "snapshots", resticfake.Response{Stdout: fixture(t, "snapshots_copy.json")})
	snaps, err := newRepo(t, f).Snapshots(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 5 {
		t.Fatalf("got %d snapshots, want 5", len(snaps))
	}
	var copied, tagged Snapshot
	for _, s := range snaps {
		if s.Original != "" {
			copied = s
		}
		if s.HasTag("keep") {
			tagged = s
		}
	}
	if copied.Hostname != "x" || copied.Summary == nil || copied.Summary.TotalFilesProcessed != 0 {
		t.Errorf("copied snapshot = %+v", copied)
	}
	if !slices.Equal(tagged.Tags, []string{"mcs-backup", "scheduled", "keep", "note:before upgrade"}) {
		t.Errorf("tags = %q", tagged.Tags)
	}
	if s := tagged.Summary; s == nil || s.TotalBytesProcessed != 14 || s.TotalFilesProcessed != 3 || s.FilesNew != 1 {
		t.Errorf("summary = %+v", s)
	}
	if want := []string{"--json", "--no-lock"}; !slices.Equal(f.Calls(t)[0][1:], want) {
		t.Errorf("args = %q", f.Calls(t)[0])
	}

	f.On(t, "snapshots", resticfake.Response{Stdout: "[]\n"})
	if snaps, err := newRepo(t, f).Snapshots(t.Context()); err != nil || len(snaps) != 0 {
		t.Errorf("empty repository: %v, %v", snaps, err)
	}
	f.On(t, "snapshots", resticfake.Response{Stdout: "not json"})
	if _, err := newRepo(t, f).Snapshots(t.Context()); err == nil || !strings.Contains(err.Error(), "unexpected output") {
		t.Errorf("garbage output: %v", err)
	}
}

func TestBackup(t *testing.T) {
	f := resticfake.New(t)
	f.On(t, "backup", resticfake.Response{Stdout: fixture(t, "backup_status.jsonl") + fixture(t, "backup.jsonl")})
	r := newRepo(t, f)

	var progress []BackupStatus
	sum, err := r.Backup(t.Context(), BackupOptions{
		Host:     "mcs-backup",
		Tags:     []string{"mcs-backup", "manual", "note:two words"},
		Parent:   "abc123",
		Excludes: []string{"*.tmp", "cache"},
		Paths:    []string{"."},
		Extra:    []string{"--one-file-system"},
	}, func(s BackupStatus) { progress = append(progress, s) })
	if err != nil {
		t.Fatal(err)
	}
	if sum.SnapshotID != "3339aef0960517f33a43e55bbc365e377a9eac162e336944ba1546b53f4d71f5" ||
		sum.TotalFilesProcessed != 2 || sum.TotalBytesProcessed != 8 || sum.FilesNew != 2 || sum.DataAdded != 1494 {
		t.Errorf("summary = %+v", sum)
	}
	if sum.BackupEnd.Sub(sum.BackupStart) < 600*time.Millisecond {
		t.Errorf("times = %v .. %v", sum.BackupStart, sum.BackupEnd)
	}
	if len(progress) != 2 || progress[0].PercentDone != 0.25 || progress[0].FilesDone != 10 ||
		!slices.Equal(progress[0].CurrentFiles, []string{"/data/a/big.bin"}) || progress[1].PercentDone != 1 {
		t.Errorf("progress = %+v", progress)
	}

	want := []string{"backup", "--json", "--host", "mcs-backup",
		"--tag", "mcs-backup", "--tag", "manual", "--tag", "note:two words",
		"--parent", "abc123", "--exclude", "*.tmp", "--exclude", "cache",
		"--one-file-system", "--", "."}
	if got := f.CallsTo(t, "backup")[0]; !slices.Equal(got, want) {
		t.Errorf("args =\n %q\nwant\n %q", got, want)
	}
	if got, want := f.LastDir(t), r.dir; resolve(t, got) != resolve(t, want) {
		t.Errorf("working directory = %q, want %q", got, want)
	}
	if f.LastEnv(t)["RESTIC_REPOSITORY"] != "/repo" {
		t.Errorf("RESTIC_REPOSITORY not passed: %v", f.LastEnv(t)["RESTIC_REPOSITORY"])
	}
}

func resolve(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestBackupStdin(t *testing.T) {
	f := resticfake.New(t)
	f.On(t, "backup", resticfake.Response{Stdout: fixture(t, "backup_stdin.jsonl")})
	sum, err := newRepo(t, f).Backup(t.Context(), BackupOptions{
		Host: "h", StdinCommand: []string{"/opt/dump.sh", "--all"}, StdinFilename: "dump.sql", Paths: []string{"ignored"},
	}, nil)
	if err != nil || sum.TotalBytesProcessed != 9 {
		t.Fatalf("Backup() = %+v, %v", sum, err)
	}
	want := []string{"backup", "--json", "--host", "h", "--stdin-from-command", "--stdin-filename", "dump.sql", "--", "/opt/dump.sh", "--all"}
	if got := f.CallsTo(t, "backup")[0]; !slices.Equal(got, want) {
		t.Errorf("args = %q, want %q", got, want)
	}
}

func TestBackupIncomplete(t *testing.T) {
	f := resticfake.New(t)
	f.On(t, "backup", resticfake.Response{
		Stdout: fixture(t, "backup_warning.jsonl"), Stderr: fixture(t, "backup_warning.stderr"), Exit: 3,
	})
	sum, err := newRepo(t, f).Backup(t.Context(), BackupOptions{Host: "h", Paths: []string{"."}}, nil)
	if !errors.Is(err, ErrIncomplete) {
		t.Fatalf("err = %v, want ErrIncomplete", err)
	}
	if sum.SnapshotID == "" {
		t.Error("the snapshot was created, the summary must be valid")
	}
	if len(sum.Errors) != 1 || !strings.Contains(sum.Errors[0], "permission denied") {
		t.Errorf("errors = %q", sum.Errors)
	}
	if !strings.Contains(err.Error(), "could not be read") {
		t.Errorf("error = %v", err)
	}
}

func TestBackupFailure(t *testing.T) {
	f := resticfake.New(t)
	f.On(t, "backup", resticfake.Response{
		Stdout: fixture(t, "backup_stdin_fail.jsonl"), Stderr: fixture(t, "backup_stdin_fail.stderr"), Exit: 1,
	})
	sum, err := newRepo(t, f).Backup(t.Context(), BackupOptions{Host: "h", StdinCommand: []string{"/bin/false"}, StdinFilename: "x"}, nil)
	if err == nil || errors.Is(err, ErrIncomplete) || sum.SnapshotID != "" {
		t.Fatalf("Backup() = %+v, %v", sum, err)
	}
	if !strings.Contains(err.Error(), "command failed") {
		t.Errorf("error = %v", err)
	}
	if len(sum.Errors) != 1 {
		t.Errorf("errors = %q", sum.Errors)
	}
}

func TestBackupNoSummary(t *testing.T) {
	f := resticfake.New(t)
	f.On(t, "backup", resticfake.Response{Stdout: "{\"message_type\":\"status\"}\n"})
	if _, err := newRepo(t, f).Backup(t.Context(), BackupOptions{Host: "h", Paths: []string{"."}}, nil); err == nil {
		t.Error("expected an error without a summary")
	}
}

func TestBackupRejectsCommaInTags(t *testing.T) {
	f := resticfake.New(t)
	_, err := newRepo(t, f).Backup(t.Context(), BackupOptions{Host: "h", Tags: []string{"note:a,b"}, Paths: []string{"."}}, nil)
	if err == nil || len(f.Calls(t)) != 0 {
		t.Errorf("err = %v, calls = %q", err, f.Calls(t))
	}
}

func TestBackupCancel(t *testing.T) {
	f := resticfake.New(t)
	f.On(t, "backup", resticfake.Response{Sleep: 30 * time.Second})
	r := newRepo(t, f)

	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := r.Backup(ctx, BackupOptions{Host: "h", Paths: []string{"."}}, nil)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want the context error", err)
	}
	if !errors.Is(err, ErrInterrupted) {
		t.Errorf("err = %v, want ErrInterrupted: restic must be stopped with SIGINT", err)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("took %v", d)
	}
}

func TestGlobalArgsComeBeforeTerminator(t *testing.T) {
	f := resticfake.New(t)
	f.On(t, "backup", resticfake.Response{Stdout: fixture(t, "backup.jsonl")})
	r := newRepo(t, f, func(o *Options) { o.GlobalArgs = []string{"-o", "s3.list-objects-v1=true"} })
	if _, err := r.Backup(t.Context(), BackupOptions{Host: "h", StdinCommand: []string{"dump"}, StdinFilename: "x"}, nil); err != nil {
		t.Fatal(err)
	}
	got := f.CallsTo(t, "backup")[0]
	if !slices.Equal(got[:3], []string{"backup", "-o", "s3.list-objects-v1=true"}) || got[len(got)-2] != "--" {
		t.Errorf("args = %q", got)
	}
}

func TestRestore(t *testing.T) {
	f := resticfake.New(t)
	f.On(t, "restore", resticfake.Response{Stdout: `{"message_type":"status","percent_done":0.5,"total_files":5,"files_restored":2}` + "\n" + fixture(t, "restore.jsonl")})
	var progress []RestoreStatus
	sum, err := newRepo(t, f).Restore(t.Context(), RestoreOptions{
		Snapshot: "abc", Target: "/mnt", Includes: []string{"/a", "/b"}, IncludeFile: "/tmp/list", Delete: true, Verify: true,
		Extra: []string{"--sparse"},
	}, func(s RestoreStatus) { progress = append(progress, s) })
	if err != nil || sum.FilesRestored != 5 || sum.BytesRestored != 14 || sum.TotalFiles != 5 {
		t.Fatalf("Restore() = %+v, %v", sum, err)
	}
	if len(progress) != 1 || progress[0].FilesRestored != 2 {
		t.Errorf("progress = %+v", progress)
	}
	want := []string{"restore", "--json", "--target", "/mnt", "--delete", "--verify",
		"--include", "/a", "--include", "/b", "--include-file", "/tmp/list", "--sparse", "--", "abc"}
	if got := f.CallsTo(t, "restore")[0]; !slices.Equal(got, want) {
		t.Errorf("args =\n %q\nwant\n %q", got, want)
	}
}

func TestDump(t *testing.T) {
	f := resticfake.New(t)
	f.On(t, "dump", resticfake.Response{Stdout: "SELECT 1;\n"})
	var buf bytes.Buffer
	if err := newRepo(t, f).Dump(t.Context(), "abc", "/dump.sql", &buf); err != nil || buf.String() != "SELECT 1;\n" {
		t.Errorf("Dump() = %q, %v", buf.String(), err)
	}
	if got := f.CallsTo(t, "dump")[0]; !slices.Equal(got, []string{"dump", "--", "abc", "/dump.sql"}) {
		t.Errorf("args = %q", got)
	}
	f.On(t, "dump", resticfake.Response{Stderr: "Fatal: path not found\n", Exit: 1})
	if err := newRepo(t, f).Dump(t.Context(), "abc", "/nope", &buf); err == nil {
		t.Error("expected an error")
	}
}

func TestForget(t *testing.T) {
	f := resticfake.New(t)
	f.On(t, "forget", resticfake.Response{Stdout: fixture(t, "forget_dry_run.json")})
	groups, err := newRepo(t, f).Forget(t.Context(), ForgetOptions{
		Hosts: []string{"mcs-backup", "dummy"}, KeepTags: []string{"keep"}, Policy: []string{"--keep-last", "1"},
		DryRun: true, Extra: []string{"--keep-within", "30d"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) == 0 || len(groups[0].Keep) == 0 || !slices.Equal(groups[0].Paths, []string{"/dump.sql"}) {
		t.Errorf("groups = %+v", groups)
	}
	var removed int
	for _, g := range groups {
		removed += len(g.Remove)
	}
	if removed == 0 {
		t.Error("the fixture removes snapshots")
	}
	want := []string{"forget", "--json", "--group-by", "paths", "--dry-run", "--no-lock",
		"--host", "mcs-backup", "--host", "dummy", "--keep-tag", "keep", "--keep-last", "1", "--keep-within", "30d"}
	if got := f.CallsTo(t, "forget")[0]; !slices.Equal(got, want) {
		t.Errorf("args =\n %q\nwant\n %q", got, want)
	}

	// a real run must lock the repository
	if _, err := newRepo(t, f).Forget(t.Context(), ForgetOptions{Policy: []string{"--keep-last", "1"}}); err != nil {
		t.Fatal(err)
	}
	if got := f.CallsTo(t, "forget")[1]; slices.Contains(got, "--no-lock") || slices.Contains(got, "--dry-run") {
		t.Errorf("args = %q", got)
	}
}

func TestStats(t *testing.T) {
	f := resticfake.New(t)
	f.On(t, "stats", resticfake.Response{Stdout: fixture(t, "stats_restore_size.json")},
		resticfake.Response{Stdout: fixture(t, "stats_raw_data.json")})
	r := newRepo(t, f)
	s, err := r.Stats(t.Context(), StatsRestoreSize, "abc")
	if err != nil || s.TotalSize != 14 || s.TotalFileCount != 5 || s.SnapshotsCount != 1 {
		t.Errorf("restore-size = %+v, %v", s, err)
	}
	s, err = r.Stats(t.Context(), StatsRawData)
	if err != nil || s.TotalSize != 2173 || s.TotalUncompressedSize != 4376 || s.CompressionRatio < 2 {
		t.Errorf("raw-data = %+v, %v", s, err)
	}
	if got := f.CallsTo(t, "stats")[0]; !slices.Equal(got, []string{"stats", "--json", "--no-lock", "--mode", "restore-size", "--", "abc"}) {
		t.Errorf("args = %q", got)
	}
}

func TestLocks(t *testing.T) {
	f := resticfake.New(t)
	f.On(t, "list locks", resticfake.Response{Stdout: fixture(t, "list_locks.txt") + "gone\n"})
	f.On(t, "cat lock",
		resticfake.Response{Stdout: fixture(t, "cat_lock.json")},
		resticfake.Response{Stderr: "Fatal: not found\n", Exit: 1})
	locks, err := newRepo(t, f).Locks(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(locks) != 1 {
		t.Fatalf("got %d locks, want 1 (the vanished one is skipped): %+v", len(locks), locks)
	}
	l := locks[0]
	if l.ID != "0d843e6aa448e38cee122781ecd5275168f346ace1ccd0ddd2eb38cc3a7b3045" || l.Exclusive || l.PID != 25548 ||
		l.Hostname != "testhost" || l.Username != "testuser" || l.Time.IsZero() {
		t.Errorf("lock = %+v", l)
	}
}

func TestUnlock(t *testing.T) {
	f := resticfake.New(t)
	f.On(t, "unlock", resticfake.Response{})
	r := newRepo(t, f)
	if err := r.Unlock(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	if err := r.Unlock(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	calls := f.CallsTo(t, "unlock")
	if !slices.Equal(calls[0], []string{"unlock"}) || !slices.Equal(calls[1], []string{"unlock", "--remove-all"}) {
		t.Errorf("calls = %q", calls)
	}
}

func TestLs(t *testing.T) {
	f := resticfake.New(t)
	f.On(t, "ls", resticfake.Response{Stdout: fixture(t, "ls.jsonl")})
	r := newRepo(t, f)

	var nodes []Node
	for n, err := range r.Ls(t.Context(), "abc", "/a") {
		if err != nil {
			t.Fatal(err)
		}
		nodes = append(nodes, n)
	}
	if len(nodes) == 0 {
		t.Fatal("no nodes")
	}
	var files int
	for _, n := range nodes {
		if n.Path == "" || n.Type == "" {
			t.Errorf("incomplete node %+v", n)
		}
		if n.Type == "file" {
			files++
		}
	}
	if files != 3 || nodes[0].Path != "/a" || nodes[0].Type != "dir" {
		t.Errorf("nodes = %+v", nodes)
	}
	if got := f.CallsTo(t, "ls")[0]; !slices.Equal(got, []string{"ls", "--json", "--no-lock", "--", "abc", "/a"}) {
		t.Errorf("args = %q", got)
	}

	// stopping early is not an error
	count := 0
	for _, err := range r.Ls(t.Context(), "abc") {
		if err != nil {
			t.Fatal(err)
		}
		if count++; count == 2 {
			break
		}
	}
	if count != 2 {
		t.Errorf("count = %d", count)
	}
}

func TestLsFailure(t *testing.T) {
	f := resticfake.New(t)
	f.On(t, "ls", resticfake.Response{Stderr: "Fatal: no matching ID found\n", Exit: 1})
	var gotErr error
	for _, err := range newRepo(t, f).Ls(t.Context(), "nope") {
		gotErr = err
	}
	if gotErr == nil || !strings.Contains(gotErr.Error(), "no matching ID") {
		t.Errorf("err = %v", gotErr)
	}
}

func TestTag(t *testing.T) {
	f := resticfake.New(t)
	f.On(t, "tag", resticfake.Response{Stdout: `{"message_type":"changed","old_snapshot_id":"old","new_snapshot_id":"new123"}` + "\n" +
		`{"message_type":"summary","changed_snapshots":1}` + "\n"},
		resticfake.Response{Stdout: "create exclusive lock for repository\nmodified tags on 1 snapshots\n"})
	r := newRepo(t, f)

	id, err := r.Tag(t.Context(), "old", []string{"keep", "note:why not"}, []string{"suspicious"})
	if err != nil || id != "new123" {
		t.Fatalf("Tag() = %q, %v", id, err)
	}
	want := []string{"tag", "--json", "--add", "keep", "--add", "note:why not", "--remove", "suspicious", "--", "old"}
	if got := f.CallsTo(t, "tag")[0]; !slices.Equal(got, want) {
		t.Errorf("args = %q, want %q", got, want)
	}
	// restic 0.17 prints text only: no new ID, but no error either
	if id, err := r.Tag(t.Context(), "x", []string{"keep"}, nil); err != nil || id != "" {
		t.Errorf("text output: %q, %v", id, err)
	}
	if _, err := r.Tag(t.Context(), "x", []string{"a,b"}, nil); err == nil {
		t.Error("comma in tag must be rejected")
	}
}

func TestInitAndCopy(t *testing.T) {
	f := resticfake.New(t)
	f.On(t, "init", resticfake.Response{Stdout: fixture(t, "init.json")})
	f.On(t, "copy", resticfake.Response{Stdout: "snapshot abc copied\n"})

	main := newRepo(t, f, func(o *Options) {
		o.Env = []string{"PATH=" + os.Getenv("PATH"), "RESTIC_REPOSITORY=/main", "RESTIC_PASSWORD_FILE=/run/main-pw", "AWS_ACCESS_KEY_ID=main"}
	})
	second := newRepo(t, f, func(o *Options) {
		o.Env = []string{"PATH=" + os.Getenv("PATH"), "RESTIC_REPOSITORY=/second", "RESTIC_PASSWORD=second-pw"}
	})

	id, err := second.Init(t.Context(), main)
	if err != nil || id != "f77dce529410d21e367a125e22ec301c937898fc97cee1a429d94832fe99d744" {
		t.Fatalf("Init() = %q, %v", id, err)
	}
	if got := f.CallsTo(t, "init")[0]; !slices.Equal(got, []string{"init", "--json", "--copy-chunker-params"}) {
		t.Errorf("init args = %q", got)
	}
	env := f.LastEnv(t)
	if env["RESTIC_REPOSITORY"] != "/second" || env["RESTIC_FROM_REPOSITORY"] != "/main" ||
		env["RESTIC_FROM_PASSWORD_FILE"] != "/run/main-pw" {
		t.Errorf("init env = %v", env)
	}
	if _, set := env["AWS_ACCESS_KEY_ID"]; set {
		t.Error("credentials of the source leaked into the destination environment")
	}

	var out bytes.Buffer
	if err := second.Copy(t.Context(), main, CopyOptions{Snapshots: []string{"abc"}, Hosts: []string{"h"}}, &out); err != nil {
		t.Fatal(err)
	}
	if got := f.CallsTo(t, "copy")[0]; !slices.Equal(got, []string{"copy", "--host", "h", "--", "abc"}) {
		t.Errorf("copy args = %q", got)
	}
	if out.String() != "snapshot abc copied\n" {
		t.Errorf("output = %q", out.String())
	}
	if env := f.LastEnv(t); env["RESTIC_FROM_REPOSITORY"] != "/main" || env["RESTIC_PASSWORD"] != "second-pw" {
		t.Errorf("copy env = %v", env)
	}

	// plain init
	if _, err := second.Init(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if got := f.CallsTo(t, "init")[1]; slices.Contains(got, "--copy-chunker-params") {
		t.Errorf("args = %q", got)
	}
	if err := second.Copy(t.Context(), nil, CopyOptions{}, &out); err == nil {
		t.Error("Copy without source must fail")
	}
}

func TestPruneAndCheck(t *testing.T) {
	f := resticfake.New(t)
	f.On(t, "prune", resticfake.Response{Stdout: "pruned\n", Stderr: "warning\n"})
	f.On(t, "check", resticfake.Response{Stderr: "Fatal: pack 1 is damaged\n", Exit: 1})
	r := newRepo(t, f)

	var out bytes.Buffer
	if err := r.Prune(t.Context(), []string{"--max-unused", "5%"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "pruned") || !strings.Contains(out.String(), "warning") {
		t.Errorf("output = %q, want stdout and stderr", out.String())
	}
	if got := f.CallsTo(t, "prune")[0]; !slices.Equal(got, []string{"prune", "--max-unused", "5%"}) {
		t.Errorf("args = %q", got)
	}

	out.Reset()
	err := r.Check(t.Context(), nil, &out)
	if err == nil || !strings.Contains(err.Error(), "damaged") || !strings.Contains(out.String(), "damaged") {
		t.Errorf("Check() error = %v, output = %q", err, out.String())
	}
}

func TestStartFailure(t *testing.T) {
	r := New(Options{Bin: filepath.Join(t.TempDir(), "missing")})
	_, err := r.Config(t.Context())
	if err == nil || errors.Is(err, ErrLocked) {
		t.Errorf("err = %v", err)
	}
	if _, ok := errors.AsType[*ExitError](err); ok {
		t.Error("a missing binary is not an ExitError")
	}
}

func TestRemove(t *testing.T) {
	f := resticfake.New(t)
	f.On(t, "forget", resticfake.Response{Stdout: "removed snapshot abc\n"})
	if err := newRepo(t, f).Remove(t.Context(), "abc", "def"); err != nil {
		t.Fatal(err)
	}
	if got := f.CallsTo(t, "forget")[0]; !slices.Equal(got, []string{"forget", "--", "abc", "def"}) {
		t.Errorf("args = %q", got)
	}
}

func TestConfigReportsAMissingBucketAtOnce(t *testing.T) {
	for name, line := range map[string]string{
		"s3":    "Stat(<config/>) returned error, retrying after 1.07s: Stat: The specified bucket does not exist",
		"azure": "Stat(<config/>) returned error, retrying after 1s: -> ContainerNotFound: The specified container does not exist.",
		"gcs":   "Stat(<config/>) returned error, retrying after 1s: storage: bucket doesn't exist",
	} {
		t.Run(name, func(t *testing.T) {
			f := resticfake.New(t)
			// restic keeps retrying for minutes: the fake sleeps, the answer must not wait for it
			f.On(t, "cat config", resticfake.Response{Early: line + "\n", Sleep: 30 * time.Second, Exit: 1})
			start := time.Now()
			_, err := newRepo(t, f).Config(t.Context())
			if !errors.Is(err, ErrStorageMissing) || !errors.Is(err, ErrRepoMissing) {
				t.Fatalf("Config() error = %v, want a missing bucket that counts as a missing repository", err)
			}
			if took := time.Since(start); took > 10*time.Second {
				t.Errorf("Config() took %v", took)
			}
			// every query is protected the same way
			f.On(t, "snapshots", resticfake.Response{Early: line + "\n", Sleep: 30 * time.Second, Exit: 1})
			if _, err := newRepo(t, f).Snapshots(t.Context()); !errors.Is(err, ErrStorageMissing) {
				t.Errorf("Snapshots() error = %v", err)
			}
		})
	}
}

func TestConfigDoesNotMistakeOtherErrorsForAMissingBucket(t *testing.T) {
	f := resticfake.New(t)
	f.On(t, "cat config", resticfake.Response{Stderr: "Fatal: unable to open config file: connection refused\n", Exit: 1})
	_, err := newRepo(t, f).Config(t.Context())
	if err == nil || errors.Is(err, ErrRepoMissing) {
		t.Errorf("Config() error = %v", err)
	}
}

func TestLineWriterBoundsLinesWithoutBreaks(t *testing.T) {
	var lines [][]byte
	w := &lineWriter{fn: func(l []byte) { lines = append(lines, bytes.Clone(l)) }}
	chunk := bytes.Repeat([]byte("x"), 64<<10)
	for range 40 { // 2.5 MiB without a line break
		if _, err := w.Write(chunk); err != nil {
			t.Fatal(err)
		}
		if len(w.buf) > maxLine {
			t.Fatalf("the buffer holds %d bytes", len(w.buf))
		}
	}
	_, _ = w.Write([]byte("\n{\"a\":1}\n"))
	w.flush()
	if len(lines) != 4 || len(lines[0]) != maxLine || string(lines[3]) != `{"a":1}` {
		t.Errorf("%d lines, want two full pieces, the rest and the line after them", len(lines))
	}
}
