package engine

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/noris-network/mcs-backup/v2/internal/restic"
	"github.com/noris-network/mcs-backup/v2/internal/resticfake"
	"github.com/noris-network/mcs-backup/v2/internal/service"
)

func tagged(id string) string {
	return `{"message_type":"changed","old_snapshot_id":"x","new_snapshot_id":"` + id + `"}` + "\n"
}

func TestResolve(t *testing.T) {
	r := newRig(t, false)
	snaps := []restic.Snapshot{
		snap("aaaa1111", "me", 3*time.Hour, 1, 1, nil),
		snap("aaaa2222", "me", 2*time.Hour, 1, 1, nil),
		snap("bbbb3333", "me", time.Hour, 1, 1, nil, "suspicious"),
		snap("cccc4444", "other", 30*time.Minute, 1, 1, nil),
	}
	tests := []struct {
		ref     string
		want    string
		wantErr string
	}{
		{"latest", "aaaa2222", ""},
		{"aaaa1111", "aaaa1111", ""},
		{"bb", "bbbb3333", ""},
		{"aaaa", "", "ambiguous"},
		{"zzzz", "", "no matching snapshot"},
		{"", "", "no matching snapshot"},
	}
	for _, tt := range tests {
		t.Run(tt.ref, func(t *testing.T) {
			got, err := r.e.resolve(snaps, tt.ref)
			if tt.wantErr != "" {
				if !errors.Is(err, ErrNoSnapshot) || !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || got.ID != tt.want {
				t.Errorf("resolve(%q) = %q, %v; want %q", tt.ref, got.ID, err, tt.want)
			}
		})
	}
	if _, err := r.e.resolve(nil, "latest"); !errors.Is(err, ErrNoSnapshot) {
		t.Errorf("latest without snapshots: %v", err)
	}
}

func TestKeepMirrorsToSecondFirst(t *testing.T) {
	r := newRig(t, true)
	src := snap("aaaa1111", "me", time.Hour, 1, 1, nil, "mcs-backup")
	copyOf := snap("bbbb2222", "me", time.Hour, 1, 1, nil, "mcs-backup")
	copyOf.Original = "aaaa1111"
	unrelated := snap("cccc3333", "me", time.Hour, 1, 1, nil)
	r.main.On(t, "snapshots", resticfake.Response{Stdout: snapsJSON(t, src)})
	r.main.On(t, "tag", resticfake.Response{Stdout: tagged("aaaa-new")})
	r.second.On(t, "snapshots", resticfake.Response{Stdout: snapsJSON(t, unrelated, copyOf)})
	r.second.On(t, "tag", resticfake.Response{Stdout: tagged("bbbb-new")})

	res, err := r.e.Keep(t.Context(), "aaaa")
	if err != nil || res.ID != "aaaa-new" || len(res.Warnings) != 0 {
		t.Fatalf("Keep() = %+v, %v", res, err)
	}
	if got := r.main.CallsTo(t, "tag"); len(got) != 1 || !hasArgs(got[0], "--add", "keep") || got[0][len(got[0])-1] != "aaaa1111" {
		t.Errorf("main tag calls = %q", got)
	}
	if got := r.second.CallsTo(t, "tag"); len(got) != 1 || !hasArgs(got[0], "--add", "keep") || got[0][len(got[0])-1] != "bbbb2222" {
		t.Errorf("the copy must get the same tag, matched through its original field: %q", got)
	}
}

func TestTagChangeWhenSecondaryFails(t *testing.T) {
	r := newRig(t, true)
	r.main.On(t, "snapshots", resticfake.Response{Stdout: snapsJSON(t, snap("aaaa1111", "me", time.Hour, 1, 1, nil, "suspicious"))})
	r.main.On(t, "tag", resticfake.Response{Stdout: tagged("aaaa-new")})
	r.second.On(t, "snapshots", resticfake.Response{Stderr: "Fatal: connection refused\n", Exit: 1})

	res, err := r.e.Acknowledge(t.Context(), "latest")
	// "latest" does not select a suspicious snapshot: use the ID
	if !errors.Is(err, ErrNoSnapshot) {
		t.Fatalf("latest must not resolve to a suspicious snapshot: %+v, %v", res, err)
	}
	res, err = r.e.Acknowledge(t.Context(), "aaaa1111")
	if err != nil || res.ID != "aaaa-new" {
		t.Fatalf("Acknowledge() = %+v, %v", res, err)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "mirrored") {
		t.Errorf("warnings = %q: a down secondary repository must not block the change", res.Warnings)
	}
	if got := r.main.CallsTo(t, "tag"); len(got) != 1 || !hasArgs(got[0], "--remove", "suspicious") {
		t.Errorf("tag calls = %q", got)
	}
}

func TestTagWithAppendOnlySecondary(t *testing.T) {
	r := newRig(t, true, "COPY_APPEND_ONLY=true")
	r.main.On(t, "snapshots", resticfake.Response{Stdout: snapsJSON(t, snap("aaaa1111", "me", time.Hour, 1, 1, nil))})
	r.main.On(t, "tag", resticfake.Response{Stdout: tagged("aaaa-new")})

	res, err := r.e.Unkeep(t.Context(), "aaaa1111")
	if err != nil || len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "append-only") {
		t.Fatalf("Unkeep() = %+v, %v", res, err)
	}
	if len(r.second.Calls(t)) != 0 {
		t.Errorf("the append-only repository must not be touched: %q", r.second.Calls(t))
	}
}

