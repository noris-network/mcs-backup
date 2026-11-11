package engine

import (
	"context"
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

func TestBackupSuccess(t *testing.T) {
	log := filepath.Join(t.TempDir(), "hooks.log")
	pre := script(t, `echo "pre in $(basename "$(pwd)")" >> "$HOOKLOG"`)
	post := script(t, `echo "post $MCS_BACKUP_RESULT $MCS_BACKUP_SNAPSHOT [$MCS_BACKUP_ERROR]" >> "$HOOKLOG"`)
	r := newRig(t, false, "HOOKLOG="+log, "PRE_BACKUP_SCRIPT="+pre, "POST_BACKUP_SCRIPT="+post, "EXCLUDE_PATHS=*.tmp")

	existing := snap("old1", "me", 24*time.Hour, 3, 1000, rootPaths(r), "mcs-backup", "scheduled")
	other := snap("other", "me", time.Hour, 3, 1000, []string{"/elsewhere"})
	r.main.On(t, "snapshots", resticfake.Response{Stdout: snapsJSON(t, existing, other)})
	r.main.On(t, "backup", resticfake.Response{Stdout: summaryLine("new1", 3, 1000)})
	r.main.On(t, "forget", resticfake.Response{Stdout: forgetJSON(t, 10, 0)})

	var progress []service.Progress
	sink := r.sink()
	sink.Progress = func(p service.Progress) { progress = append(progress, p) }
	res := r.e.Backup(t.Context(), service.BackupRequest{Origin: service.OriginScheduled, Note: "nightly, ok", Keep: true}, sink)

	if res.Result != service.ResultSuccess || res.Error != "" || res.Snapshot != "new1" {
		t.Fatalf("result = %+v", res)
	}
	call := r.main.CallsTo(t, "backup")[0]
	for _, want := range [][]string{
		{"--host", "me"}, {"--parent", "old1"}, {"--tag", "mcs-backup"}, {"--tag", "scheduled"},
		{"--tag", "keep"}, {"--tag", "note:nightly; ok"}, {"--exclude", "*.tmp"},
	} {
		if !hasArgs(call, want...) {
			t.Errorf("backup args lack %q: %q", want, call)
		}
	}
	if call[len(call)-1] != "." {
		t.Errorf("paths = %q", call[len(call)-1])
	}
	if got := r.main.CallsTo(t, "forget"); len(got) != 1 || !slices.Contains(got[0], "--dry-run") {
		t.Errorf("forget calls = %q: nothing to remove, so only the preview runs", got)
	}
	if hostArgs := r.main.CallsTo(t, "forget")[0]; !hasArgs(hostArgs, "--host", "me") || slices.Contains(hostArgs, "dummy") {
		t.Errorf("forget args = %q", hostArgs)
	}
	if !slices.Equal(r.phases, []string{"prepare", "backup", "forget", "wrapup"}) {
		t.Errorf("phases = %q", r.phases)
	}
	if len(progress) != 1 || progress[0].FilesTotal != 3 || progress[0].Current != "/x" {
		t.Errorf("progress = %+v", progress)
	}
	hookLog, _ := os.ReadFile(log)
	rootName := filepath.Base(r.root)
	if want := "pre in " + rootName + "\npost success new1 []\n"; string(hookLog) != want {
		t.Errorf("hook log = %q, want %q", hookLog, want)
	}
	if !strings.Contains(r.logText(), "using snapshot old1 as parent") {
		t.Errorf("log:\n%s", r.logText())
	}
}

func TestBackupForgetRemoves(t *testing.T) {
	r := newRig(t, false)
	r.main.On(t, "snapshots", resticfake.Response{Stdout: snapsJSON(t)})
	r.main.On(t, "backup", resticfake.Response{Stdout: summaryLine("new1", 3, 1000)})
	r.main.On(t, "forget", resticfake.Response{Stdout: forgetJSON(t, 10, 1)}, resticfake.Response{Stdout: forgetJSON(t, 10, 1)})

	res := r.e.Backup(t.Context(), service.BackupRequest{}, r.sink())
	if res.Result != service.ResultSuccess || !res.Forget.Ran || res.Forget.Removed != 1 || res.Forget.Held {
		t.Fatalf("result = %+v", res)
	}
	calls := r.main.CallsTo(t, "forget")
	if len(calls) != 2 || !slices.Contains(calls[0], "--dry-run") || slices.Contains(calls[1], "--dry-run") {
		t.Fatalf("forget calls = %q", calls)
	}
	if !hasArgs(calls[1], "--keep-tag", "keep") || !hasArgs(calls[1], "--keep-last", "10") || !hasArgs(calls[1], "--group-by", "paths") {
		t.Errorf("forget args = %q", calls[1])
	}
	if call := r.main.CallsTo(t, "backup")[0]; hasArgs(call, "--parent") || slices.Contains(call, "--parent") {
		t.Errorf("no parent expected without own snapshots: %q", call)
	}
	if call := r.main.CallsTo(t, "backup")[0]; !hasArgs(call, "--tag", "manual") {
		t.Errorf("the default origin is manual: %q", call)
	}
}

func TestBackupForgetFailureIsLogged(t *testing.T) {
	r := newRig(t, false)
	r.main.On(t, "snapshots", resticfake.Response{Stdout: snapsJSON(t)})
	r.main.On(t, "backup", resticfake.Response{Stdout: summaryLine("new1", 3, 1000)})
	r.main.On(t, "forget", resticfake.Response{Stdout: forgetJSON(t, 10, 1)},
		resticfake.Response{Stderr: "unable to create lock in backend: repository is already locked by PID 7\n", Exit: 11})

	res := r.e.Backup(t.Context(), service.BackupRequest{}, r.sink())
	if res.Result != service.ResultWarning || len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "forget on the main repository failed") {
		t.Fatalf("result = %+v", res)
	}
	// the log has the reason in full, also where a result is shown cut short
	if log := r.logText(); !strings.Contains(log, "forget on the main repository failed") || !strings.Contains(log, "already locked by PID 7") {
		t.Errorf("log:\n%s", log)
	}
}

