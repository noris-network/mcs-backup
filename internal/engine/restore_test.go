package engine

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/noris-network/mcs-backup/v2/internal/restic"
	"github.com/noris-network/mcs-backup/v2/internal/resticfake"
	"github.com/noris-network/mcs-backup/v2/internal/service"
)

// restoreRig prepares a rig with one regular snapshot "snap1" and data in the root.
func restoreRig(t *testing.T, env ...string) *rig {
	t.Helper()
	r := newRig(t, false, env...)
	s := snap("snap1", "me", time.Hour, 3, 1000, rootPaths(r), "mcs-backup")
	r.main.On(t, "snapshots", resticfake.Response{Stdout: snapsJSON(t, s)})
	r.main.On(t, "stats", resticfake.Response{Stdout: statsJSON(1000)})
	r.main.On(t, "restore", resticfake.Response{Stdout: restoreSummary(3)})
	r.main.On(t, "backup", resticfake.Response{Stdout: summaryLine("safety1", 2, 400)})
	r.main.On(t, "tag", resticfake.Response{})
	writeFile(t, filepath.Join(r.root, "data", "current.bin"), 400)
	return r
}

func TestRestorePreflight(t *testing.T) {
	t.Run("in place", func(t *testing.T) {
		r := restoreRig(t)
		p, err := r.e.RestorePreflight(t.Context(), RestoreRequest{Snapshot: "latest"})
		if err != nil {
			t.Fatal(err)
		}
		if p.Snapshot.ID != "snap1" || !p.InPlace || p.Mode != ModeExact || p.TargetEmpty || !p.SafetySnapshot || p.PipeMode {
			t.Errorf("preflight = %+v", p)
		}
		// 1000 bytes in the snapshot, 400 already there, plus 10 percent
		if p.NeedBytes != 660 || p.FreeBytes <= 0 || p.SpaceWarning {
			t.Errorf("space: need %d, free %d, warning %v", p.NeedBytes, p.FreeBytes, p.SpaceWarning)
		}
		if p.Target != r.root {
			t.Errorf("target = %q", p.Target)
		}
	})

	t.Run("other target always merges", func(t *testing.T) {
		r := restoreRig(t)
		target := t.TempDir()
		p, err := r.e.RestorePreflight(t.Context(), RestoreRequest{Snapshot: "snap1", Target: target, Mode: ModeExact})
		if err != nil {
			t.Fatal(err)
		}
		if p.InPlace || p.Mode != ModeMerge || !p.TargetEmpty || p.SafetySnapshot || p.NeedBytes != 1100 || len(p.Warnings) != 0 {
			t.Errorf("preflight = %+v", p)
		}

		writeFile(t, filepath.Join(target, "x"), 1)
		p, err = r.e.RestorePreflight(t.Context(), RestoreRequest{Snapshot: "snap1", Target: target})
		if err != nil || p.TargetEmpty || len(p.Warnings) != 1 || !strings.Contains(p.Warnings[0], "not empty") {
			t.Errorf("preflight = %+v, %v", p, err)
		}
	})

	t.Run("lost+found does not count", func(t *testing.T) {
		r := restoreRig(t)
		target := t.TempDir()
		writeFile(t, filepath.Join(target, "lost+found", "x"), 1)
		writeFile(t, filepath.Join(target, ".DS_Store"), 1)
		p, err := r.e.RestorePreflight(t.Context(), RestoreRequest{Snapshot: "snap1", Target: target})
		if err != nil || !p.TargetEmpty {
			t.Errorf("preflight = %+v, %v", p, err)
		}
	})

	t.Run("space warning", func(t *testing.T) {
		r := restoreRig(t)
		r.main.On(t, "stats", resticfake.Response{Stdout: statsJSON(1 << 60)})
		p, err := r.e.RestorePreflight(t.Context(), RestoreRequest{Snapshot: "snap1"})
		if err != nil || !p.SpaceWarning || len(p.Warnings) != 1 {
			t.Errorf("preflight = %+v, %v", p, err)
		}
	})

	t.Run("partial restore sizes only the included files", func(t *testing.T) {
		r := restoreRig(t)
		r.main.On(t, "ls", resticfake.Response{Stdout: lsOutput(
			restic.Node{Type: "dir", Path: "/a"}, file("/a/f1", 100), file("/a/f2", 200))})
		p, err := r.e.RestorePreflight(t.Context(), RestoreRequest{Snapshot: "snap1", Includes: []string{"/a"}})
		if err != nil || p.NeedBytes != 330 {
			t.Errorf("preflight = %+v, %v", p, err)
		}
		if len(r.main.CallsTo(t, "stats")) != 0 {
			t.Error("a partial restore must not use the size of the whole snapshot")
		}
	})

	t.Run("errors", func(t *testing.T) {
		r := restoreRig(t)
		if _, err := r.e.RestorePreflight(t.Context(), RestoreRequest{Snapshot: "nope"}); !errors.Is(err, ErrNoSnapshot) {
			t.Errorf("unknown snapshot: %v", err)
		}
		if _, err := r.e.RestorePreflight(t.Context(), RestoreRequest{Snapshot: "snap1", Mode: "weird"}); err == nil {
			t.Error("unknown mode accepted")
		}
	})

	t.Run("a failing size estimate only warns", func(t *testing.T) {
		r := restoreRig(t)
		r.main.On(t, "stats", resticfake.Response{Stderr: "Fatal: boom\n", Exit: 1})
		p, err := r.e.RestorePreflight(t.Context(), RestoreRequest{Snapshot: "snap1"})
		if err != nil || p.SpaceWarning || len(p.Warnings) != 1 {
			t.Errorf("preflight = %+v, %v", p, err)
		}
	})
}

