package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/noris-network/mcs-backup/v2/internal/service"
	"github.com/noris-network/mcs-backup/v2/internal/servicetest"
)

// request sends a request and returns the status and the body.
func request(t *testing.T, method, url, body string) (int, string) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(t.Context(), method, url, rd)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(data)
}

func newPair(t *testing.T) (*servicetest.Fake, *Client, *httptest.Server) {
	t.Helper()
	fake := &servicetest.Fake{}
	srv := httptest.NewServer(NewHandler(fake, nil))
	t.Cleanup(srv.Close)
	return fake, newClient(srv.URL, srv.Client(), service.SourceCLI), srv
}

func TestRoundTrips(t *testing.T) {
	fake, c, _ := newPair(t)
	ctx := t.Context()
	started := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	fake.StatusFn = func(context.Context) (service.Status, error) {
		return service.Status{Version: "v2", Host: "me", Repository: service.RepoStatus{Up: true, Snapshots: 3, SizeBytes: -1},
			Schedule: service.ScheduleStatus{Enabled: true, Next: map[service.Kind]time.Time{service.KindBackup: started}},
			Warnings: []service.Warning{{Code: "x", Severity: service.SeverityInfo, Message: "m"}}}, nil
	}
	st, err := c.Status(ctx)
	if err != nil || st.Version != "v2" || st.Repository.Snapshots != 3 || !st.Schedule.Next[service.KindBackup].Equal(started) || len(st.Warnings) != 1 {
		t.Fatalf("Status() = %+v, %v", st, err)
	}

	fake.SnapshotsFn = func(context.Context) ([]service.Snapshot, error) {
		return []service.Snapshot{{ID: "abc", Time: started, Keep: true, Note: "n", Paths: []string{"/x"}}}, nil
	}
	snaps, err := c.Snapshots(ctx)
	if err != nil || len(snaps) != 1 || snaps[0].ID != "abc" || !snaps[0].Keep || !snaps[0].Time.Equal(started) {
		t.Fatalf("Snapshots() = %+v, %v", snaps, err)
	}

	fake.RetentionFn = func(context.Context) (service.RetentionInfo, error) {
		return service.RetentionInfo{Policy: "the last 10 snapshots", Total: 12, Held: true, Remove: []service.Snapshot{{ID: "old"}}}, nil
	}
	if r, err := c.Retention(ctx); err != nil || !r.Held || r.Total != 12 || r.Remove[0].ID != "old" {
		t.Errorf("Retention() = %+v, %v", r, err)
	}

	fake.LocksFn = func(context.Context) ([]service.Lock, error) {
		return []service.Lock{{ID: "l1", Age: service.Duration(2 * time.Hour), Stale: true, PID: 7}}, nil
	}
	if l, err := c.Locks(ctx); err != nil || len(l) != 1 || l[0].Age.Std() != 2*time.Hour || !l[0].Stale {
		t.Errorf("Locks() = %+v, %v", l, err)
	}

	fake.FilesFn = func(_ context.Context, req service.FilesRequest) (service.FileList, error) {
		return service.FileList{Path: req.Path, Files: []service.File{{Path: req.Path + "/f", Type: "file", Size: 7, Mtime: started}}, Truncated: true}, nil
	}
	fl, err := c.Files(ctx, service.FilesRequest{Snapshot: "abc", Path: "/a b/ü&x"})
	if err != nil || fl.Files[0].Path != "/a b/ü&x/f" || fl.Files[0].Size != 7 || !fl.Truncated {
		t.Errorf("Files() = %+v, %v", fl, err)
	}
	if req := fake.CallsTo("Files")[0].Args[0].(service.FilesRequest); req.Snapshot != "abc" {
		t.Errorf("request = %+v", req)
	}

	fake.FindFn = func(_ context.Context, req service.FindRequest) (service.FindResult, error) {
		return service.FindResult{Searched: 4, Versions: []service.FileVersion{{Path: req.Pattern, Newest: service.SnapshotRef{ID: "n"}, Snapshots: 2}}}, nil
	}
	fr, err := c.Find(ctx, service.FindRequest{Pattern: "*.sql?&", Snapshot: "latest", IgnoreCase: true})
	if err != nil || fr.Searched != 4 || fr.Versions[0].Path != "*.sql?&" || fr.Versions[0].Newest.ID != "n" {
		t.Errorf("Find() = %+v, %v", fr, err)
	}
	if req := fake.CallsTo("Find")[0].Args[0].(service.FindRequest); req.Snapshot != "latest" || !req.IgnoreCase {
		t.Errorf("request = %+v", req)
	}

	fake.DiffFn = func(_ context.Context, req service.DiffRequest) (service.DiffResult, error) {
		return service.DiffResult{From: service.Snapshot{ID: req.From}, To: service.Snapshot{ID: req.To}, Changed: 3,
			Removed: service.DiffSide{Files: 2, Paths: []service.DiffPath{{Path: "/big/", Entries: 4}}, More: 1}}, nil
	}
	df, err := c.Diff(ctx, service.DiffRequest{From: "a&b", To: "latest"})
	if err != nil || df.From.ID != "a&b" || df.To.ID != "latest" || df.Changed != 3 || df.Removed.Paths[0].Entries != 4 || df.Removed.More != 1 {
		t.Errorf("Diff() = %+v, %v", df, err)
	}

	fake.LogFn = func(_ context.Context, k service.Kind) (service.Log, error) {
		return service.Log{Kind: k, Lines: []string{"a", "b"}}, nil
	}
	if l, err := c.Log(ctx, service.KindBackup); err != nil || l.Kind != service.KindBackup || len(l.Lines) != 2 {
		t.Errorf("Log() = %+v, %v", l, err)
	}

	fake.EnvFn = func(_ context.Context, reveal bool) (service.EnvInfo, error) {
		return service.EnvInfo{MinVersion: "0.17", Vars: []service.EnvVar{{Name: "RESTIC_PASSWORD", Value: map[bool]string{true: "pw", false: "***"}[reveal], Secret: true}}}, nil
	}
	if e, err := c.Env(ctx, true); err != nil || e.Vars[0].Value != "pw" {
		t.Errorf("Env(true) = %+v, %v", e, err)
	}
	if e, err := c.Env(ctx, false); err != nil || e.Vars[0].Value != "***" {
		t.Errorf("Env(false) = %+v, %v", e, err)
	}

	op, err := c.StartBackup(ctx, service.BackupRequest{Note: "before upgrade", Keep: true})
	if err != nil || op.ID != "op1" || op.Kind != service.KindBackup {
		t.Fatalf("StartBackup() = %+v, %v", op, err)
	}
	if got := fake.CallsTo("StartBackup")[0]; got.Args[0] != (service.BackupRequest{Note: "before upgrade", Keep: true}) || got.Source != service.SourceCLI {
		t.Errorf("call = %+v: the request and its source must arrive", got)
	}

	req := service.RestoreRequest{Snapshot: "latest", Target: "/tmp/x", Includes: []string{"/a", "/b"}, Mode: service.ModeMerge,
		NoSafetySnapshot: true, IgnoreSpaceWarning: true, HookArgs: []string{"db1"}, Confirm: "latest"}
	if _, err := c.StartRestore(ctx, req); err != nil {
		t.Fatal(err)
	}
	got := fake.CallsTo("StartRestore")[0].Args[0].(service.RestoreRequest)
	if got.Snapshot != "latest" || got.Target != "/tmp/x" || len(got.Includes) != 2 || got.Mode != service.ModeMerge || !got.NoSafetySnapshot ||
		!got.IgnoreSpaceWarning || got.HookArgs[0] != "db1" || got.Confirm != "latest" {
		t.Errorf("restore request = %+v", got)
	}

	if _, err := c.StartVerify(ctx, service.VerifyRequest{Mode: "full"}); err != nil || fake.CallsTo("StartVerify")[0].Args[0] != (service.VerifyRequest{Mode: "full"}) {
		t.Errorf("StartVerify: %v %v", err, fake.CallsTo("StartVerify"))
	}
	if op, err := c.StartPrune(ctx); err != nil || op.Kind != service.KindPrune || len(fake.CallsTo("StartPrune")) != 1 {
		t.Errorf("StartPrune: %+v %v", op, err)
	}
	if op, err := c.StartCheck(ctx); err != nil || op.Kind != service.KindCheck || len(fake.CallsTo("StartCheck")) != 1 {
		t.Errorf("StartCheck: %+v %v", op, err)
	}
	fake.StartPruneFn = func(context.Context) (service.Operation, error) {
		return service.Operation{}, fmt.Errorf("%w: prune removes data", service.ErrAppendOnly)
	}
	if _, err := c.StartPrune(ctx); !errors.Is(err, service.ErrAppendOnly) {
		t.Errorf("StartPrune on an append-only repository: %v", err)
	}
	if _, err := c.ApproveForget(ctx, "forget"); err != nil || fake.CallsTo("ApproveForget")[0].Args[0] != "forget" {
		t.Errorf("ApproveForget: %v", err)
	}
	if err := c.Cancel(ctx); err != nil || len(fake.CallsTo("Cancel")) != 1 {
		t.Errorf("Cancel: %v", err)
	}
	if err := c.NotifyTest(ctx); err != nil || len(fake.CallsTo("NotifyTest")) != 1 {
		t.Errorf("NotifyTest: %v", err)
	}
}