func TestBackupLegacySnapshots(t *testing.T) {
	r := newRig(t, false)
	legacy := snap("legacy1", service.LegacyHost, 48*time.Hour, 3, 1000, rootPaths(r))
	r.main.On(t, "snapshots", resticfake.Response{Stdout: snapsJSON(t, legacy)})
	r.main.On(t, "backup", resticfake.Response{Stdout: summaryLine("new1", 3, 1000)})
	r.main.On(t, "forget", resticfake.Response{Stdout: forgetJSON(t, 2, 0)})

	res := r.e.Backup(t.Context(), service.BackupRequest{}, r.sink())
	if res.Result != service.ResultSuccess {
		t.Fatalf("result = %+v", res)
	}
	if call := r.main.CallsTo(t, "backup")[0]; !hasArgs(call, "--parent", "legacy1") {
		t.Errorf("the 1.x snapshot must be the parent, otherwise everything is rescanned: %q", call)
	}
	if call := r.main.CallsTo(t, "forget")[0]; !hasArgs(call, "--host", "me", "--host", "dummy") {
		t.Errorf("1.x snapshots are cleaned up too: %q", call)
	}
}

func TestBackupRetentionGuard(t *testing.T) {
	r := newRig(t, false)
	r.main.On(t, "snapshots", resticfake.Response{Stdout: snapsJSON(t)})
	r.main.On(t, "backup", resticfake.Response{Stdout: summaryLine("new1", 3, 1000)})
	// 4 of 12 would be removed: more than 2 and more than 25%
	r.main.On(t, "forget", resticfake.Response{Stdout: forgetJSON(t, 8, 4)}, resticfake.Response{Stdout: forgetJSON(t, 8, 4)})

	res := r.e.Backup(t.Context(), service.BackupRequest{}, r.sink())
	if res.Result != service.ResultWarning || !res.Forget.Held || res.Forget.Removed != 4 || res.Forget.Total != 12 || res.Forget.Ran {
		t.Fatalf("result = %+v", res)
	}
	if !slices.ContainsFunc(res.Warnings, func(w string) bool { return strings.Contains(w, "held back") }) {
		t.Errorf("warnings = %q", res.Warnings)
	}
	if got := r.main.CallsTo(t, "forget"); len(got) != 1 {
		t.Fatalf("forget calls = %q: only the preview may run", got)
	}

	approved := r.e.ApproveForget(t.Context(), r.sink())
	if approved.Result != service.ResultSuccess || !approved.Main.Ran || approved.Main.Removed != 4 {
		t.Fatalf("approve = %+v", approved)
	}
	calls := r.main.CallsTo(t, "forget")
	if len(calls) != 2 || slices.Contains(calls[1], "--dry-run") {
		t.Errorf("approval must run the real forget without a preview: %q", calls)
	}
}