func TestRestoreInPlace(t *testing.T) {
	log := filepath.Join(t.TempDir(), "hooks.log")
	pre := script(t, `echo "pre $*" >> "$HOOKLOG"`)
	post := script(t, `echo "post $*" >> "$HOOKLOG"`)
	r := restoreRig(t, "HOOKLOG="+log, "PRE_RESTORE_SCRIPT="+pre, "POST_RESTORE_SCRIPT="+post)

	res := r.e.Restore(t.Context(), RestoreRequest{Snapshot: "latest", Confirm: "latest", HookArgs: []string{"db1", "db2"}}, r.sink())
	if res.Result != service.ResultSuccess || res.Snapshot != "snap1" || res.SafetySnapshot != "safety1" || res.Summary.FilesRestored != 3 {
		t.Fatalf("result = %+v", res)
	}

	backup := r.main.CallsTo(t, "backup")
	if len(backup) != 1 || !hasArgs(backup[0], "--tag", "pre-restore") || !hasArgs(backup[0], "--tag", "mcs-backup") {
		t.Errorf("safety snapshot call = %q", backup)
	}
	call := r.main.CallsTo(t, "restore")[0]
	for _, want := range [][]string{{"snap1"}, {"--target", r.root}, {"--delete"}} {
		if !hasArgs(call, want...) {
			t.Errorf("restore args lack %q: %q", want, call)
		}
	}
	if hookLog, _ := os.ReadFile(log); string(hookLog) != "pre db1 db2\npost db1 db2\n" {
		t.Errorf("hook log = %q", hookLog)
	}
	if !slices.Equal(r.phases, []string{"prepare", "safety snapshot", "restore", "wrapup"}) {
		t.Errorf("phases = %q", r.phases)
	}
	if len(r.main.CallsTo(t, "forget")) != 0 {
		t.Error("a safety snapshot is neither checked for plausibility nor followed by forget")
	}
}

func TestRestoreNeedsConfirmation(t *testing.T) {
	for _, confirm := range []string{"", "yes", "snap2", "snap"} {
		r := restoreRig(t)
		res := r.e.Restore(t.Context(), RestoreRequest{Snapshot: "snap1", Confirm: confirm}, r.sink())
		if res.Result != service.ResultFailure || !strings.Contains(res.Error, "confirm with \"snap1\"") {
			t.Errorf("confirm %q: result = %+v", confirm, res)
		}
		if len(r.main.CallsTo(t, "restore")) != 0 || len(r.main.CallsTo(t, "backup")) != 0 {
			t.Errorf("confirm %q: nothing may happen without confirmation: %q", confirm, r.main.Calls(t))
		}
	}
	r := restoreRig(t)
	if res := r.e.Restore(t.Context(), RestoreRequest{Snapshot: "snap1", Confirm: "snap1"}, r.sink()); res.Result != service.ResultSuccess {
		t.Errorf("the ID works as confirmation too: %+v", res)
	}
}