func TestSnapshotReferences(t *testing.T) {
	fake, c, _ := newPair(t)
	ctx := t.Context()
	if res, err := c.Keep(ctx, "abc123"); err != nil || res.ID != "abc123-kept" {
		t.Errorf("Keep() = %+v, %v", res, err)
	}
	if res, err := c.Unkeep(ctx, "abc123"); err != nil || res.ID != "abc123-unkept" {
		t.Errorf("Unkeep() = %+v, %v", res, err)
	}
	if res, err := c.Acknowledge(ctx, "abc123", "acknowledge"); err != nil || res.ID != "abc123-acked" {
		t.Errorf("Acknowledge() = %+v, %v", res, err)
	}
	if a := fake.CallsTo("Acknowledge")[0].Args; a[0] != "abc123" || a[1] != "acknowledge" {
		t.Errorf("args = %v", a)
	}
	if err := c.Unlock(ctx, true, "remove-all"); err != nil {
		t.Fatal(err)
	}
	if a := fake.CallsTo("Unlock")[0].Args; a[0] != true || a[1] != "remove-all" {
		t.Errorf("args = %v", a)
	}
}

func TestOperation(t *testing.T) {
	fake, c, _ := newPair(t)
	if op, err := c.Operation(t.Context()); err != nil || op != nil {
		t.Errorf("idle: Operation() = %+v, %v", op, err)
	}
	fake.OperationFn = func(context.Context) (*service.Operation, error) {
		return &service.Operation{ID: "x", Kind: service.KindRestore, Phase: "restore", Progress: &service.Progress{Percent: 0.4}}, nil
	}
	op, err := c.Operation(t.Context())
	if err != nil || op == nil || op.Kind != service.KindRestore || op.Progress.Percent != 0.4 {
		t.Errorf("Operation() = %+v, %v", op, err)
	}
}