func TestTagOnRestic017FindsTheNewID(t *testing.T) {
	r := newRig(t, false)
	before := snap("aaaa1111", "me", time.Hour, 1, 1, rootPaths(r))
	rewritten := before
	rewritten.ID = "aaaa-rewritten"
	rewritten.Tags = []string{"keep"}
	r.main.On(t, "snapshots",
		resticfake.Response{Stdout: snapsJSON(t, before)}, // resolve
		resticfake.Response{Stdout: snapsJSON(t, before)}, // before the rewrite
		resticfake.Response{Stdout: snapsJSON(t, rewritten)})
	r.main.On(t, "tag", resticfake.Response{Stdout: "create exclusive lock for repository\nmodified tags on 1 snapshots\n"})

	res, err := r.e.Keep(t.Context(), "aaaa1111")
	if err != nil || res.ID != "aaaa-rewritten" {
		t.Fatalf("Keep() = %+v, %v: restic 0.17 does not report the new ID", res, err)
	}
}

func TestPruneAndCheck(t *testing.T) {
	r := newRig(t, true)
	r.main.On(t, "prune", resticfake.Response{Stdout: "pruned main\n"})
	r.second.On(t, "prune", resticfake.Response{Stdout: "pruned second\n"})
	r.main.On(t, "check", resticfake.Response{Stdout: "no errors found\n"})
	r.second.On(t, "check", resticfake.Response{Stderr: "Fatal: pack abc is damaged\n", Exit: 1})

	res := r.e.Prune(t.Context(), r.sink())
	if res.Result != service.ResultSuccess || len(res.Repos) != 2 {
		t.Fatalf("prune = %+v", res)
	}
	if !strings.Contains(r.logText(), "pruned main") || !strings.Contains(r.logText(), "pruned second") {
		t.Errorf("restic output belongs into the run log: %s", r.logText())
	}

	res = r.e.Check(t.Context(), r.sink())
	if res.Result != service.ResultFailure || res.Repos[0].Result != service.ResultSuccess ||
		res.Repos[1].Result != service.ResultFailure || !strings.Contains(res.Error, "damaged") {
		t.Fatalf("check = %+v", res)
	}
}

func TestMaintenanceAppendOnly(t *testing.T) {
	r := newRig(t, true, "APPEND_ONLY=true", "RETENTION_POLICY=", "COPY_APPEND_ONLY=true")
	r.main.On(t, "check", resticfake.Response{})
	r.second.On(t, "check", resticfake.Response{})

	res := r.e.Prune(t.Context(), r.sink())
	if res.Result != service.ResultSkipped || len(r.main.Calls(t)) != 0 || len(r.second.Calls(t)) != 0 {
		t.Errorf("prune on append-only repositories must do nothing: %+v", res)
	}
	if res := r.e.Check(t.Context(), r.sink()); res.Result != service.ResultSuccess {
		t.Errorf("check is read-only and works: %+v", res)
	}
}

func TestMaintenanceTimeout(t *testing.T) {
	r := newRig(t, false, "PRUNE_TIMEOUT=300ms")
	r.main.On(t, "prune", resticfake.Response{Sleep: 30 * time.Second})
	res := r.e.Prune(t.Context(), r.sink())
	if res.Result != service.ResultTimeout {
		t.Errorf("prune = %+v", res)
	}
}