func TestRestoreSafetySnapshotFailure(t *testing.T) {
	log := filepath.Join(t.TempDir(), "hooks.log")
	post := script(t, `echo "post" >> "$HOOKLOG"`)
	r := restoreRig(t, "HOOKLOG="+log, "POST_RESTORE_SCRIPT="+post)
	r.main.On(t, "backup", resticfake.Response{Stderr: "Fatal: database is down\n", Exit: 1})

	res := r.e.Restore(t.Context(), RestoreRequest{Snapshot: "latest", Confirm: "latest"}, r.sink())
	if res.Result != service.ResultFailure || !strings.Contains(res.Error, "safety snapshot failed") || !strings.Contains(res.Error, "without a safety snapshot") {
		t.Fatalf("result = %+v", res)
	}
	if len(r.main.CallsTo(t, "restore")) != 0 {
		t.Error("the restore must not run after a failed safety snapshot")
	}
	if _, err := os.Stat(log); err == nil {
		t.Error("the post-restore hook must not run after a failed restore")
	}

	res = r.e.Restore(t.Context(), RestoreRequest{Snapshot: "latest", Confirm: "latest", NoSafetySnapshot: true}, r.sink())
	if res.Result != service.ResultSuccess || res.SafetySnapshot != "" {
		t.Fatalf("explicitly skipping the safety snapshot: %+v", res)
	}
	if got := len(r.main.CallsTo(t, "backup")); got != 1 {
		t.Errorf("backup calls = %d, the second restore must not try again", got)
	}
}

func TestRestoreSafetySnapshotWithUnreadableFiles(t *testing.T) {
	r := restoreRig(t)
	r.main.On(t, "backup", resticfake.Response{Stdout: summaryLine("safety1", 2, 400), Stderr: "Warning: at least one source file could not be read\n", Exit: 3})
	res := r.e.Restore(t.Context(), RestoreRequest{Snapshot: "latest", Confirm: "latest"}, r.sink())
	if res.Result != service.ResultSuccess || res.SafetySnapshot != "safety1" {
		t.Errorf("result = %+v", res)
	}
}