func TestGuardTrips(t *testing.T) {
	tests := []struct {
		total, remove int
		percent       float64
		want          bool
	}{
		{12, 4, 25, true},
		{12, 3, 25, false}, // exactly 25%
		{100, 3, 25, false},
		{100, 26, 25, true},
		{4, 2, 25, false}, // 50%, but not more than 2 snapshots
		{3, 3, 25, true},
		{10, 0, 25, false},
		{10, 10, 100, false},
		{10, 3, 0, true},
	}
	for _, tt := range tests {
		if got := guardTrips(tt.total, tt.remove, tt.percent); got != tt.want {
			t.Errorf("guardTrips(%d, %d, %v) = %v, want %v", tt.total, tt.remove, tt.percent, got, tt.want)
		}
	}
}

func TestBackupIncomplete(t *testing.T) {
	r := newRig(t, false)
	r.main.On(t, "snapshots", resticfake.Response{Stdout: snapsJSON(t)})
	r.main.On(t, "backup", resticfake.Response{
		Stdout: summaryLine("new1", 3, 1000),
		Stderr: `{"message_type":"error","error":{"message":"open /data/locked: permission denied"},"during":"archival","item":"/data/locked"}` + "\n" +
			`{"message_type":"exit_error","code":3,"message":"Warning: at least one source file could not be read"}` + "\n",
		Exit: 3,
	})
	r.main.On(t, "forget", resticfake.Response{Stdout: forgetJSON(t, 3, 0)})

	res := r.e.Backup(t.Context(), service.BackupRequest{}, r.sink())
	if res.Result != service.ResultWarning || res.Snapshot != "new1" || res.Error != "" {
		t.Fatalf("result = %+v", res)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "permission denied") {
		t.Errorf("warnings = %q", res.Warnings)
	}
	if len(r.main.CallsTo(t, "forget")) != 1 {
		t.Error("the snapshot is valid, so forget still runs")
	}
}

func TestBackupFailureStillRunsPostHook(t *testing.T) {
	log := filepath.Join(t.TempDir(), "hooks.log")
	post := script(t, `echo "post $MCS_BACKUP_RESULT [$MCS_BACKUP_ERROR]" >> "$HOOKLOG"`)
	r := newRig(t, false, "HOOKLOG="+log, "POST_BACKUP_SCRIPT="+post)
	r.main.On(t, "snapshots", resticfake.Response{Stdout: snapsJSON(t)})
	r.main.On(t, "backup", resticfake.Response{Stderr: "Fatal: unable to save snapshot\n", Exit: 1})

	res := r.e.Backup(t.Context(), service.BackupRequest{}, r.sink())
	if res.Result != service.ResultFailure || !strings.Contains(res.Error, "unable to save snapshot") || res.Snapshot != "" {
		t.Fatalf("result = %+v", res)
	}
	if len(r.main.CallsTo(t, "forget")) != 0 {
		t.Error("a failed backup must not forget anything")
	}
	hookLog, _ := os.ReadFile(log)
	if !strings.HasPrefix(string(hookLog), "post failure [restic backup: exit code 1: Fatal: unable to save snapshot") {
		t.Errorf("hook log = %q", hookLog)
	}
}

func TestBackupPreHookFails(t *testing.T) {
	log := filepath.Join(t.TempDir(), "hooks.log")
	pre := script(t, `echo "database is down" >&2; exit 7`)
	post := script(t, `echo "post $MCS_BACKUP_RESULT" >> "$HOOKLOG"`)
	r := newRig(t, false, "HOOKLOG="+log, "PRE_BACKUP_SCRIPT="+pre, "POST_BACKUP_SCRIPT="+post)

	res := r.e.Backup(t.Context(), service.BackupRequest{}, r.sink())
	if res.Result != service.ResultFailure || !strings.Contains(res.Error, "pre-backup hook") || !strings.Contains(res.Error, "database is down") {
		t.Fatalf("result = %+v", res)
	}
	if len(r.main.Calls(t)) != 0 {
		t.Errorf("restic must not run: %q", r.main.Calls(t))
	}
	if hookLog, _ := os.ReadFile(log); string(hookLog) != "post failure\n" {
		t.Errorf("hook log = %q", hookLog)
	}
}

