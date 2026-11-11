package mcpserver_test

import (
	"context"
	"errors"
	"iter"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/noris-network/mcs-backup/v2/internal/mcpserver"
	"github.com/noris-network/mcs-backup/v2/internal/service"
	"github.com/noris-network/mcs-backup/v2/internal/servicetest"
)

var t0 = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

type agent struct {
	t    *testing.T
	fake *servicetest.Fake
	cs   *mcp.ClientSession
	init *mcp.InitializeResult
}

// connect starts a server with the given options and connects an agent to it
// over an in-memory transport.
func connect(t *testing.T, o mcpserver.Options, mod ...func(*servicetest.Fake, *service.Status)) *agent {
	t.Helper()
	fake := &servicetest.Fake{}
	st := service.Status{Version: "2.0.0", Host: "app", Repository: service.RepoStatus{Up: true}}
	for _, m := range mod {
		m(fake, &st)
	}
	fake.StatusFn = func(context.Context) (service.Status, error) { return st, nil }
	srv, err := mcpserver.New(t.Context(), fake, o)
	if err != nil {
		t.Fatal(err)
	}
	ct, stp := mcp.NewInMemoryTransports()
	ss, err := srv.MCP().Connect(t.Context(), stp, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test-agent", Version: "1"}, nil).Connect(t.Context(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return &agent{t: t, fake: fake, cs: cs, init: cs.InitializeResult()}
}

func (a *agent) tools() map[string]*mcp.Tool {
	a.t.Helper()
	out := map[string]*mcp.Tool{}
	for tool, err := range a.cs.Tools(a.t.Context(), nil) {
		if err != nil {
			a.t.Fatal(err)
		}
		out[tool.Name] = tool
	}
	return out
}

func (a *agent) names() []string {
	var names []string
	for n := range a.tools() {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

// call runs a tool and returns its text and whether it was an error.
func (a *agent) call(name string, args map[string]any) (string, bool) {
	a.t.Helper()
	res, err := a.cs.CallTool(a.t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		a.t.Fatalf("%s: %v", name, err)
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String(), res.IsError
}

var (
	readTools        = []string{"find_files", "get_log", "get_operation", "get_restic_env", "get_restore_preflight", "get_retention", "get_snapshot_diff", "get_status", "list_files", "list_locks", "list_snapshots"}
	writeTools       = []string{"cancel_operation", "keep_snapshot", "restore_to_directory", "set_schedule", "start_backup", "start_verify", "test_notification", "unkeep_snapshot", "unlock_stale_locks"}
	destructiveTools = []string{"acknowledge_snapshot", "approve_forget", "restore_overwrite", "unlock_all_locks"}
)

func union(lists ...[]string) []string {
	var out []string
	for _, l := range lists {
		out = append(out, l...)
	}
	slices.Sort(out)
	return out
}

func TestToolsFollowTheAccessLevel(t *testing.T) {
	for _, tc := range []struct {
		name string
		o    mcpserver.Options
		want []string
	}{
		{"read only", mcpserver.Options{}, union(readTools)},
		{"write", mcpserver.Options{Write: true}, union(readTools, writeTools)},
		{"destructive", mcpserver.Options{Destructive: true}, union(readTools, writeTools, destructiveTools)},
		{"destructive and write", mcpserver.Options{Write: true, Destructive: true}, union(readTools, writeTools, destructiveTools)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := connect(t, tc.o).names(); !slices.Equal(got, tc.want) {
				t.Errorf("tools = %v\nwant    %v", got, tc.want)
			}
		})
	}
}

func TestAppendOnlyHasNoTagOrDeleteTools(t *testing.T) {
	a := connect(t, mcpserver.Options{Destructive: true}, func(_ *servicetest.Fake, st *service.Status) { st.AppendOnly = true })
	names := a.names()
	for _, gone := range []string{"keep_snapshot", "unkeep_snapshot", "acknowledge_snapshot", "approve_forget", "unlock_all_locks"} {
		if slices.Contains(names, gone) {
			t.Errorf("%s is offered on an append-only repository", gone)
		}
	}
	for _, kept := range []string{"start_backup", "restore_overwrite", "unlock_stale_locks", "get_status"} {
		if !slices.Contains(names, kept) {
			t.Errorf("%s is missing", kept)
		}
	}
	if !strings.Contains(a.init.Instructions, "append-only") {
		t.Errorf("instructions do not mention append-only:\n%s", a.init.Instructions)
	}
}

func TestNothingForTheWebUIAndNoSecretTools(t *testing.T) {
	for _, n := range connect(t, mcpserver.Options{Destructive: true}).names() {
		if strings.Contains(n, "web") || strings.Contains(n, "login") || strings.Contains(n, "secret") {
			t.Errorf("tool %s must not exist", n)
		}
	}
}

func TestAnnotations(t *testing.T) {
	tools := connect(t, mcpserver.Options{Destructive: true}).tools()
	for _, n := range readTools {
		if !tools[n].Annotations.ReadOnlyHint {
			t.Errorf("%s should be read only", n)
		}
	}
	for _, n := range writeTools {
		an := tools[n].Annotations
		if an.ReadOnlyHint || an.DestructiveHint == nil || *an.DestructiveHint {
			t.Errorf("%s should be additive: %+v", n, an)
		}
	}
	for _, n := range destructiveTools {
		an := tools[n].Annotations
		if an.ReadOnlyHint || an.DestructiveHint == nil || !*an.DestructiveHint {
			t.Errorf("%s should be destructive: %+v", n, an)
		}
	}
}

func TestInstructionsDescribeTheInstance(t *testing.T) {
	a := connect(t, mcpserver.Options{})
	for _, want := range []string{`host "app"`, "read-only", "get_operation", "never shown"} {
		if !strings.Contains(a.init.Instructions, want) {
			t.Errorf("instructions lack %q:\n%s", want, a.init.Instructions)
		}
	}
	w := connect(t, mcpserver.Options{Write: true})
	if !strings.Contains(w.init.Instructions, "read and write") {
		t.Errorf("instructions:\n%s", w.init.Instructions)
	}
}

func TestServerNeedsAReachableService(t *testing.T) {
	fake := &servicetest.Fake{StatusFn: func(context.Context) (service.Status, error) {
		return service.Status{}, errors.New("connection refused")
	}}
	if _, err := mcpserver.New(t.Context(), fake, mcpserver.Options{}); err == nil {
		t.Error("New succeeded without a service")
	}
}

func TestReadTools(t *testing.T) {
	a := connect(t, mcpserver.Options{}, func(f *servicetest.Fake, st *service.Status) {
		st.Warnings = []service.Warning{{Code: "x", Severity: service.SeverityWarning, Message: "forget is held back"}}
		f.SnapshotsFn = func(context.Context) ([]service.Snapshot, error) {
			return []service.Snapshot{{ID: "abc", ShortID: "abc", Time: t0, Keep: true, Note: "before upgrade"}}, nil
		}
		f.LocksFn = func(context.Context) ([]service.Lock, error) { return []service.Lock{{ID: "l1", Stale: true}}, nil }
		f.LogFn = func(_ context.Context, k service.Kind) (service.Log, error) {
			return service.Log{Kind: k, Lines: []string{"a line"}}, nil
		}
		f.RetentionFn = func(context.Context) (service.RetentionInfo, error) {
			return service.RetentionInfo{Policy: "the last 7 snapshots", Held: true}, nil
		}
		f.RestorePreflightFn = func(_ context.Context, r service.RestoreRequest) (service.RestorePreflight, error) {
			return service.RestorePreflight{Target: r.Target, InPlace: r.Target == "", Mode: r.Mode}, nil
		}
	})
	for name, want := range map[string]string{
		"get_status": "forget is held back", "list_snapshots": "before upgrade", "list_locks": `"stale": true`,
		"get_log": "a line", "get_retention": "the last 7 snapshots",
	} {
		if text, isErr := a.call(name, nil); isErr || !strings.Contains(text, want) {
			t.Errorf("%s: error=%v\n%s", name, isErr, text)
		}
	}
	if text, _ := a.call("get_log", map[string]any{"kind": "prune"}); !strings.Contains(text, `"kind": "prune"`) {
		t.Errorf("get_log kind: %s", text)
	}
	text, isErr := a.call("get_restore_preflight", map[string]any{"snapshot": "latest"})
	if isErr || !strings.Contains(text, `"in_place": true`) {
		t.Errorf("preflight: %v\n%s", isErr, text)
	}
	a.fake.FilesFn = func(_ context.Context, r service.FilesRequest) (service.FileList, error) {
		return service.FileList{Path: r.Path, Files: []service.File{{Path: r.Path + "/a.txt", Type: "file"}}}, nil
	}
	if text, isErr := a.call("list_files", map[string]any{"path": "/docs"}); isErr || !strings.Contains(text, `"/docs/a.txt"`) {
		t.Errorf("list_files: %v\n%s", isErr, text)
	}
	a.fake.FindFn = func(_ context.Context, r service.FindRequest) (service.FindResult, error) {
		return service.FindResult{Searched: 3, Versions: []service.FileVersion{{Path: "/" + r.Pattern, Snapshots: 2}}}, nil
	}
	if text, isErr := a.call("find_files", map[string]any{"pattern": "x.sql", "ignore_case": true}); isErr || !strings.Contains(text, `"/x.sql"`) {
		t.Errorf("find_files: %v\n%s", isErr, text)
	}
	if req := a.fake.CallsTo("Find")[0].Args[0].(service.FindRequest); !req.IgnoreCase {
		t.Errorf("request = %+v", req)
	}
	a.fake.DiffFn = func(_ context.Context, r service.DiffRequest) (service.DiffResult, error) {
		return service.DiffResult{To: service.Snapshot{ID: r.To}, Removed: service.DiffSide{Paths: []service.DiffPath{{Path: "/big/", Entries: 4}}}}, nil
	}
	if text, isErr := a.call("get_snapshot_diff", map[string]any{"snapshot": "abc", "older": "def"}); isErr || !strings.Contains(text, `"/big/"`) {
		t.Errorf("get_snapshot_diff: %v\n%s", isErr, text)
	}
	if req := a.fake.CallsTo("Diff")[0].Args[0].(service.DiffRequest); req.To != "abc" || req.From != "def" {
		t.Errorf("request = %+v", req)
	}
	if len(a.fake.CallsTo("StartRestore")) != 0 {
		t.Error("a read tool started a restore")
	}
	for _, c := range a.fake.Calls() {
		if c.Name != "Status" && c.Source != service.SourceMCP {
			t.Errorf("%s was called with source %q", c.Name, c.Source)
		}
	}
}

func TestEnvNamesOnly(t *testing.T) {
	a := connect(t, mcpserver.Options{}, func(f *servicetest.Fake, _ *service.Status) {
		f.EnvFn = func(_ context.Context, reveal bool) (service.EnvInfo, error) {
			pw := "hunter2"
			if !reveal {
				pw = "***"
			}
			return service.EnvInfo{MinVersion: "0.17", Args: []string{"-o", "s3.list-objects-v1=true"}, Notes: []string{"a note"},
				Vars: []service.EnvVar{{Name: "RESTIC_REPOSITORY", Value: "s3:host/bucket"}, {Name: "RESTIC_PASSWORD", Value: pw, Secret: true}}}, nil
		}
	})
	text, isErr := a.call("get_restic_env", nil)
	if isErr {
		t.Fatal(text)
	}
	for _, want := range []string{"RESTIC_REPOSITORY", "RESTIC_PASSWORD", `"secret": true`, "0.17", "a note"} {
		if !strings.Contains(text, want) {
			t.Errorf("output lacks %q:\n%s", want, text)
		}
	}
	for _, leak := range []string{"hunter2", "***", "s3:host/bucket", `"value"`} {
		if strings.Contains(text, leak) {
			t.Errorf("output contains %q:\n%s", leak, text)
		}
	}
	for _, c := range a.fake.CallsTo("Env") {
		if c.Args[0] != false {
			t.Error("the secrets were requested from the service")
		}
	}
}

func TestStartBackup(t *testing.T) {
	a := connect(t, mcpserver.Options{Write: true}, func(f *servicetest.Fake, _ *service.Status) {
		f.StartBackupFn = func(context.Context, service.BackupRequest) (service.Operation, error) {
			return service.Operation{ID: "op1", Kind: service.KindBackup, Source: "mcp"}, nil
		}
	})
	text, isErr := a.call("start_backup", map[string]any{"note": "before the move", "keep": true})
	if isErr || !strings.Contains(text, `"started": true`) || !strings.Contains(text, "get_operation") {
		t.Errorf("error=%v\n%s", isErr, text)
	}
	calls := a.fake.CallsTo("StartBackup")
	if len(calls) != 1 || calls[0].Args[0].(service.BackupRequest) != (service.BackupRequest{Note: "before the move", Keep: true}) || calls[0].Source != service.SourceMCP {
		t.Errorf("calls: %v", a.fake)
	}
}

func TestBusyIsExplained(t *testing.T) {
	a := connect(t, mcpserver.Options{Write: true}, func(f *servicetest.Fake, _ *service.Status) {
		f.StartBackupFn = func(context.Context, service.BackupRequest) (service.Operation, error) {
			return service.Operation{}, &service.BusyError{Current: service.Operation{Kind: service.KindPrune, Source: "schedule", Started: t0}}
		}
	})
	text, isErr := a.call("start_backup", nil)
	if !isErr || !strings.Contains(text, "another operation is running (prune") || !strings.Contains(text, "get_operation") {
		t.Errorf("error=%v\n%s", isErr, text)
	}
}

func TestRestoreToDirectoryOnlyIntoAnEmptyDirectory(t *testing.T) {
	var pre service.RestorePreflight
	a := connect(t, mcpserver.Options{Write: true}, func(f *servicetest.Fake, _ *service.Status) {
		f.RestorePreflightFn = func(context.Context, service.RestoreRequest) (service.RestorePreflight, error) { return pre, nil }
	})

	if text, isErr := a.call("restore_to_directory", map[string]any{"snapshot": "latest"}); !isErr || !strings.Contains(text, "target") {
		t.Errorf("no target: error=%v\n%s", isErr, text)
	}
	for name, p := range map[string]service.RestorePreflight{
		"in place":  {InPlace: true, TargetEmpty: true},
		"pipe mode": {PipeMode: true, TargetEmpty: true},
		"not empty": {TargetEmpty: false},
	} {
		pre = p
		text, isErr := a.call("restore_to_directory", map[string]any{"snapshot": "latest", "target": "/data"})
		if !isErr || !strings.Contains(text, "restore_overwrite") {
			t.Errorf("%s: error=%v\n%s", name, isErr, text)
		}
	}
	if n := len(a.fake.CallsTo("StartRestore")); n != 0 {
		t.Fatalf("a restore was started despite the refusals: %d", n)
	}

	pre = service.RestorePreflight{TargetEmpty: true}
	text, isErr := a.call("restore_to_directory", map[string]any{"snapshot": "abc", "target": "/tmp/out", "include": []string{"/etc"}, "ignore_space_warning": true})
	if isErr || !strings.Contains(text, `"started": true`) {
		t.Fatalf("error=%v\n%s", isErr, text)
	}
	got := a.fake.CallsTo("StartRestore")[0].Args[0].(service.RestoreRequest)
	if got.Snapshot != "abc" || got.Target != "/tmp/out" || got.Mode != service.ModeMerge || !got.IgnoreSpaceWarning || !slices.Equal(got.Includes, []string{"/etc"}) || got.NoSafetySnapshot {
		t.Errorf("request = %+v", got)
	}
}

func TestRestoreOverwrite(t *testing.T) {
	a := connect(t, mcpserver.Options{Destructive: true}, func(f *servicetest.Fake, _ *service.Status) {
		f.StartRestoreFn = func(_ context.Context, req service.RestoreRequest) (service.Operation, error) {
			if req.Target == "" && req.Confirm != req.Snapshot {
				return service.Operation{}, service.ErrConfirm
			}
			return service.Operation{Kind: service.KindRestore}, nil
		}
	})
	text, isErr := a.call("restore_overwrite", map[string]any{"snapshot": "latest"})
	if !isErr || !strings.Contains(text, "confirmation") {
		t.Errorf("without confirm: error=%v\n%s", isErr, text)
	}
	if text, isErr = a.call("restore_overwrite", map[string]any{"snapshot": "latest", "confirm": "latest", "mode": "merge", "safety_snapshot": false}); isErr {
		t.Fatal(text)
	}
	got := a.fake.CallsTo("StartRestore")[1].Args[0].(service.RestoreRequest)
	if got.Mode != service.ModeMerge || !got.NoSafetySnapshot || got.Confirm != "latest" {
		t.Errorf("request = %+v", got)
	}
	// the safety snapshot is the default
	a.call("restore_overwrite", map[string]any{"snapshot": "latest", "confirm": "latest"})
	if got := a.fake.CallsTo("StartRestore")[2].Args[0].(service.RestoreRequest); got.NoSafetySnapshot || got.Mode != service.ModeExact {
		t.Errorf("defaults: %+v", got)
	}
}

func TestDestructiveToolsPassTheConfirmation(t *testing.T) {
	a := connect(t, mcpserver.Options{Destructive: true})
	a.call("acknowledge_snapshot", map[string]any{"snapshot": "abc", "confirm": "acknowledge"})
	a.call("approve_forget", map[string]any{"confirm": "forget"})
	a.call("unlock_all_locks", map[string]any{"confirm": "remove-all"})
	if c := a.fake.CallsTo("Acknowledge"); len(c) != 1 || c[0].Args[0] != "abc" || c[0].Args[1] != "acknowledge" {
		t.Errorf("Acknowledge: %v", a.fake)
	}
	if c := a.fake.CallsTo("ApproveForget"); len(c) != 1 || c[0].Args[0] != "forget" {
		t.Errorf("ApproveForget: %v", a.fake)
	}
	if c := a.fake.CallsTo("Unlock"); len(c) != 1 || c[0].Args[0] != true || c[0].Args[1] != "remove-all" {
		t.Errorf("Unlock: %v", a.fake)
	}
	for _, c := range a.fake.Calls() {
		if c.Name != "Status" && c.Source != service.SourceMCP {
			t.Errorf("%s was called with source %q", c.Name, c.Source)
		}
	}
}

func TestKeepUnkeepUnlockSchedule(t *testing.T) {
	a := connect(t, mcpserver.Options{Write: true})
	a.call("keep_snapshot", map[string]any{"snapshot": "abc"})
	a.call("unkeep_snapshot", map[string]any{"snapshot": "abc"})
	a.call("unlock_stale_locks", nil)
	a.call("cancel_operation", nil)
	a.call("test_notification", nil)
	a.call("start_verify", map[string]any{"mode": "full"})
	for _, name := range []string{"Keep", "Unkeep", "Unlock", "Cancel", "NotifyTest", "StartVerify"} {
		if len(a.fake.CallsTo(name)) != 1 {
			t.Errorf("%s not called once: %v", name, a.fake)
		}
	}
	if c := a.fake.CallsTo("Unlock"); c[0].Args[0] != false {
		t.Error("unlock_stale_locks removed all locks")
	}

	if _, isErr := a.call("set_schedule", map[string]any{"action": "maintenance", "for": "2h"}); isErr {
		t.Error("maintenance for 2h failed")
	}
	for _, args := range []map[string]any{{"action": "maintenance"}, {"action": "maintenance", "for": "soon"}, {"action": "explode"}} {
		if _, isErr := a.call("set_schedule", args); !isErr {
			t.Errorf("set_schedule %v should fail", args)
		}
	}
	calls := a.fake.CallsTo("SetSchedule")
	if len(calls) != 1 || calls[0].Args[0].(service.ScheduleRequest).For.Std() != 2*time.Hour {
		t.Errorf("calls: %v", a.fake)
	}
}

func TestGetOperation(t *testing.T) {
	var running *service.Operation
	events := make(chan service.Event, 4)
	a := connect(t, mcpserver.Options{}, func(f *servicetest.Fake, st *service.Status) {
		st.LastRuns = map[service.Kind]service.RunResult{service.KindBackup: {Kind: service.KindBackup, Result: service.ResultSuccess}}
		f.OperationFn = func(context.Context) (*service.Operation, error) { return running, nil }
		f.EventsFn = func(ctx context.Context) iter.Seq2[service.Event, error] {
			return func(yield func(service.Event, error) bool) {
				for {
					select {
					case ev := <-events:
						if !yield(ev, nil) {
							return
						}
					case <-ctx.Done():
						return
					}
				}
			}
		}
	})

	text, isErr := a.call("get_operation", nil)
	if isErr || !strings.Contains(text, `"running": false`) || !strings.Contains(text, "success") {
		t.Errorf("idle: error=%v\n%s", isErr, text)
	}

	running = &service.Operation{ID: "1", Kind: service.KindBackup, Phase: "backup"}
	if text, _ = a.call("get_operation", nil); !strings.Contains(text, `"running": true`) || !strings.Contains(text, `"phase": "backup"`) {
		t.Errorf("running: %s", text)
	}

	// waiting returns the result when the operation ends
	events <- service.Event{Type: service.EventProgress, Operation: running}
	events <- service.Event{Type: service.EventFinished, Result: &service.RunResult{Kind: service.KindBackup, Result: service.ResultWarning, Snapshot: "abc"}}
	text, isErr = a.call("get_operation", map[string]any{"wait_seconds": 30})
	if isErr || !strings.Contains(text, `"running": false`) || !strings.Contains(text, `"result": "warning"`) {
		t.Errorf("finished: error=%v\n%s", isErr, text)
	}
}

func TestGetOperationTimesOut(t *testing.T) {
	a := connect(t, mcpserver.Options{}, func(f *servicetest.Fake, _ *service.Status) {
		f.OperationFn = func(context.Context) (*service.Operation, error) {
			return &service.Operation{ID: "1", Kind: service.KindBackup}, nil
		}
		f.EventsFn = func(ctx context.Context) iter.Seq2[service.Event, error] {
			return func(func(service.Event, error) bool) { <-ctx.Done() }
		}
	})
	start := time.Now()
	text, _ := a.call("get_operation", map[string]any{"wait_seconds": 1})
	if !strings.Contains(text, `"running": true`) || !strings.Contains(text, "call get_operation again") {
		t.Errorf("output:\n%s", text)
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("waited %v for a 1 second wait", time.Since(start))
	}
}

func TestGetOperationWhenItEndedBeforeTheStreamStarted(t *testing.T) {
	calls := 0
	a := connect(t, mcpserver.Options{}, func(f *servicetest.Fake, _ *service.Status) {
		f.OperationFn = func(context.Context) (*service.Operation, error) {
			calls++
			if calls == 1 {
				return &service.Operation{ID: "1", Kind: service.KindBackup}, nil
			}
			return nil, nil // finished in the meantime
		}
		f.EventsFn = func(context.Context) iter.Seq2[service.Event, error] {
			return func(func(service.Event, error) bool) {}
		}
	})
	text, isErr := a.call("get_operation", map[string]any{"wait_seconds": 5})
	if isErr || !strings.Contains(text, `"running": false`) || strings.Contains(text, "did not end") {
		t.Errorf("error=%v\n%s", isErr, text)
	}
}

func TestInputIsValidated(t *testing.T) {
	a := connect(t, mcpserver.Options{Write: true})
	// a string where a boolean belongs is rejected before the service is touched
	res, err := a.cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "start_backup", Arguments: map[string]any{"keep": "yes"}})
	if err == nil && !res.IsError {
		t.Error("invalid input was accepted")
	}
	if len(a.fake.CallsTo("StartBackup")) != 0 {
		t.Error("the service was called with invalid input")
	}
}