func TestRestoreVariants(t *testing.T) {
	tests := []struct {
		name       string
		req        RestoreRequest
		prepare    func(t *testing.T, r *rig)
		wantDelete bool
		wantBackup bool
		wantArgs   [][]string
	}{
		{
			name: "merge in place", req: RestoreRequest{Snapshot: "snap1", Confirm: "snap1", Mode: ModeMerge},
			wantDelete: false, wantBackup: true,
		},
		{
			name: "partial restore may delete inside the included paths", req: RestoreRequest{Snapshot: "snap1", Confirm: "snap1", Includes: []string{"/a", "/b"}},
			prepare: func(t *testing.T, r *rig) {
				r.main.On(t, "ls", resticfake.Response{Stdout: lsOutput(file("/a/f", 1))})
			},
			wantDelete: true, wantBackup: true, wantArgs: [][]string{{"--include", "/a"}, {"--include", "/b"}},
		},
		{
			name: "empty target needs no safety snapshot", req: RestoreRequest{Snapshot: "snap1", Confirm: "snap1"},
			prepare:    func(t *testing.T, r *rig) { _ = os.RemoveAll(filepath.Join(r.root, "data")) },
			wantDelete: true, wantBackup: false,
		},
		{
			name: "restic arguments from the configuration", req: RestoreRequest{Snapshot: "snap1", Confirm: "snap1"},
			wantDelete: true, wantBackup: true, wantArgs: [][]string{{"--sparse"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := restoreRig(t, "RESTIC_RESTORE_ARGS=--sparse")
			if tt.prepare != nil {
				tt.prepare(t, r)
			}
			res := r.e.Restore(t.Context(), tt.req, r.sink())
			if res.Result != service.ResultSuccess {
				t.Fatalf("result = %+v", res)
			}
			call := r.main.CallsTo(t, "restore")[0]
			if slices.Contains(call, "--delete") != tt.wantDelete {
				t.Errorf("--delete = %v, want %v: %q", slices.Contains(call, "--delete"), tt.wantDelete, call)
			}
			if got := len(r.main.CallsTo(t, "backup")) > 0; got != tt.wantBackup {
				t.Errorf("safety snapshot = %v, want %v", got, tt.wantBackup)
			}
			for _, want := range tt.wantArgs {
				if !hasArgs(call, want...) {
					t.Errorf("restore args lack %q: %q", want, call)
				}
			}
		})
	}
}

func TestRestoreToOtherTarget(t *testing.T) {
	r := restoreRig(t)
	target := t.TempDir()
	res := r.e.Restore(t.Context(), RestoreRequest{Snapshot: "latest", Target: target}, r.sink())
	if res.Result != service.ResultSuccess {
		t.Fatalf("result = %+v: another target needs no confirmation", res)
	}
	call := r.main.CallsTo(t, "restore")[0]
	if slices.Contains(call, "--delete") || !hasArgs(call, "--target", target) || len(r.main.CallsTo(t, "backup")) != 0 {
		t.Errorf("restore = %q, backups = %q", call, r.main.CallsTo(t, "backup"))
	}
}

func TestRestoreSpaceWarning(t *testing.T) {
	r := restoreRig(t)
	r.main.On(t, "stats", resticfake.Response{Stdout: statsJSON(1 << 60)})
	req := RestoreRequest{Snapshot: "snap1", Confirm: "snap1"}

	res := r.e.Restore(t.Context(), req, r.sink())
	if res.Result != service.ResultFailure || !strings.Contains(res.Error, "not enough free space") {
		t.Fatalf("result = %+v", res)
	}
	if len(r.main.CallsTo(t, "restore")) != 0 || len(r.main.CallsTo(t, "backup")) != 0 {
		t.Error("nothing may happen when the space is short")
	}

	req.IgnoreSpaceWarning = true
	res = r.e.Restore(t.Context(), req, r.sink())
	if res.Result != service.ResultWarning || len(res.Warnings) == 0 {
		t.Errorf("overriding the warning keeps it visible: %+v", res)
	}
}

func TestRestoreFailure(t *testing.T) {
	log := filepath.Join(t.TempDir(), "hooks.log")
	post := script(t, `echo "post" >> "$HOOKLOG"`)
	r := restoreRig(t, "HOOKLOG="+log, "POST_RESTORE_SCRIPT="+post)
	r.main.On(t, "restore", resticfake.Response{Stderr: "Fatal: disk full\n", Exit: 1})

	res := r.e.Restore(t.Context(), RestoreRequest{Snapshot: "snap1", Confirm: "snap1"}, r.sink())
	if res.Result != service.ResultFailure || !strings.Contains(res.Error, "disk full") || res.SafetySnapshot != "safety1" {
		t.Fatalf("result = %+v", res)
	}
	if _, err := os.Stat(log); err == nil {
		t.Error("the post-restore hook must not run after a failed restore")
	}
}

func TestRestoreTimeout(t *testing.T) {
	r := restoreRig(t, "RESTORE_TIMEOUT=300ms")
	r.main.On(t, "restore", resticfake.Response{Sleep: 30 * time.Second})
	res := r.e.Restore(t.Context(), RestoreRequest{Snapshot: "snap1", Confirm: "snap1", NoSafetySnapshot: true}, r.sink())
	if res.Result != service.ResultTimeout {
		t.Errorf("result = %+v", res)
	}
}

func TestRestorePreHookFails(t *testing.T) {
	pre := script(t, `echo "cannot stop the application" >&2; exit 1`)
	r := restoreRig(t, "PRE_RESTORE_SCRIPT="+pre)
	res := r.e.Restore(t.Context(), RestoreRequest{Snapshot: "snap1", Confirm: "snap1"}, r.sink())
	if res.Result != service.ResultFailure || !strings.Contains(res.Error, "pre-restore hook") || len(r.main.CallsTo(t, "restore")) != 0 {
		t.Errorf("result = %+v", res)
	}
}

func TestRestorePipeMode(t *testing.T) {
	received := filepath.Join(t.TempDir(), "received")
	in := script(t, "echo dump")
	out := script(t, `cat > "$RECEIVED"`)
	setup := func(t *testing.T) *rig {
		r := newRig(t, false, "PIPE_IN_SCRIPT="+in, "PIPE_OUT_SCRIPT="+out, "RECEIVED="+received, "PIPE_IN_FILENAME=dump.sql")
		s := snap("snap1", "me", time.Hour, 1, 100, []string{"/dump.sql"}, "mcs-backup")
		r.main.On(t, "snapshots", resticfake.Response{Stdout: snapsJSON(t, s)})
		r.main.On(t, "dump", resticfake.Response{Stdout: "SELECT 1;\n"})
		r.main.On(t, "backup", resticfake.Response{Stdout: summaryLine("safety1", 1, 50)})
		return r
	}

	t.Run("streams the dump to the script", func(t *testing.T) {
		_ = os.Remove(received)
		r := setup(t)
		res := r.e.Restore(t.Context(), RestoreRequest{Snapshot: "latest", Confirm: "latest"}, r.sink())
		if res.Result != service.ResultSuccess || res.SafetySnapshot != "safety1" {
			t.Fatalf("result = %+v", res)
		}
		if got, _ := os.ReadFile(received); string(got) != "SELECT 1;\n" {
			t.Errorf("script received %q", got)
		}
		if call := r.main.CallsTo(t, "dump")[0]; !slices.Equal(call, []string{"dump", "snap1", "/dump.sql"}) {
			t.Errorf("dump args = %q", call)
		}
		if call := r.main.CallsTo(t, "backup")[0]; !hasArgs(call, "--stdin-from-command", "--stdin-filename", "dump.sql") || !hasArgs(call, "--tag", "pre-restore") {
			t.Errorf("the safety snapshot is a pipe backup: %q", call)
		}
	})

	t.Run("needs confirmation", func(t *testing.T) {
		r := setup(t)
		res := r.e.Restore(t.Context(), RestoreRequest{Snapshot: "latest"}, r.sink())
		if res.Result != service.ResultFailure || len(r.main.CallsTo(t, "dump")) != 0 {
			t.Errorf("result = %+v", res)
		}
	})

	t.Run("a failing dump fails the restore", func(t *testing.T) {
		r := setup(t)
		r.main.On(t, "dump", resticfake.Response{Stdout: "partial", Stderr: "Fatal: pack missing\n", Exit: 1})
		res := r.e.Restore(t.Context(), RestoreRequest{Snapshot: "latest", Confirm: "latest", NoSafetySnapshot: true}, r.sink())
		if res.Result != service.ResultFailure || !strings.Contains(res.Error, "pack missing") {
			t.Errorf("a truncated dump must never count as restored: %+v", res)
		}
	})

	t.Run("a dead database blocks the restore until the safety snapshot is skipped", func(t *testing.T) {
		r := setup(t)
		r.main.On(t, "backup", resticfake.Response{Stderr: "Fatal: command failed\n", Exit: 1})
		res := r.e.Restore(t.Context(), RestoreRequest{Snapshot: "latest", Confirm: "latest"}, r.sink())
		if res.Result != service.ResultFailure || len(r.main.CallsTo(t, "dump")) != 0 {
			t.Errorf("result = %+v", res)
		}
	})

	t.Run("no target or include paths", func(t *testing.T) {
		r := setup(t)
		res := r.e.Restore(t.Context(), RestoreRequest{Snapshot: "latest", Confirm: "latest", Target: t.TempDir()}, r.sink())
		if res.Result != service.ResultFailure {
			t.Errorf("result = %+v", res)
		}
	})
}

func TestRestoreIfEmpty(t *testing.T) {
	log := filepath.Join(t.TempDir(), "hooks.log")
	pre := script(t, `echo "pre [$*]" >> "$HOOKLOG"`)
	post := script(t, `echo "post [$*]" >> "$HOOKLOG"`)

	t.Run("a target with data is left alone without contacting the repository", func(t *testing.T) {
		r := restoreRig(t)
		action, err := r.e.RestoreIfEmpty(t.Context(), r.sink())
		if err != nil || action != ActionTargetNotEmpty {
			t.Fatalf("action = %q, %v", action, err)
		}
		if len(r.main.Calls(t)) != 0 {
			t.Errorf("restic was called: %q", r.main.Calls(t))
		}
	})

	t.Run("restores the latest regular snapshot", func(t *testing.T) {
		_ = os.Remove(log)
		r := restoreRig(t, "HOOKLOG="+log, "PRE_RESTORE_SCRIPT="+pre, "POST_RESTORE_SCRIPT="+post)
		_ = os.RemoveAll(filepath.Join(r.root, "data"))
		writeFile(t, filepath.Join(r.root, "lost+found", "x"), 1)
		bad := snap("bad", "me", time.Minute, 1, 1, rootPaths(r), "suspicious")
		good := snap("snap1", "me", time.Hour, 3, 1000, rootPaths(r), "mcs-backup")
		other := snap("foreign", "other-host", time.Second, 3, 1000, rootPaths(r))
		r.main.On(t, "snapshots", resticfake.Response{Stdout: snapsJSON(t, good, bad, other)})

		action, err := r.e.RestoreIfEmpty(t.Context(), r.sink())
		if err != nil || action != ActionRestored {
			t.Fatalf("action = %q, %v", action, err)
		}
		call := r.main.CallsTo(t, "restore")[0]
		if !hasArgs(call, "snap1", "--json") || slices.Contains(call, "--delete") {
			t.Errorf("restore args = %q", call)
		}
		if len(r.main.CallsTo(t, "backup")) != 0 {
			t.Error("there is nothing to take a safety snapshot of")
		}
		if hookLog, _ := os.ReadFile(log); string(hookLog) != "pre []\npost []\n" {
			t.Errorf("hook log = %q", hookLog)
		}
	})

	t.Run("a missing repository is a fresh installation", func(t *testing.T) {
		r := restoreRig(t)
		_ = os.RemoveAll(filepath.Join(r.root, "data"))
		r.main.On(t, "snapshots", resticfake.Response{Stderr: "Fatal: repository does not exist\n", Exit: 10})
		action, err := r.e.RestoreIfEmpty(t.Context(), r.sink())
		if err != nil || action != ActionNoRepository {
			t.Fatalf("action = %q, %v", action, err)
		}
		if len(r.main.CallsTo(t, "init")) != 0 {
			t.Error("restore-if-empty never creates a repository")
		}
	})

	t.Run("no snapshots is a fresh installation too", func(t *testing.T) {
		r := restoreRig(t)
		_ = os.RemoveAll(filepath.Join(r.root, "data"))
		r.main.On(t, "snapshots", resticfake.Response{Stdout: snapsJSON(t, snap("bad", "me", time.Hour, 1, 1, nil, "suspicious"))})
		action, err := r.e.RestoreIfEmpty(t.Context(), r.sink())
		if err != nil || action != ActionNoSnapshot {
			t.Errorf("action = %q, %v", action, err)
		}
	})

	t.Run("an unreachable repository is an error", func(t *testing.T) {
		r := restoreRig(t)
		_ = os.RemoveAll(filepath.Join(r.root, "data"))
		r.main.On(t, "snapshots", resticfake.Response{Stderr: "Fatal: connection refused\n", Exit: 1})
		if _, err := r.e.RestoreIfEmpty(t.Context(), r.sink()); err == nil || !strings.Contains(err.Error(), "connection refused") {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("a failing restore is an error", func(t *testing.T) {
		r := restoreRig(t)
		_ = os.RemoveAll(filepath.Join(r.root, "data"))
		r.main.On(t, "restore", resticfake.Response{Stderr: "Fatal: disk full\n", Exit: 1})
		if _, err := r.e.RestoreIfEmpty(t.Context(), r.sink()); err == nil || !strings.Contains(err.Error(), "disk full") {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("pipe mode is not supported", func(t *testing.T) {
		in, out := script(t, "echo"), script(t, "cat")
		r := newRig(t, false, "PIPE_IN_SCRIPT="+in, "PIPE_OUT_SCRIPT="+out)
		if _, err := r.e.RestoreIfEmpty(t.Context(), r.sink()); !errors.Is(err, ErrPipeMode) {
			t.Errorf("err = %v", err)
		}
		if len(r.main.Calls(t)) != 0 {
			t.Error("restic was called")
		}
	})
}