func TestBackupPostHookFailureIsAWarning(t *testing.T) {
	post := script(t, `echo "cannot reach monitoring" >&2; exit 1`)
	r := newRig(t, false, "POST_BACKUP_SCRIPT="+post)
	r.main.On(t, "snapshots", resticfake.Response{Stdout: snapsJSON(t)})
	r.main.On(t, "backup", resticfake.Response{Stdout: summaryLine("new1", 3, 1000)})
	r.main.On(t, "forget", resticfake.Response{Stdout: forgetJSON(t, 1, 0)})

	res := r.e.Backup(t.Context(), service.BackupRequest{}, r.sink())
	if res.Result != service.ResultWarning || res.Snapshot != "new1" || !strings.Contains(strings.Join(res.Warnings, "\n"), "post-backup hook failed") {
		t.Fatalf("a broken post hook must not turn a good backup into a failure: %+v", res)
	}
}

func TestBackupPlausibility(t *testing.T) {
	tests := []struct {
		name         string
		env          []string
		existing     func(r *rig) []restic.Snapshot
		files, bytes int64
		wantResult   service.Result
		wantTagged   bool
		wantRemoved  bool
		wantForget   bool
	}{
		{
			name: "empty with a previous snapshot fails and is tagged",
			existing: func(r *rig) []restic.Snapshot {
				return []restic.Snapshot{snap("old1", "me", time.Hour, 5, 1000, rootPaths(r))}
			},
			files: 0, bytes: 0, wantResult: service.ResultFailure, wantTagged: true,
		},
		{
			name:     "empty on a fresh install is skipped and removed",
			existing: func(*rig) []restic.Snapshot { return nil },
			files:    0, bytes: 0, wantResult: service.ResultSkipped, wantRemoved: true,
		},
		{
			name: "empty is fine when explicitly allowed",
			env:  []string{"ALLOW_EMPTY_SNAPSHOT=true"},
			existing: func(r *rig) []restic.Snapshot {
				return []restic.Snapshot{snap("old1", "me", time.Hour, 5, 1000, rootPaths(r))}
			},
			files: 0, bytes: 0, wantResult: service.ResultSuccess, wantForget: true,
		},
		{
			name: "a previous snapshot that is itself suspicious does not count",
			existing: func(r *rig) []restic.Snapshot {
				return []restic.Snapshot{snap("bad", "me", time.Hour, 5, 1000, rootPaths(r), "suspicious")}
			},
			files: 0, bytes: 0, wantResult: service.ResultSkipped, wantRemoved: true,
		},
		{
			name: "shrunk by more than 50 percent is suspicious",
			existing: func(r *rig) []restic.Snapshot {
				return []restic.Snapshot{snap("old1", "me", time.Hour, 5, 1000, rootPaths(r))}
			},
			files: 2, bytes: 400, wantResult: service.ResultSuspicious, wantTagged: true,
		},
		{
			name: "shrunk by less than 50 percent is fine",
			existing: func(r *rig) []restic.Snapshot {
				return []restic.Snapshot{snap("old1", "me", time.Hour, 5, 1000, rootPaths(r))}
			},
			files: 2, bytes: 600, wantResult: service.ResultSuccess, wantForget: true,
		},
		{
			name: "the baseline is the latest regular snapshot",
			existing: func(r *rig) []restic.Snapshot {
				return []restic.Snapshot{
					snap("old1", "me", 2*time.Hour, 5, 1000, rootPaths(r)),
					snap("bad", "me", time.Hour, 5, 100, rootPaths(r), "suspicious"),
				}
			},
			files: 2, bytes: 400, wantResult: service.ResultSuspicious, wantTagged: true,
		},
		{
			name: "snapshots without statistics are no baseline",
			existing: func(r *rig) []restic.Snapshot {
				s := snap("old1", "me", time.Hour, 5, 1000, rootPaths(r))
				s.Summary = nil
				return []restic.Snapshot{s}
			},
			files: 2, bytes: 1, wantResult: service.ResultSuccess, wantForget: true,
		},
		{
			name: "the check can be switched off",
			env:  []string{"BACKUP_MAX_SHRINK=0"},
			existing: func(r *rig) []restic.Snapshot {
				return []restic.Snapshot{snap("old1", "me", time.Hour, 5, 1000, rootPaths(r))}
			},
			files: 2, bytes: 1, wantResult: service.ResultSuccess, wantForget: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t, false, tt.env...)
			existing := tt.existing(r)
			newSnap := snap("new1", "me", 0, tt.files, tt.bytes, rootPaths(r))
			r.main.On(t, "snapshots", resticfake.Response{Stdout: snapsJSON(t, existing...)}, resticfake.Response{Stdout: snapsJSON(t, append(existing, newSnap)...)})
			r.main.On(t, "backup", resticfake.Response{Stdout: summaryLine("new1", tt.files, tt.bytes)})
			r.main.On(t, "tag", resticfake.Response{Stdout: `{"message_type":"changed","old_snapshot_id":"new1","new_snapshot_id":"new1-tagged"}` + "\n"})
			r.main.On(t, "forget", resticfake.Response{Stdout: forgetJSON(t, 3, 0)})

			res := r.e.Backup(t.Context(), service.BackupRequest{}, r.sink())
			if res.Result != tt.wantResult {
				t.Fatalf("result = %s, want %s: %+v", res.Result, tt.wantResult, res)
			}
			tagCalls := r.main.CallsTo(t, "tag")
			if tt.wantTagged {
				if len(tagCalls) != 1 || !hasArgs(tagCalls[0], "--add", "suspicious") {
					t.Fatalf("tag calls = %q", tagCalls)
				}
				if res.Snapshot != "new1-tagged" || !res.Suspicious {
					t.Errorf("snapshot = %q, suspicious = %v", res.Snapshot, res.Suspicious)
				}
			} else if len(tagCalls) != 0 {
				t.Errorf("unexpected tag calls: %q", tagCalls)
			}
			removeCalls := 0
			for _, c := range r.main.CallsTo(t, "forget") {
				if slices.Contains(c, "new1") {
					removeCalls++
				}
			}
			if tt.wantRemoved != (removeCalls == 1) || tt.wantRemoved && res.Snapshot != "" {
				t.Errorf("removal of the empty snapshot: calls = %d, snapshot = %q", removeCalls, res.Snapshot)
			}
			if forgot := len(r.main.CallsTo(t, "forget")) - removeCalls; tt.wantForget != (forgot > 0) {
				t.Errorf("forget calls = %d, want forget = %v", forgot, tt.wantForget)
			}
		})
	}
}