func TestPreflightQuery(t *testing.T) {
	fake, c, _ := newPair(t)
	fake.RestorePreflightFn = func(context.Context, service.RestoreRequest) (service.RestorePreflight, error) {
		return service.RestorePreflight{InPlace: true, NeedBytes: 5, SpaceWarning: true, Snapshot: service.Snapshot{ID: "s"}}, nil
	}
	req := service.RestoreRequest{Snapshot: "abc", Target: "/t", Includes: []string{"/a b", "/c"}, Mode: service.ModeExact, NoSafetySnapshot: true}
	p, err := c.RestorePreflight(t.Context(), req)
	if err != nil || !p.InPlace || p.NeedBytes != 5 || !p.SpaceWarning || p.Snapshot.ID != "s" {
		t.Fatalf("RestorePreflight() = %+v, %v", p, err)
	}
	got := fake.CallsTo("RestorePreflight")[0].Args[0].(service.RestoreRequest)
	if got.Snapshot != "abc" || got.Target != "/t" || len(got.Includes) != 2 || got.Includes[0] != "/a b" || got.Mode != service.ModeExact || !got.NoSafetySnapshot {
		t.Errorf("request = %+v", got)
	}
}

func TestSchedule(t *testing.T) {
	fake, c, srv := newPair(t)
	if _, err := c.SetSchedule(t.Context(), service.ScheduleRequest{Action: service.ScheduleMaintenance, For: service.Duration(90 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if got := fake.CallsTo("SetSchedule")[0].Args[0].(service.ScheduleRequest); got.Action != service.ScheduleMaintenance || got.For.Std() != 90*time.Minute {
		t.Errorf("request = %+v", got)
	}

	// a hand-written request, as from curl
	status, _ := request(t, http.MethodPost, srv.URL+"/v1/schedule", `{"action":"maintenance","for":"2h"}`)
	if got := fake.CallsTo("SetSchedule")[1].Args[0].(service.ScheduleRequest); status != http.StatusOK || got.For.Std() != 2*time.Hour {
		t.Errorf("status %d, request %+v: durations like \"2h\" must work", status, got)
	}
}

func TestWebUI(t *testing.T) {
	fake, c, _ := newPair(t)
	fake.WebUIFn = func(context.Context) (service.WebUIStatus, error) {
		return service.WebUIStatus{Enabled: true, Addr: "127.0.0.1:8080"}, nil
	}
	if w, err := c.WebUI(t.Context()); err != nil || !w.Enabled || w.Addr != "127.0.0.1:8080" {
		t.Errorf("WebUI() = %+v, %v", w, err)
	}
	if w, err := c.SetWebUI(t.Context(), service.WebUIRequest{Enable: true, Addr: ":9090", For: service.Duration(time.Hour)}); err != nil || !w.Enabled {
		t.Errorf("SetWebUI() = %+v, %v", w, err)
	}
	if got := fake.CallsTo("SetWebUI")[0].Args[0].(service.WebUIRequest); got.Addr != ":9090" || got.For.Std() != time.Hour {
		t.Errorf("request = %+v", got)
	}
	if link, err := c.LoginLink(t.Context()); err != nil || !strings.Contains(link, "/login?code=") {
		t.Errorf("LoginLink() = %q, %v", link, err)
	}
}

func TestErrorMapping(t *testing.T) {
	fake, c, _ := newPair(t)
	ctx := t.Context()
	running := service.Operation{ID: "run1", Kind: service.KindRestore, Started: time.Now()}

	tests := []struct {
		name   string
		err    error
		is     error
		status int
	}{
		{"busy", &service.BusyError{Current: running}, service.ErrBusy, 409},
		{"confirm", errors.New("x: " + service.ErrConfirm.Error()), nil, 500},
		{"confirm sentinel", service.ErrConfirm, service.ErrConfirm, 400},
		{"not found", service.ErrNotFound, service.ErrNotFound, 404},
		{"append only", service.ErrAppendOnly, service.ErrAppendOnly, 403},
		{"unavailable", service.ErrUnavailable, service.ErrUnavailable, 501},
		{"other", errors.New("disk exploded"), nil, 500},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake.StartBackupFn = func(context.Context, service.BackupRequest) (service.Operation, error) {
				return service.Operation{}, tt.err
			}
			_, err := c.StartBackup(ctx, service.BackupRequest{})
			if err == nil {
				t.Fatal("no error")
			}
			if tt.is != nil && !errors.Is(err, tt.is) {
				t.Errorf("errors.Is(%v, %v) = false", err, tt.is)
			}
			if !strings.Contains(err.Error(), tt.err.Error()) {
				t.Errorf("message %q lost the service's text %q", err, tt.err)
			}
			if re, ok := errors.AsType[*remoteError](err); ok && re.StatusCode() != tt.status {
				t.Errorf("status = %d, want %d", re.StatusCode(), tt.status)
			}
		})
	}

	fake.StartBackupFn = func(context.Context, service.BackupRequest) (service.Operation, error) {
		return service.Operation{}, &service.BusyError{Current: running}
	}
	_, err := c.StartBackup(ctx, service.BackupRequest{})
	be, ok := errors.AsType[*service.BusyError](err)
	if !ok || be.Current.ID != "run1" || be.Current.Kind != service.KindRestore {
		t.Errorf("the busy error must name the running operation: %v", err)
	}
}

func TestBadRequests(t *testing.T) {
	fake, _, srv := newPair(t)
	post := func(path, body string) (int, string) {
		return request(t, http.MethodPost, srv.URL+path, body)
	}
	for name, tc := range map[string]struct{ path, body string }{
		"unknown field": {"/v1/backup", `{"note":"x","exec":"rm -rf /"}`},
		"not json":      {"/v1/backup", `{`},
		"wrong type":    {"/v1/backup", `{"keep":"yes"}`},
		"unknown mode":  {"/v1/schedule", `{"action":1}`},
	} {
		code, body := post(tc.path, tc.body)
		if code != http.StatusBadRequest || !strings.Contains(body, `"code":"bad_request"`) {
			t.Errorf("%s: %d %s", name, code, body)
		}
	}
	if len(fake.CallsTo("StartBackup")) != 0 {
		t.Error("the service was called with a malformed request")
	}
	if code, _ := post("/v1/backup", ""); code != http.StatusAccepted {
		t.Errorf("an empty body is a default request: %d", code)
	}

	if code, _ := request(t, http.MethodGet, srv.URL+"/v1/restore/preflight?no_safety_snapshot=maybe", ""); code != http.StatusBadRequest {
		t.Errorf("bad preflight query: %d", code)
	}
	if code, _ := request(t, http.MethodGet, srv.URL+"/v1/nothing", ""); code != http.StatusNotFound {
		t.Errorf("unknown path: %d", code)
	}
	if code, _ := request(t, http.MethodGet, srv.URL+"/v1/backup", ""); code != http.StatusMethodNotAllowed {
		t.Errorf("wrong method: %d", code)
	}
}

func TestSourceHeader(t *testing.T) {
	fake, _, srv := newPair(t)
	for header, want := range map[string]string{"mcp": "mcp", "tui": "tui", "web": "web", "": "unknown", "hacker": "unknown"} {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/v1/status", nil)
		if header != "" {
			req.Header.Set(sourceHeader, header)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		calls := fake.CallsTo("Status")
		if got := calls[len(calls)-1].Source; got != want {
			t.Errorf("header %q: source = %q, want %q", header, got, want)
		}
	}
}

func TestClientReportsItsSource(t *testing.T) {
	fake, c, _ := newPair(t)
	for _, source := range []string{service.SourceTUI, service.SourceMCP, service.SourceCLI} {
		if _, err := c.As(source).Status(t.Context()); err != nil {
			t.Fatal(err)
		}
		calls := fake.CallsTo("Status")
		if got := calls[len(calls)-1].Source; got != source {
			t.Errorf("As(%q): the service saw %q", source, got)
		}
	}
}

func TestEvents(t *testing.T) {
	fake, c, _ := newPair(t)
	fake.EventsFn = func(context.Context) iter.Seq2[service.Event, error] {
		return func(yield func(service.Event, error) bool) {
			for _, ev := range []service.Event{
				{Type: service.EventStarted, Operation: &service.Operation{ID: "op1", Kind: service.KindBackup}},
				{Type: service.EventLog, Line: "line with\ttab and \"quotes\" and ünicode"},
				{Type: service.EventFinished, Result: &service.RunResult{ID: "op1", Result: service.ResultSuccess}},
			} {
				if !yield(ev, nil) {
					return
				}
			}
		}
	}
	var got []service.Event
	for ev, err := range c.Events(t.Context()) {
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, ev)
	}
	if len(got) != 3 || got[0].Operation.ID != "op1" || got[1].Line != "line with\ttab and \"quotes\" and ünicode" || got[2].Result.Result != service.ResultSuccess {
		t.Errorf("events = %+v", got)
	}
}

func TestEventsError(t *testing.T) {
	fake, c, _ := newPair(t)
	fake.EventsFn = func(context.Context) iter.Seq2[service.Event, error] {
		return func(yield func(service.Event, error) bool) {
			if yield(service.Event{Type: service.EventLog, Line: "a"}, nil) {
				yield(service.Event{}, errors.New("event stream dropped"))
			}
		}
	}
	var n int
	var gotErr error
	for _, err := range c.Events(t.Context()) {
		if err != nil {
			gotErr = err
			break
		}
		n++
	}
	if n != 1 || gotErr == nil || !strings.Contains(gotErr.Error(), "event stream dropped") {
		t.Errorf("events = %d, err = %v", n, gotErr)
	}
}

func TestEventsStopWhenTheClientStops(t *testing.T) {
	fake, c, srv := newPair(t)
	stopped := make(chan struct{})
	fake.EventsFn = func(ctx context.Context) iter.Seq2[service.Event, error] {
		return func(yield func(service.Event, error) bool) {
			defer close(stopped)
			if !yield(service.Event{Type: service.EventLog, Line: "first"}, nil) {
				return
			}
			<-ctx.Done()
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	for ev, err := range c.Events(ctx) {
		if err != nil || ev.Line != "first" {
			t.Fatalf("event = %+v, %v", ev, err)
		}
		cancel()
		break
	}
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Error("the server kept the event stream open after the client left")
	}
	srv.Close()
}

func TestUnreachable(t *testing.T) {
	socket := filepath.Join(shortTempDir(t), "none.sock")
	c := NewClient(socket, service.SourceCLI)
	_, err := c.Status(t.Context())
	if !errors.Is(err, ErrUnreachable) || !strings.Contains(err.Error(), socket) {
		t.Errorf("err = %v", err)
	}
	for _, err := range c.Events(t.Context()) {
		if !errors.Is(err, ErrUnreachable) {
			t.Errorf("Events() err = %v", err)
		}
		break
	}
}

// shortTempDir returns a directory with a path short enough for a Unix socket.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "mcsb")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func TestSocket(t *testing.T) {
	path := filepath.Join(shortTempDir(t), "mcs.sock")
	fake := &servicetest.Fake{}

	ln, err := Listen(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o660 {
		t.Errorf("socket mode = %v, %v: only the owner and the group may use it", fi.Mode(), err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, ln, fake, nil) }()

	c := NewClient(path, service.SourceTUI)
	defer c.Close()
	if _, err := c.Status(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := fake.CallsTo("Status"); len(got) != 1 || got[0].Source != service.SourceTUI {
		t.Errorf("calls = %v", got)
	}

	// a second instance must not take the socket over
	if _, err := Listen(t.Context(), path); !errors.Is(err, ErrInstanceRunning) {
		t.Errorf("second Listen() = %v, want ErrInstanceRunning", err)
	}
	if _, err := c.Status(t.Context()); err != nil {
		t.Errorf("the first instance was disturbed: %v", err)
	}

	// an open event stream must not hold the shutdown
	streaming := make(chan struct{})
	fake.EventsFn = func(ctx context.Context) iter.Seq2[service.Event, error] {
		return func(func(service.Event, error) bool) {
			close(streaming)
			<-ctx.Done()
		}
	}
	go func() {
		for _, err := range c.Events(t.Context()) {
			if err != nil {
				return
			}
		}
	}()
	<-streaming
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve() = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve() did not return")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the socket file was left behind: %v", err)
	}
}

func TestListenReplacesAStaleSocket(t *testing.T) {
	path := filepath.Join(shortTempDir(t), "stale.sock")
	// a crashed service leaves its socket file behind
	stale, err := (&net.ListenConfig{}).Listen(t.Context(), "unix", path)
	if err != nil {
		t.Fatal(err)
	}
	stale.(*net.UnixListener).SetUnlinkOnClose(false)
	stale.Close()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("setup: %v", err)
	}

	ln, err := Listen(t.Context(), path)
	if err != nil {
		t.Fatalf("Listen() = %v: an orphaned socket must be replaced", err)
	}
	ln.Close()
}

func TestListenRefusesOtherFiles(t *testing.T) {
	path := filepath.Join(shortTempDir(t), "file")
	if err := os.WriteFile(path, []byte("precious"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Listen(t.Context(), path); err == nil || !strings.Contains(err.Error(), "not a socket") {
		t.Errorf("Listen() = %v", err)
	}
	if data, _ := os.ReadFile(path); string(data) != "precious" {
		t.Error("the file was removed")
	}
}