func TestLocks(t *testing.T) {
	r := newRig(t, false)
	r.main.On(t, "list locks", resticfake.Response{Stdout: "young\nold\n"})
	r.main.On(t, "cat lock", resticfake.Response{Stdout: lockJSON(5 * time.Minute)}, resticfake.Response{Stdout: lockJSON(2 * time.Hour)})
	r.main.On(t, "unlock", resticfake.Response{})

	locks, err := r.e.Locks(t.Context())
	if err != nil || len(locks) != 2 {
		t.Fatalf("Locks() = %+v, %v", locks, err)
	}
	if locks[0].Stale || locks[0].Age != 5*time.Minute || !locks[1].Stale || locks[1].Age != 2*time.Hour {
		t.Errorf("locks = %+v", locks)
	}
	if err := r.e.Unlock(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	if got := r.main.CallsTo(t, "unlock"); !slices.Equal(got[0], []string{"unlock", "--remove-all"}) {
		t.Errorf("unlock = %q", got)
	}
}

func TestRetentionInfo(t *testing.T) {
	r := newRig(t, false)
	mine := snap("m1", "me", time.Hour, 1, 1, nil)
	foreign := snap("f1", "other", time.Hour, 1, 1, nil)
	r.main.On(t, "snapshots", resticfake.Response{Stdout: snapsJSON(t, mine, foreign)})
	r.main.On(t, "forget", resticfake.Response{Stdout: forgetJSON(t, 2, 4)})

	info, err := r.e.Retention(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if info.Policy != "the last 10 snapshots" || info.Foreign != 1 || info.Total != 6 || len(info.Remove) != 4 ||
		!info.Held || !slices.Equal(info.Hosts, []string{"me"}) {
		t.Errorf("info = %+v", info)
	}
	if !slices.IsSortedFunc(info.Remove, func(a, b service.Snapshot) int { return a.Time.Compare(b.Time) }) {
		t.Error("the snapshots to remove should be sorted by time")
	}
	call := r.main.CallsTo(t, "forget")[0]
	if !slices.Contains(call, "--dry-run") || !slices.Contains(call, "--no-lock") {
		t.Errorf("a preview must not lock the repository: %q", call)
	}
}

func TestOpen(t *testing.T) {
	t.Run("existing repository", func(t *testing.T) {
		r := newRig(t, false)
		r.main.On(t, "cat config", resticfake.Response{Stdout: `{"version":2,"id":"repo-id","chunker_polynomial":"x"}`})
		info, err := r.e.OpenMain(t.Context())
		if err != nil || info.ID != "repo-id" || info.Created {
			t.Fatalf("OpenMain() = %+v, %v", info, err)
		}
		if len(r.main.CallsTo(t, "init")) != 0 {
			t.Error("an existing repository must not be initialized")
		}
	})
	t.Run("missing repository is created", func(t *testing.T) {
		r := newRig(t, true)
		r.main.On(t, "cat config", resticfake.Response{Stderr: "Fatal: repository does not exist\n", Exit: 10})
		r.main.On(t, "init", resticfake.Response{Stdout: `{"message_type":"initialized","id":"new-id","repository":"/main"}`})
		r.second.On(t, "cat config", resticfake.Response{Stderr: "Fatal: repository does not exist\n", Exit: 10})
		r.second.On(t, "init", resticfake.Response{Stdout: `{"message_type":"initialized","id":"second-id"}`})

		info, err := r.e.OpenMain(t.Context())
		if err != nil || info.ID != "new-id" || !info.Created {
			t.Fatalf("OpenMain() = %+v, %v", info, err)
		}
		info, err = r.e.OpenSecond(t.Context())
		if err != nil || info.ID != "second-id" || !info.Created {
			t.Fatalf("OpenSecond() = %+v, %v", info, err)
		}
		if got := r.second.CallsTo(t, "init")[0]; !slices.Contains(got, "--copy-chunker-params") {
			t.Errorf("the secondary repository must share the chunker parameters: %q", got)
		}
	})
	t.Run("wrong password is not a reason to initialize", func(t *testing.T) {
		r := newRig(t, false)
		r.main.On(t, "cat config", resticfake.Response{Stderr: "Fatal: wrong password or no key found\n", Exit: 12})
		_, err := r.e.OpenMain(t.Context())
		if !errors.Is(err, restic.ErrWrongPassword) || len(r.main.CallsTo(t, "init")) != 0 {
			t.Errorf("err = %v, calls = %q", err, r.main.Calls(t))
		}
	})
	t.Run("no secondary repository", func(t *testing.T) {
		r := newRig(t, false)
		if _, err := r.e.OpenSecond(t.Context()); !errors.Is(err, ErrNoCopy) {
			t.Errorf("err = %v", err)
		}
	})
}

func TestCheckVersion(t *testing.T) {
	for out, wantErr := range map[string]bool{
		"restic 0.19.1 compiled with go1.26 on linux/amd64": false,
		"restic 0.17.0 compiled with go1.23 on linux/amd64": false,
		"restic 0.16.5 compiled with go1.22 on linux/amd64": true,
		"restic 1.0.0 compiled with go1.30 on linux/amd64":  false,
	} {
		r := newRig(t, false)
		r.main.On(t, "version", resticfake.Response{Stdout: out})
		_, err := r.e.CheckVersion(t.Context())
		if (err != nil) != wantErr {
			t.Errorf("%q: err = %v, wantErr %v", out, err, wantErr)
		}
	}
}