func TestBackupWhenResticRefusesAnEmptySnapshot(t *testing.T) {
	tests := []struct {
		name       string
		env        []string
		existing   func(r *rig) []restic.Snapshot
		wantResult service.Result
	}{
		{"fresh install", nil, func(*rig) []restic.Snapshot { return nil }, service.ResultSkipped},
		{"with a previous snapshot", nil, func(r *rig) []restic.Snapshot {
			return []restic.Snapshot{snap("old1", "me", time.Hour, 5, 1000, rootPaths(r))}
		}, service.ResultFailure},
		{"explicitly allowed", []string{"ALLOW_EMPTY_SNAPSHOT=true"}, func(r *rig) []restic.Snapshot {
			return []restic.Snapshot{snap("old1", "me", time.Hour, 5, 1000, rootPaths(r))}
		}, service.ResultSkipped},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t, false, tt.env...)
			r.main.On(t, "snapshots", resticfake.Response{Stdout: snapsJSON(t, tt.existing(r)...)})
			r.main.On(t, "backup", resticfake.Response{Stderr: "Fatal: unable to save snapshot: snapshot is empty\n", Exit: 1})

			res := r.e.Backup(t.Context(), service.BackupRequest{}, r.sink())
			if res.Result != tt.wantResult || res.Snapshot != "" {
				t.Fatalf("result = %+v, want %s without a snapshot", res, tt.wantResult)
			}
			for _, c := range []string{"tag", "forget"} {
				if calls := r.main.CallsTo(t, c); len(calls) != 0 {
					t.Errorf("%s was called although restic stored nothing: %q", c, calls)
				}
			}
		})
	}
}

func TestBackupPlausibilityPipeMode(t *testing.T) {
	in := script(t, "echo dump")
	out := script(t, "cat > /dev/null")
	r := newRig(t, false, "PIPE_IN_SCRIPT="+in, "PIPE_OUT_SCRIPT="+out, "PIPE_IN_FILENAME=dump.sql")
	existing := snap("old1", "me", time.Hour, 1, 1000, []string{"/dump.sql"})
	r.main.On(t, "snapshots", resticfake.Response{Stdout: snapsJSON(t, existing)}, resticfake.Response{Stdout: snapsJSON(t, existing)})
	// one file (the dump) but no bytes: a pipe backup is empty when it has no bytes
	r.main.On(t, "backup", resticfake.Response{Stdout: summaryLine("new1", 1, 0)})
	r.main.On(t, "tag", resticfake.Response{Stdout: `{"message_type":"changed","old_snapshot_id":"new1","new_snapshot_id":"new2"}` + "\n"})

	res := r.e.Backup(t.Context(), service.BackupRequest{}, r.sink())
	if res.Result != service.ResultFailure {
		t.Fatalf("result = %+v", res)
	}
	call := r.main.CallsTo(t, "backup")[0]
	want := []string{"--stdin-from-command", "--stdin-filename", "dump.sql", "--", in}
	if !hasArgs(call, want...) || !hasArgs(call, "--parent", "old1") {
		t.Errorf("backup args = %q", call)
	}
}

func TestBackupCopy(t *testing.T) {
	r := newRig(t, true)
	r.main.On(t, "snapshots", resticfake.Response{Stdout: snapsJSON(t)})
	r.main.On(t, "backup", resticfake.Response{Stdout: summaryLine("new1", 3, 1000)})
	r.main.On(t, "forget", resticfake.Response{Stdout: forgetJSON(t, 3, 0)})
	r.second.On(t, "copy", resticfake.Response{Stdout: "snapshot copied\n"})
	r.second.On(t, "snapshots", resticfake.Response{Stdout: snapsJSON(t)})
	r.second.On(t, "forget", resticfake.Response{Stdout: forgetJSON(t, 1, 0)})

	res := r.e.Backup(t.Context(), service.BackupRequest{}, r.sink())
	if res.Result != service.ResultSuccess || res.Copy == nil || res.Copy.Result != service.ResultSuccess {
		t.Fatalf("result = %+v", res)
	}
	if call := r.second.CallsTo(t, "copy")[0]; !slices.Equal(call, []string{"copy", "--", "new1"}) {
		t.Errorf("copy args = %q", call)
	}
	env := r.second.LastEnv(t)
	if env["RESTIC_REPOSITORY"] != "/main" && env["RESTIC_FROM_REPOSITORY"] != "/main" {
		t.Errorf("copy env = %v", env)
	}
	if len(r.second.CallsTo(t, "forget")) != 1 {
		t.Error("the secondary repository gets the same retention")
	}
	if !slices.Contains(r.phases, "copy") {
		t.Errorf("phases = %q", r.phases)
	}
}

func TestBackupCopyFailureIsAWarning(t *testing.T) {
	r := newRig(t, true)
	r.main.On(t, "snapshots", resticfake.Response{Stdout: snapsJSON(t)})
	r.main.On(t, "backup", resticfake.Response{Stdout: summaryLine("new1", 3, 1000)})
	r.main.On(t, "forget", resticfake.Response{Stdout: forgetJSON(t, 3, 0)})
	r.second.On(t, "copy", resticfake.Response{Stderr: "Fatal: connection refused\n", Exit: 1})

	res := r.e.Backup(t.Context(), service.BackupRequest{}, r.sink())
	if res.Result != service.ResultWarning || res.Snapshot != "new1" || res.Copy == nil || res.Copy.Result != service.ResultFailure ||
		!strings.Contains(res.Copy.Error, "connection refused") {
		t.Fatalf("result = %+v", res)
	}
	if len(r.second.CallsTo(t, "forget")) != 0 {
		t.Error("no forget on a repository that did not receive the snapshot")
	}
}

func TestBackupSuspiciousIsStillCopied(t *testing.T) {
	r := newRig(t, true)
	existing := snap("old1", "me", time.Hour, 5, 1000, rootPaths(r))
	r.main.On(t, "snapshots", resticfake.Response{Stdout: snapsJSON(t, existing)},
		resticfake.Response{Stdout: snapsJSON(t, existing, snap("new1", "me", 0, 2, 100, rootPaths(r)))})
	r.main.On(t, "backup", resticfake.Response{Stdout: summaryLine("new1", 2, 100)})
	r.main.On(t, "tag", resticfake.Response{Stdout: `{"message_type":"changed","old_snapshot_id":"new1","new_snapshot_id":"new2"}` + "\n"})
	r.second.On(t, "copy", resticfake.Response{})

	res := r.e.Backup(t.Context(), service.BackupRequest{}, r.sink())
	if res.Result != service.ResultSuspicious || res.Copy == nil || res.Copy.Result != service.ResultSuccess {
		t.Fatalf("result = %+v", res)
	}
	if call := r.second.CallsTo(t, "copy")[0]; !slices.Equal(call, []string{"copy", "--", "new2"}) {
		t.Errorf("the tagged snapshot is copied: %q", call)
	}
	if len(r.main.CallsTo(t, "forget")) != 0 || len(r.second.CallsTo(t, "forget")) != 0 {
		t.Error("nothing is forgotten after a suspicious backup")
	}
}

func TestBackupCancelAndTimeout(t *testing.T) {
	log := filepath.Join(t.TempDir(), "hooks.log")
	post := script(t, `echo "post $MCS_BACKUP_RESULT" >> "$HOOKLOG"`)

	t.Run("cancel", func(t *testing.T) {
		_ = os.Remove(log)
		r := newRig(t, false, "HOOKLOG="+log, "POST_BACKUP_SCRIPT="+post)
		r.main.On(t, "snapshots", resticfake.Response{Stdout: snapsJSON(t)})
		r.main.On(t, "backup", resticfake.Response{Sleep: 30 * time.Second})
		ctx, cancel := context.WithCancel(t.Context())
		time.AfterFunc(300*time.Millisecond, cancel)

		start := time.Now()
		res := r.e.Backup(ctx, service.BackupRequest{}, r.sink())
		if res.Result != service.ResultCancelled {
			t.Fatalf("result = %+v", res)
		}
		if time.Since(start) > 10*time.Second {
			t.Error("cancel took too long")
		}
		if hookLog, _ := os.ReadFile(log); string(hookLog) != "post cancelled\n" {
			t.Errorf("the post hook runs after a cancel and learns about it: %q", hookLog)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		_ = os.Remove(log)
		r := newRig(t, false, "HOOKLOG="+log, "POST_BACKUP_SCRIPT="+post, "BACKUP_TIMEOUT=300ms")
		r.main.On(t, "snapshots", resticfake.Response{Stdout: snapsJSON(t)})
		r.main.On(t, "backup", resticfake.Response{Sleep: 30 * time.Second})

		res := r.e.Backup(t.Context(), service.BackupRequest{}, r.sink())
		if res.Result != service.ResultTimeout {
			t.Fatalf("result = %+v", res)
		}
		if hookLog, _ := os.ReadFile(log); string(hookLog) != "post timeout\n" {
			t.Errorf("the post hook has its own time limit and runs after a timeout: %q", hookLog)
		}
	})
}

func TestBackupAutoUnlock(t *testing.T) {
	setup := func(t *testing.T, lockAge time.Duration) *rig {
		r := newRig(t, false, "AUTO_UNLOCK_AFTER=1h")
		r.main.On(t, "snapshots", resticfake.Response{Stdout: snapsJSON(t)})
		r.main.On(t, "backup",
			resticfake.Response{Stderr: "unable to create lock in backend: repository is already locked\n", Exit: 11},
			resticfake.Response{Stdout: summaryLine("new1", 3, 1000)})
		r.main.On(t, "list locks", resticfake.Response{Stdout: "lock1\n"})
		r.main.On(t, "cat lock", resticfake.Response{Stdout: lockJSON(lockAge)})
		r.main.On(t, "unlock", resticfake.Response{})
		r.main.On(t, "forget", resticfake.Response{Stdout: forgetJSON(t, 3, 0)})
		return r
	}

	t.Run("old locks are removed", func(t *testing.T) {
		r := setup(t, 2*time.Hour)
		res := r.e.Backup(t.Context(), service.BackupRequest{}, r.sink())
		if res.Result != service.ResultSuccess {
			t.Fatalf("result = %+v", res)
		}
		if got := r.main.CallsTo(t, "unlock"); len(got) != 1 || !slices.Equal(got[0], []string{"unlock", "--remove-all"}) {
			t.Errorf("unlock calls = %q", got)
		}
		if len(r.main.CallsTo(t, "backup")) != 2 {
			t.Error("the backup is retried once")
		}
	})

	t.Run("young locks are left alone", func(t *testing.T) {
		r := setup(t, 5*time.Minute)
		res := r.e.Backup(t.Context(), service.BackupRequest{}, r.sink())
		if res.Result != service.ResultFailure || !strings.Contains(res.Error, "locked") {
			t.Fatalf("result = %+v", res)
		}
		if len(r.main.CallsTo(t, "unlock")) != 0 || len(r.main.CallsTo(t, "backup")) != 1 {
			t.Errorf("calls = %q", r.main.Calls(t))
		}
		if !strings.Contains(r.logText(), "not removing it") {
			t.Errorf("log = %s", r.logText())
		}
	})

	t.Run("disabled by default", func(t *testing.T) {
		r := newRig(t, false)
		r.main.On(t, "snapshots", resticfake.Response{Stdout: snapsJSON(t)})
		r.main.On(t, "backup", resticfake.Response{Stderr: "locked\n", Exit: 11})
		res := r.e.Backup(t.Context(), service.BackupRequest{}, r.sink())
		if res.Result != service.ResultFailure || len(r.main.CallsTo(t, "list")) != 0 {
			t.Errorf("result = %+v, calls = %q", res, r.main.Calls(t))
		}
	})
}

func TestBackupAppendOnly(t *testing.T) {
	env := []string{"APPEND_ONLY=true", "RETENTION_POLICY="}
	t.Run("never forgets", func(t *testing.T) {
		r := newRig(t, false, env...)
		r.main.On(t, "snapshots", resticfake.Response{Stdout: snapsJSON(t)})
		r.main.On(t, "backup", resticfake.Response{Stdout: summaryLine("new1", 3, 1000)})
		res := r.e.Backup(t.Context(), service.BackupRequest{}, r.sink())
		if res.Result != service.ResultSuccess || len(r.main.CallsTo(t, "forget")) != 0 {
			t.Fatalf("result = %+v, calls = %q", res, r.main.Calls(t))
		}
	})
	t.Run("suspicious is reported but cannot be tagged", func(t *testing.T) {
		r := newRig(t, false, env...)
		existing := snap("old1", "me", time.Hour, 5, 1000, rootPaths(r))
		r.main.On(t, "snapshots", resticfake.Response{Stdout: snapsJSON(t, existing)})
		r.main.On(t, "backup", resticfake.Response{Stdout: summaryLine("new1", 2, 100)})
		res := r.e.Backup(t.Context(), service.BackupRequest{}, r.sink())
		if res.Result != service.ResultSuspicious || !res.Suspicious || len(r.main.CallsTo(t, "tag")) != 0 {
			t.Fatalf("result = %+v, calls = %q", res, r.main.Calls(t))
		}
	})
	t.Run("tag changes are refused", func(t *testing.T) {
		r := newRig(t, false, env...)
		for _, f := range []func() error{
			func() error { _, err := r.e.Keep(t.Context(), "x"); return err },
			func() error { _, err := r.e.Unkeep(t.Context(), "x"); return err },
			func() error { _, err := r.e.Acknowledge(t.Context(), "x"); return err },
		} {
			if err := f(); !errors.Is(err, ErrAppendOnly) {
				t.Errorf("err = %v, want ErrAppendOnly", err)
			}
		}
		if res := r.e.ApproveForget(t.Context(), r.sink()); res.Result != service.ResultFailure {
			t.Errorf("approve = %+v", res)
		}
		if err := r.e.Unlock(t.Context(), true); !errors.Is(err, ErrAppendOnly) {
			t.Errorf("unlock --all: %v", err)
		}
	})
}

func TestBackupPostHookEndsOnHardStop(t *testing.T) {
	post := script(t, `sleep 30`)
	r := newRig(t, false, "POST_BACKUP_SCRIPT="+post)
	r.main.On(t, "snapshots", resticfake.Response{Stdout: snapsJSON(t)})
	r.main.On(t, "backup", resticfake.Response{Sleep: 30 * time.Second})

	stop := make(chan struct{})
	ctx, cancel := context.WithCancel(WithHardStop(t.Context(), stop))
	time.AfterFunc(300*time.Millisecond, cancel)                 // shutdown: the backup is cancelled
	time.AfterFunc(900*time.Millisecond, func() { close(stop) }) // the budget is used up

	start := time.Now()
	res := r.e.Backup(ctx, service.BackupRequest{}, r.sink())
	if res.Result != service.ResultCancelled {
		t.Fatalf("result = %+v", res)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("took %v: the post hook ignored the hard stop", d)
	}
	if !slices.ContainsFunc(res.Warnings, func(w string) bool { return strings.Contains(w, "post-backup hook failed") }) {
		t.Errorf("warnings = %q", res.Warnings)
	}
}
