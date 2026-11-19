package webui_test

import (
	"context"
	b64 "encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/noris-network/mcs-backup/v2/internal/service"
	"github.com/noris-network/mcs-backup/v2/internal/servicetest"
	"github.com/noris-network/mcs-backup/v2/internal/webui"
)

var t0 = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

// clock is a clock the tests can move.
type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

type site struct {
	t      *testing.T
	fake   *servicetest.Fake
	auth   *webui.Auth
	clock  *clock
	srv    *httptest.Server
	client *http.Client
}

func newSite(t *testing.T, token string) *site {
	t.Helper()
	s := &site{t: t, fake: &servicetest.Fake{}, clock: &clock{t0}}
	s.auth = webui.NewAuth(token, s.clock.now)
	s.fake.StatusFn = func(context.Context) (service.Status, error) {
		return service.Status{
			Version: "2.0.0", Host: "app", StartedAt: t0.Add(-time.Hour), Ready: true, VerifyConfigured: true,
			Repository: service.RepoStatus{Up: true, Snapshots: 2, SizeBytes: 1 << 20, LastSuccess: t0.Add(-3 * time.Hour)},
			Schedule:   service.ScheduleStatus{Enabled: true, Backup: "0 2 * * *"}, Retention: "the last 7 snapshots",
			Source: "files: /data",
		}, nil
	}
	s.fake.SnapshotsFn = func(context.Context) ([]service.Snapshot, error) {
		return []service.Snapshot{
			{ID: strings.Repeat("a", 64), ShortID: "aaaaaaaa", Time: t0.Add(-48 * time.Hour), Host: "app", Origin: service.OriginScheduled, HasSummary: true, Files: 10, Bytes: 4096},
			{ID: strings.Repeat("b", 64), ShortID: "bbbbbbbb", Time: t0.Add(-time.Hour), Host: "app", Origin: service.OriginManual, Suspicious: true, Note: "<b>x</b>"},
		}, nil
	}
	s.srv = httptest.NewServer(webui.NewHandler(s.fake, s.auth, webui.Options{Now: s.clock.now}))
	t.Cleanup(s.srv.Close)
	jar, _ := cookiejar.New(nil)
	s.client = &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return s
}

// login redeems a fresh code like a browser that opened the link.
func (s *site) login() {
	s.t.Helper()
	resp := s.do("GET", "/login?code="+s.auth.NewCode(), nil)
	if resp.status != http.StatusSeeOther {
		s.t.Fatalf("login: %d %s", resp.status, resp.body)
	}
}

type response struct {
	status int
	body   string
	header http.Header
}

func (s *site) do(method, path string, form url.Values, headers ...string) response {
	s.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(s.t.Context(), method, s.srv.URL+path, body)
	if err != nil {
		s.t.Fatal(err)
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := s.client.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return response{resp.StatusCode, string(b), resp.Header}
}

func (r response) want(t *testing.T, status int, parts ...string) {
	t.Helper()
	if r.status != status {
		t.Errorf("status %d, want %d\n%s", r.status, status, r.body)
	}
	for _, p := range parts {
		if !strings.Contains(r.body, p) {
			t.Errorf("the page lacks %q:\n%s", p, r.body)
		}
	}
}

func TestEverythingNeedsALogin(t *testing.T) {
	s := newSite(t, "")
	for _, p := range []string{"GET /", "GET /snapshots", "GET /locks", "GET /access", "GET /log/backup", "GET /restore", "GET /ui/live"} {
		method, path, _ := strings.Cut(p, " ")
		s.do(method, path, nil).want(t, http.StatusUnauthorized, "mcs-backup web")
	}
	for _, p := range []string{"/backup", "/verify", "/cancel", "/schedule", "/notify-test", "/forget/approve", "/restore", "/restore/check", "/locks/unlock", "/web/off", "/snapshots/x/keep"} {
		s.do("POST", p, url.Values{}).want(t, http.StatusUnauthorized)
	}
	if len(s.fake.Calls()) != 0 {
		t.Errorf("the service was called without a login: %v", s.fake)
	}
	if s.do("GET", "/static/app.css", nil).status != http.StatusOK {
		t.Error("the stylesheet needs no login")
	}
}

func TestLoginCodeWorksOnceAndBriefly(t *testing.T) {
	s := newSite(t, "")
	code := s.auth.NewCode()
	resp := s.do("GET", "/login?code="+code, nil)
	if resp.status != http.StatusSeeOther || resp.header.Get("Location") != "/" {
		t.Fatalf("login: %d %v", resp.status, resp.header)
	}
	var cookie string
	for _, c := range resp.header.Values("Set-Cookie") {
		if strings.HasPrefix(c, "mcs_session=") {
			cookie = c
		}
	}
	for _, want := range []string{"HttpOnly", "SameSite=Strict", "Path=/"} {
		if !strings.Contains(cookie, want) {
			t.Errorf("the session cookie lacks %s: %q", want, cookie)
		}
	}
	if strings.Contains(cookie, "Secure") {
		t.Errorf("Secure on plain HTTP would lose the session: %q", cookie)
	}
	s.do("GET", "/", nil).want(t, http.StatusOK, "Overview")

	other := newSite(t, "")
	other.auth = s.auth // the same codes, another browser
	other.srv = s.srv
	fresh, _ := cookiejar.New(nil)
	other.client = &http.Client{Jar: fresh, CheckRedirect: s.client.CheckRedirect}
	other.do("GET", "/login?code="+code, nil).want(t, http.StatusUnauthorized)

	late := s.auth.NewCode()
	s.clock.t = s.clock.t.Add(webui.CodeTTL + time.Second)
	other.do("GET", "/login?code="+late, nil).want(t, http.StatusUnauthorized)
	other.do("GET", "/login?code=", nil).want(t, http.StatusUnauthorized)
	other.do("GET", "/", nil).want(t, http.StatusUnauthorized)
}

func TestSecureCookieBehindTLS(t *testing.T) {
	s := newSite(t, "")
	resp := s.do("GET", "/login?code="+s.auth.NewCode(), nil, "X-Forwarded-Proto", "https")
	if c := strings.Join(resp.header.Values("Set-Cookie"), ";"); !strings.Contains(c, "Secure") {
		t.Errorf("cookie = %q, want Secure behind TLS", c)
	}
}

func TestSessionEndsAfterIdleTimeAndLogout(t *testing.T) {
	s := newSite(t, "")
	s.login()
	s.clock.t = s.clock.t.Add(webui.SessionIdle - time.Minute)
	s.do("GET", "/", nil).want(t, http.StatusOK) // use extends the session
	s.clock.t = s.clock.t.Add(webui.SessionIdle - time.Minute)
	s.do("GET", "/", nil).want(t, http.StatusOK)
	s.clock.t = s.clock.t.Add(webui.SessionIdle)
	s.do("GET", "/", nil).want(t, http.StatusUnauthorized)

	s.login()
	s.do("POST", "/logout", url.Values{}).want(t, http.StatusUnauthorized)
	s.do("GET", "/", nil).want(t, http.StatusUnauthorized)
}

func TestBasicAuthWithToken(t *testing.T) {
	s := newSite(t, "s3cret-token")
	resp := s.do("GET", "/", nil)
	resp.want(t, http.StatusUnauthorized)
	if !strings.Contains(resp.header.Get("Www-Authenticate"), "Basic") {
		t.Errorf("no Basic challenge: %v", resp.header)
	}
	good := "Basic " + basic("admin", "s3cret-token")
	s.do("GET", "/", nil, "Authorization", good).want(t, http.StatusOK, "Overview")
	s.do("GET", "/", nil, "Authorization", "Basic "+basic("admin", "wrong")).want(t, http.StatusUnauthorized)

	// without a token configured Basic Auth gets nobody in, and no challenge is sent
	none := newSite(t, "")
	r := none.do("GET", "/", nil, "Authorization", "Basic "+basic("admin", ""))
	r.want(t, http.StatusUnauthorized)
	if r.header.Get("Www-Authenticate") != "" {
		t.Error("a Basic challenge without a token")
	}
}

func TestWrongTokensLockBasicAuth(t *testing.T) {
	s := newSite(t, "s3cret-token")
	good := "Basic " + basic("admin", "s3cret-token")
	bad := "Basic " + basic("admin", "guess")

	for range webui.TokenTries - 1 {
		s.do("GET", "/", nil, "Authorization", bad).want(t, http.StatusUnauthorized)
	}
	// the right token in between starts the count again
	s.do("GET", "/", nil, "Authorization", good).want(t, http.StatusOK)
	for range webui.TokenTries {
		s.do("GET", "/", nil, "Authorization", bad).want(t, http.StatusUnauthorized)
	}

	// locked: not even the right token is looked at, a session still works
	r := s.do("GET", "/", nil, "Authorization", good)
	r.want(t, http.StatusTooManyRequests)
	if r.header.Get("Retry-After") != "30" {
		t.Errorf("Retry-After = %q", r.header.Get("Retry-After"))
	}
	s.do("GET", "/", nil).want(t, http.StatusUnauthorized)
	s.login()
	s.do("GET", "/", nil).want(t, http.StatusOK)

	s.clock.t = s.clock.t.Add(webui.TokenLock)
	if ok, wait := s.auth.CheckToken("s3cret-token"); !ok || wait != 0 {
		t.Errorf("after the lock: ok = %v, wait = %v", ok, wait)
	}
}

func basic(user, pass string) string {
	return base64(user + ":" + pass)
}

func TestSecurityHeaders(t *testing.T) {
	s := newSite(t, "")
	s.login()
	h := s.do("GET", "/", nil).header
	for k, want := range map[string]string{
		"X-Frame-Options": "DENY", "Referrer-Policy": "no-referrer", "X-Content-Type-Options": "nosniff", "Cache-Control": "no-store",
	} {
		if h.Get(k) != want {
			t.Errorf("%s = %q, want %q", k, h.Get(k), want)
		}
	}
	csp := h.Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'self'") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("CSP = %q", csp)
	}
}

func TestCrossOriginPostsAreRejected(t *testing.T) {
	s := newSite(t, "")
	s.login()
	s.do("POST", "/backup", url.Values{}, "Sec-Fetch-Site", "cross-site").want(t, http.StatusForbidden)
	s.do("POST", "/backup", url.Values{}, "Origin", "https://evil.example").want(t, http.StatusForbidden)
	if len(s.fake.CallsTo("StartBackup")) != 0 {
		t.Fatal("a cross-origin post started a backup")
	}
	s.do("POST", "/backup", url.Values{}, "Sec-Fetch-Site", "same-origin").want(t, http.StatusSeeOther)
	if len(s.fake.CallsTo("StartBackup")) != 1 {
		t.Error("a same-origin post was refused")
	}
}

func TestOverview(t *testing.T) {
	s := newSite(t, "")
	st := s.fake.StatusFn
	s.fake.StatusFn = func(ctx context.Context) (service.Status, error) {
		v, _ := st(ctx)
		v.Warnings = []service.Warning{{Code: "x", Severity: service.SeverityWarning, Message: "forget is <held> back"}}
		return v, nil
	}
	s.login()
	s.do("GET", "/", nil).want(t, http.StatusOK, "reachable", "2 snapshots", "3h ago", "backups enabled (0 2 * * *)", "keeps the last 7 snapshots", "files: /data",
		"Needs attention", "forget is &lt;held&gt; back", `hx-get="/ui/live"`, "Start a backup")
	s.do("GET", "/ui/live", nil).want(t, http.StatusOK, "reachable")
}

func TestLiveFragmentShowsTheOperation(t *testing.T) {
	s := newSite(t, "")
	st := s.fake.StatusFn
	s.fake.StatusFn = func(ctx context.Context) (service.Status, error) {
		v, _ := st(ctx)
		v.Operation = &service.Operation{Kind: service.KindBackup, Source: "schedule", Started: t0.Add(-time.Minute), Phase: "backup", Progress: &service.Progress{Percent: 0.4, Current: "/data/x"}}
		return v, nil
	}
	s.fake.LogFn = func(_ context.Context, k service.Kind) (service.Log, error) {
		return service.Log{Kind: k, Lines: []string{"using snapshot 1234abcd as parent", "", "scanned 3 files"}}, nil
	}
	s.login()
	live := s.do("GET", "/ui/live", nil)
	live.want(t, http.StatusOK, "Backing up", "backup, started by schedule, running for 1m", `value="40"`, "/data/x",
		"incremental to snapshot 1234abcd\nscanned 3 files\n", `action="/cancel"`)
	// the progress is said once, and the details do not repeat the panel
	if strings.Contains(live.body, "40%,") || strings.Contains(live.body, "<dt>Operation</dt>") {
		t.Errorf("the operation is shown twice:\n%s", live.body)
	}
}

func TestDetailsAndRetry(t *testing.T) {
	s := newSite(t, "")
	st := s.fake.StatusFn
	s.fake.StatusFn = func(ctx context.Context) (service.Status, error) {
		v, _ := st(ctx)
		v.Schedule.Next = map[service.Kind]time.Time{service.KindBackup: t0.Add(time.Hour), service.KindPrune: t0.Add(2 * time.Hour)}
		v.LastRuns = map[service.Kind]service.RunResult{
			service.KindBackup: {Kind: service.KindBackup, Result: service.ResultFailure, Error: "repository not reachable", Started: t0, WillRetry: true},
		}
		v.Retry = &service.RetryInfo{Attempt: 2, Max: 3, At: t0.Add(5 * time.Minute)}
		return v, nil
	}
	s.login()
	// the retry can be cancelled although the failed backup is shown as the last result
	s.do("GET", "/ui/live", nil).want(t, http.StatusOK, "backup: failure", "tried again (2/3)", "Cancel the retry",
		`<dt>Schedule</dt>`, `<dt class="sub">next prune</dt>`)
}

func TestLastResultAndRepeatWithoutSafetySnapshot(t *testing.T) {
	s := newSite(t, "")
	failed := service.RunResult{Kind: service.KindRestore, Result: service.ResultFailure, Error: "safety snapshot failed", Started: t0}
	st := s.fake.StatusFn
	s.fake.StatusFn = func(ctx context.Context) (service.Status, error) {
		v, _ := st(ctx)
		v.LastRuns = map[service.Kind]service.RunResult{service.KindRestore: failed}
		return v, nil
	}
	s.login()
	s.do("GET", "/ui/live", nil).want(t, http.StatusOK, "restore: failure", "safety snapshot failed")
	if strings.Contains(s.do("GET", "/ui/live", nil).body, "no-safety-snapshot") {
		t.Error("a repeat is offered for a restore that was not started here")
	}

	s.do("POST", "/restore", url.Values{"snapshot": {"latest"}, "confirm": {"latest"}})
	// the refresh keeps what was typed into the form
	s.do("GET", "/ui/live", nil).want(t, http.StatusOK, "no-safety-snapshot", `id="repeat-restore" hx-preserve`)

	// the repeat needs the second phrase
	s.do("POST", "/restore", url.Values{"snapshot": {"latest"}, "confirm": {"latest"}, "no_safety_snapshot": {"1"}})
	if n := len(s.fake.CallsTo("StartRestore")); n != 1 {
		t.Fatalf("restore without safety snapshot started without the second confirmation: %d calls", n)
	}
	s.do("POST", "/restore", url.Values{"snapshot": {"latest"}, "confirm": {"latest"}, "no_safety_snapshot": {"1"}, "confirm_no_safety": {"no-safety-snapshot"}})
	calls := s.fake.CallsTo("StartRestore")
	if len(calls) != 2 || !calls[1].Args[0].(service.RestoreRequest).NoSafetySnapshot {
		t.Errorf("calls: %v", s.fake)
	}
}

func TestSnapshotsPageEscapesAndActs(t *testing.T) {
	s := newSite(t, "")
	s.fake.RetentionFn = func(context.Context) (service.RetentionInfo, error) {
		return service.RetentionInfo{Policy: "the last 1 snapshots", Hosts: []string{"app"}, Total: 2, Held: true, Remove: []service.Snapshot{{ShortID: "aaaaaaaa", Time: t0.Add(-48 * time.Hour)}}}, nil
	}
	s.login()
	page := s.do("GET", "/snapshots", nil)
	page.want(t, http.StatusOK, "aaaaaaaa", "SUSPICIOUS", "&lt;b&gt;x&lt;/b&gt;", "would remove 1 snapshots", "held back until it is approved", "/restore?snapshot=aaaaaaaa")
	if strings.Contains(page.body, "<b>x</b>") {
		t.Error("a note was not escaped")
	}
	if i, j := strings.Index(page.body, "bbbbbbbb"), strings.Index(page.body, "aaaaaaaa"); i > j {
		t.Error("the newest snapshot should come first")
	}

	id := strings.Repeat("a", 64)
	s.do("POST", "/snapshots/"+id+"/keep", url.Values{}).want(t, http.StatusSeeOther)
	s.do("POST", "/snapshots/"+id+"/unkeep", url.Values{})
	s.do("POST", "/snapshots/"+id+"/acknowledge", url.Values{"confirm": {"acknowledge"}})
	s.do("POST", "/forget/approve", url.Values{"confirm": {"forget"}})
	if len(s.fake.CallsTo("Keep")) != 1 || len(s.fake.CallsTo("Unkeep")) != 1 {
		t.Errorf("calls: %v", s.fake)
	}
	if c := s.fake.CallsTo("Acknowledge"); len(c) != 1 || c[0].Args[1] != "acknowledge" {
		t.Errorf("calls: %v", s.fake)
	}
	if c := s.fake.CallsTo("ApproveForget"); len(c) != 1 || c[0].Args[0] != "forget" {
		t.Errorf("calls: %v", s.fake)
	}
	for _, c := range s.fake.Calls() {
		if c.Name == "Keep" && !strings.HasPrefix(c.Source, "web@127.0.0.1") {
			t.Errorf("source = %q, want web@<remote address>", c.Source)
		}
	}
}

func TestAppendOnlyHidesTagActions(t *testing.T) {
	s := newSite(t, "")
	st := s.fake.StatusFn
	s.fake.StatusFn = func(ctx context.Context) (service.Status, error) {
		v, _ := st(ctx)
		v.AppendOnly = true
		return v, nil
	}
	s.login()
	body := s.do("GET", "/snapshots", nil).body
	for _, unwanted := range []string{"/keep", "acknowledge"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("the page offers %q on an append-only repository", unwanted)
		}
	}
	if strings.Contains(s.do("GET", "/locks", nil).body, "remove-all") {
		t.Error("remove all locks is offered on an append-only repository")
	}
}

func TestActionsShowTheirOutcome(t *testing.T) {
	s := newSite(t, "")
	s.login()
	resp := s.do("POST", "/backup", url.Values{"note": {"before the move"}, "keep": {"1"}})
	resp.want(t, http.StatusSeeOther)
	if resp.header.Get("Location") != "/" {
		t.Errorf("location = %q", resp.header.Get("Location"))
	}
	if c := s.fake.CallsTo("StartBackup"); len(c) != 1 || c[0].Args[0].(service.BackupRequest) != (service.BackupRequest{Note: "before the move", Keep: true}) {
		t.Errorf("calls: %v", s.fake)
	}
	s.do("GET", "/", nil).want(t, http.StatusOK, "Backup started.")
	if strings.Contains(s.do("GET", "/", nil).body, "Backup started.") {
		t.Error("the message is shown twice")
	}

	s.fake.StartBackupFn = func(context.Context, service.BackupRequest) (service.Operation, error) {
		return service.Operation{}, &service.BusyError{Current: service.Operation{Kind: service.KindPrune}}
	}
	s.do("POST", "/backup", url.Values{})
	s.do("GET", "/", nil).want(t, http.StatusOK, "Another operation is running (prune)")

	for msg, want := range map[string]string{"übel: no space": "Übel: no space", "": "Failed without an error message."} {
		s.fake.StartBackupFn = func(context.Context, service.BackupRequest) (service.Operation, error) {
			return service.Operation{}, errors.New(msg)
		}
		s.do("POST", "/backup", url.Values{}).want(t, http.StatusSeeOther)
		s.do("GET", "/", nil).want(t, http.StatusOK, want)
	}

	// what is not available says why, if the service knows
	s.fake.NotifyTestFn = func(context.Context) error {
		return fmt.Errorf("%w: NOTIFY_COMMAND is not configured", service.ErrUnavailable)
	}
	s.do("POST", "/notify-test", url.Values{}).want(t, http.StatusSeeOther)
	s.do("GET", "/", nil).want(t, http.StatusOK, "Not available: NOTIFY_COMMAND is not configured.")
}

func TestScheduleActions(t *testing.T) {
	s := newSite(t, "")
	s.login()
	s.do("POST", "/schedule", url.Values{"action": {"maintenance"}, "for": {"2h"}})
	s.do("POST", "/schedule", url.Values{"action": {"disable"}})
	s.do("POST", "/schedule", url.Values{"action": {"maintenance"}, "for": {"soon"}})
	s.do("POST", "/schedule", url.Values{"action": {"explode"}})
	calls := s.fake.CallsTo("SetSchedule")
	if len(calls) != 2 {
		t.Fatalf("calls: %v", s.fake)
	}
	if r := calls[0].Args[0].(service.ScheduleRequest); r.Action != service.ScheduleMaintenance || r.For.Std() != 2*time.Hour {
		t.Errorf("request = %+v", r)
	}
	s.do("GET", "/", nil).want(t, http.StatusOK, "Unknown schedule action")
}

func TestSchedulePanelOffersWhatFits(t *testing.T) {
	s := newSite(t, "")
	var sched service.ScheduleStatus
	st := s.fake.StatusFn
	s.fake.StatusFn = func(ctx context.Context) (service.Status, error) {
		v, _ := st(ctx)
		v.Schedule = sched
		return v, nil
	}
	s.login()
	enable, disable, pause := `name="action" value="enable"`, `name="action" value="disable"`, `name="action" value="maintenance"`
	for _, c := range []struct {
		name     string
		sched    service.ScheduleStatus
		text     string
		want, no []string
	}{
		{"on", service.ScheduleStatus{Enabled: true, Backup: "0 2 * * *"}, "backups enabled (0 2 * * *)", []string{disable, pause}, []string{enable}},
		{"paused", service.ScheduleStatus{Enabled: true, Backup: "0 2 * * *", MaintenanceUntil: t0.Add(time.Hour)}, "paused for maintenance", []string{enable}, []string{disable, pause}},
		{"off", service.ScheduleStatus{Backup: "0 2 * * *"}, "backups are disabled", []string{enable}, []string{disable, pause}},
		{"manual", service.ScheduleStatus{Enabled: true}, "backups are started manually", nil, []string{enable, disable, pause}},
	} {
		sched = c.sched
		body := s.do("GET", "/", nil).body
		for _, p := range append(c.want, c.text) {
			if !strings.Contains(body, p) {
				t.Errorf("%s: the page lacks %q", c.name, p)
			}
		}
		for _, p := range c.no {
			if strings.Contains(body, p) {
				t.Errorf("%s: the page offers %q", c.name, p)
			}
		}
	}
}

func TestRestoreFlow(t *testing.T) {
	s := newSite(t, "")
	s.fake.RestorePreflightFn = func(_ context.Context, req service.RestoreRequest) (service.RestorePreflight, error) {
		return service.RestorePreflight{Snapshot: service.Snapshot{ShortID: "aaaaaaaa", Time: t0.Add(-time.Hour)}, Target: "/data", InPlace: req.Target == "", Mode: req.Mode, SafetySnapshot: true, SpaceWarning: req.Target != ""}, nil
	}
	s.login()
	s.do("GET", "/restore?snapshot=aaaaaaaa", nil).want(t, http.StatusOK, `value="aaaaaaaa"`)

	check := s.do("POST", "/restore/check", url.Values{"snapshot": {"aaaaaaaa"}, "mode": {"merge"}, "include": {"/etc\r\n/my docs/a b.txt\n"}})
	check.want(t, http.StatusOK, "replaces the current data", "a snapshot of the current data is taken first", "Type <code>aaaaaaaa</code> to confirm", `name="mode" value="merge"`,
		`name="include" value="/etc"`, `name="include" value="/my docs/a b.txt"`)
	req := s.fake.CallsTo("RestorePreflight")[0].Args[0].(service.RestoreRequest)
	// one path per line, so that a path may contain spaces
	if req.Snapshot != "aaaaaaaa" || req.Mode != service.ModeMerge || !slices.Equal(req.Includes, []string{"/etc", "/my docs/a b.txt"}) {
		t.Errorf("preflight request = %+v", req)
	}

	// the check page sends the paths back one by one
	s.do("POST", "/restore", url.Values{"snapshot": {"aaaaaaaa"}, "mode": {"merge"}, "confirm": {"aaaaaaaa"}, "include": {"/etc", "/my docs/a b.txt"}}).want(t, http.StatusSeeOther)
	got := s.fake.CallsTo("StartRestore")[0].Args[0].(service.RestoreRequest)
	if got.Snapshot != "aaaaaaaa" || got.Confirm != "aaaaaaaa" || got.Mode != service.ModeMerge || got.NoSafetySnapshot || len(got.Includes) != 2 {
		t.Errorf("restore request = %+v", got)
	}

	// latest: the restore takes the snapshot that was checked, also if a backup comes in meanwhile
	s.do("POST", "/restore/check", url.Values{"snapshot": {"latest"}}).want(t, http.StatusOK,
		"Type <code>aaaaaaaa</code> to confirm", `name="snapshot" value="aaaaaaaa"`, `href="/restore?snapshot=latest"`)

	// another target: no typed confirmation, the space warning needs its checkbox
	other := s.do("POST", "/restore/check", url.Values{"snapshot": {"latest"}, "target": {"/tmp/out"}})
	other.want(t, http.StatusOK, "restore although the space looks too small", `href="/restore?snapshot=latest&amp;target=%2Ftmp%2Fout"`)
	if strings.Contains(other.body, "to confirm") {
		t.Error("a restore to another directory asks for a typed confirmation")
	}
	s.do("POST", "/restore", url.Values{"snapshot": {"latest"}, "target": {"/tmp/out"}, "ignore_space": {"1"}})
	if g := s.fake.CallsTo("StartRestore")[1].Args[0].(service.RestoreRequest); !g.IgnoreSpaceWarning || g.Target != "/tmp/out" {
		t.Errorf("restore request = %+v", g)
	}

	s.do("POST", "/restore/check", url.Values{"snapshot": {""}}).want(t, http.StatusSeeOther)

	// a check that fails goes back to the form with everything that was entered
	s.fake.RestorePreflightFn = func(context.Context, service.RestoreRequest) (service.RestorePreflight, error) {
		return service.RestorePreflight{}, errors.New("no snapshot a&b#c")
	}
	failed := s.do("POST", "/restore/check", url.Values{"snapshot": {"a&b#c"}, "target": {"/tmp/x y"}, "include": {"/etc\n/a b"}, "mode": {"merge"}})
	failed.want(t, http.StatusSeeOther)
	s.do("GET", failed.header.Get("Location"), nil).want(t, http.StatusOK, "No snapshot a&amp;b#c",
		`value="a&amp;b#c"`, `value="/tmp/x y"`, ">/etc\n/a b</textarea>", `value="merge" selected`)
}

func TestFilesPageBrowsesASnapshot(t *testing.T) {
	s := newSite(t, "")
	id := strings.Repeat("a", 64)
	s.fake.FilesFn = func(_ context.Context, req service.FilesRequest) (service.FileList, error) {
		if req.Path == "/nosuch" {
			return service.FileList{}, errors.New("snapshot aaaaaaaa has no /nosuch")
		}
		return service.FileList{
			Snapshot: service.Snapshot{ID: id, ShortID: "aaaaaaaa", Time: t0.Add(-time.Hour)}, Path: "/my docs/2026", Truncated: true,
			Files: []service.File{
				{Path: "/my docs/2026/sub", Type: "dir", Mtime: t0},
				{Path: "/my docs/2026/<b>a&b.txt", Type: "file", Size: 2048, Mtime: t0},
			},
		}, nil
	}
	s.do("GET", "/files", nil).want(t, http.StatusUnauthorized)
	s.login()

	page := s.do("GET", "/files?snapshot=latest&path=my+docs/2026", nil)
	page.want(t, http.StatusOK, "in snapshot aaaaaaaa",
		// the way back up, one step at a time
		`<a href="/files?path=%2F&amp;snapshot=`+id+`">top</a>`, `<a href="/files?path=%2Fmy&#43;docs&amp;snapshot=`+id+`">my docs</a>`,
		// a directory opens, everything can be restored by its path
		`<a href="/files?path=%2Fmy&#43;docs%2F2026%2Fsub&amp;snapshot=`+id+`">sub/</a>`,
		`href="/restore?include=%2Fmy&#43;docs%2F2026%2F%3Cb%3Ea%26b.txt&amp;snapshot=`+id+`"`,
		"&lt;b&gt;a&amp;b.txt", "2.0 KiB", "Only the first 2 entries")
	if strings.Contains(page.body, "<b>a") {
		t.Error("a file name is not escaped")
	}
	if req := s.fake.CallsTo("Files")[0].Args[0].(service.FilesRequest); req.Snapshot != "latest" || req.Path != "my docs/2026" {
		t.Errorf("request = %+v", req)
	}

	// the restore link fills the form
	s.do("GET", "/restore?include=%2Fmy+docs%2Fa.txt&snapshot="+id, nil).want(t, http.StatusOK, ">/my docs/a.txt</textarea>", `value="`+id+`"`)

	s.do("GET", "/files?path=/nosuch", nil).want(t, http.StatusOK, "Snapshot aaaaaaaa has no /nosuch", "Find a file")
	s.do("GET", "/snapshots", nil).want(t, http.StatusOK, `href="/files?snapshot=aaaaaaaa"`)
}

func TestRestoreFormInPipeModeAsksOnlyForTheSnapshot(t *testing.T) {
	s := newSite(t, "")
	status := s.fake.StatusFn
	s.fake.StatusFn = func(ctx context.Context) (service.Status, error) {
		st, err := status(ctx)
		st.PipeMode = true
		return st, err
	}
	s.login()
	// also when the file browser links a path of the snapshot
	page := s.do("GET", "/restore?snapshot=aaaaaaaa&include=%2Fdump.sql", nil)
	page.want(t, http.StatusOK, `value="aaaaaaaa"`, "Pipe mode: the whole snapshot goes to the restore script")
	for _, field := range []string{`name="target"`, `name="include"`, `name="mode"`} {
		if strings.Contains(page.body, field) {
			t.Errorf("the form in pipe mode has %s", field)
		}
	}
}

func TestRestoreCheckCanShowWhatWouldChange(t *testing.T) {
	s := newSite(t, "")
	s.fake.RestorePreflightFn = func(_ context.Context, req service.RestoreRequest) (service.RestorePreflight, error) {
		p := service.RestorePreflight{Snapshot: service.Snapshot{ShortID: "aaaaaaaa", Time: t0}, Target: "/data", InPlace: true, Mode: req.Mode}
		if req.Preview {
			p.Changes = &service.RestoreChanges{Overwritten: 3, Deleted: 1, DeletedPaths: []string{"/data/<new>.txt"}}
		}
		return p, nil
	}
	s.login()
	form := url.Values{"snapshot": {"aaaaaaaa"}, "mode": {"merge"}, "include": {"/a b"}}
	s.do("POST", "/restore/check", form).want(t, http.StatusOK, "Show what would change", `name="preview" value="1"`)
	form.Set("preview", "1")
	page := s.do("POST", "/restore/check", form)
	page.want(t, http.StatusOK, "3 overwritten, 1 deleted", "/data/&lt;new&gt;.txt", "Type <code>aaaaaaaa</code> to confirm", `name="include" value="/a b"`)
	if strings.Contains(page.body, "Show what would change") {
		t.Error("the preview is offered again although it is shown")
	}
	if req := s.fake.CallsTo("RestorePreflight")[1].Args[0].(service.RestoreRequest); !req.Preview || req.Mode != service.ModeMerge {
		t.Errorf("request = %+v", req)
	}
}

func TestDiffPage(t *testing.T) {
	s := newSite(t, "")
	s.fake.DiffFn = func(_ context.Context, req service.DiffRequest) (service.DiffResult, error) {
		if req.To == "old" {
			return service.DiffResult{}, errors.New("there is no regular snapshot before old to compare it with")
		}
		return service.DiffResult{From: service.Snapshot{ShortID: "aaaaaaaa", Time: t0}, To: service.Snapshot{ShortID: "bbbbbbbb", Time: t0, Suspicious: true},
			Removed: service.DiffSide{Files: 3, Dirs: 1, Paths: []service.DiffPath{{Path: "/<b>big/", Entries: 4}}}}, nil
	}
	s.login()
	page := s.do("GET", "/diff?to=bbbbbbbb", nil)
	page.want(t, http.StatusOK, "From snapshot aaaaaaaa", "removed  3 files, 1 directory", "/&lt;b&gt;big/  (4 entries)", "acknowledge it on the snapshots page")
	if req := s.fake.CallsTo("Diff")[0].Args[0].(service.DiffRequest); req.To != "bbbbbbbb" || req.From != "" {
		t.Errorf("request = %+v", req)
	}
	s.do("GET", "/diff?to=old", nil).want(t, http.StatusOK, "There is no regular snapshot before old")
	s.do("GET", "/snapshots", nil).want(t, http.StatusOK, `href="/diff?to=`+strings.Repeat("b", 64)+`"`)
}

func TestFilesPageFindsAFile(t *testing.T) {
	s := newSite(t, "")
	newest := service.SnapshotRef{ID: strings.Repeat("n", 64), Time: t0.Add(-time.Hour)}
	oldest := service.SnapshotRef{ID: strings.Repeat("o", 64), Time: t0.Add(-72 * time.Hour)}
	s.fake.FindFn = func(_ context.Context, req service.FindRequest) (service.FindResult, error) {
		if req.Pattern == "*" {
			return service.FindResult{}, errors.New("the pattern matches too many files: narrow it or search a single snapshot")
		}
		if req.Pattern == "none" {
			return service.FindResult{Searched: 7}, nil
		}
		return service.FindResult{Searched: 7, Versions: []service.FileVersion{
			{Path: "/docs/a.txt", Type: "file", Size: 3 << 20, Mtime: t0, Newest: newest, Oldest: oldest, Snapshots: 4},
		}}, nil
	}
	s.login()

	page := s.do("GET", "/files?snapshot=latest&q=a.txt&i=1", nil)
	page.want(t, http.StatusOK, "/docs/a.txt", "3.0 MiB", "<code>nnnnnnnn</code>", "<code>oooooooo</code>", "Searched 7 snapshots",
		// restored from the newest snapshot that has this state, and its folder there
		`href="/restore?include=%2Fdocs%2Fa.txt&amp;snapshot=`+newest.ID+`"`, `href="/files?path=%2Fdocs&amp;snapshot=`+newest.ID+`"`,
		`name="q" value="a.txt"`, `name="i" value="1" checked`)
	if req := s.fake.CallsTo("Find")[0].Args[0].(service.FindRequest); req != (service.FindRequest{Pattern: "a.txt", IgnoreCase: true}) {
		t.Errorf("request = %+v: all snapshots are searched unless asked otherwise", req)
	}
	if len(s.fake.CallsTo("Files")) != 0 {
		t.Error("a search lists a directory as well")
	}

	s.do("GET", "/files?snapshot=abc&q=none&only=1", nil).want(t, http.StatusOK, "Nothing found in 7 snapshots")
	if req := s.fake.CallsTo("Find")[1].Args[0].(service.FindRequest); req.Snapshot != "abc" {
		t.Errorf("request = %+v", req)
	}
	s.do("GET", "/files?q=*", nil).want(t, http.StatusOK, "The pattern matches too many files")
}

func TestLocksAndLog(t *testing.T) {
	s := newSite(t, "")
	s.fake.LocksFn = func(context.Context) ([]service.Lock, error) {
		return []service.Lock{{ID: "1234567890abcdef", Age: service.Duration(2 * time.Hour), Hostname: "old-pod", PID: 7, Stale: true}}, nil
	}
	s.fake.LogFn = func(_ context.Context, k service.Kind) (service.Log, error) {
		return service.Log{Kind: k, Lines: []string{"line <1>"}}, nil
	}
	s.login()
	s.do("GET", "/locks", nil).want(t, http.StatusOK, "12345678", "2h", "old-pod", "stale")
	s.do("POST", "/locks/unlock", url.Values{})
	s.do("POST", "/locks/unlock", url.Values{"all": {"1"}, "confirm": {"remove-all"}})
	calls := s.fake.CallsTo("Unlock")
	if len(calls) != 2 || calls[0].Args[0] != false || calls[1].Args[0] != true || calls[1].Args[1] != "remove-all" {
		t.Errorf("calls: %v", s.fake)
	}
	s.do("GET", "/log/backup", nil).want(t, http.StatusOK, "Log of the last backup run", "line &lt;1&gt;")
}

func TestAccessMasksSecretsUnlessAsked(t *testing.T) {
	s := newSite(t, "")
	s.fake.EnvFn = func(_ context.Context, reveal bool) (service.EnvInfo, error) {
		pw := "***"
		if reveal {
			pw = "hunter2"
		}
		return service.EnvInfo{MinVersion: "0.17", Vars: []service.EnvVar{{Name: "RESTIC_PASSWORD", Value: pw, Secret: true}}, Notes: []string{"note"}}, nil
	}
	s.login()
	s.do("GET", "/access", nil).want(t, http.StatusOK, "RESTIC_PASSWORD", "***", "0.17")
	if strings.Contains(s.do("GET", "/access", nil).body, "hunter2") {
		t.Error("secret shown by default")
	}
	s.do("GET", "/access?reveal=1", nil).want(t, http.StatusOK, "hunter2")
	// the same variables ready for a shell, with the export prefix
	hidden := s.do("GET", "/access", nil)
	hidden.want(t, http.StatusOK, "export RESTIC_PASSWORD=&#39;***&#39;", "RESTIC_PASSWORD=***", "docker run")
	s.do("GET", "/access?reveal=1", nil).want(t, http.StatusOK, "export RESTIC_PASSWORD=&#39;hunter2&#39;")
}

func TestServiceErrorsDoNotBreakPages(t *testing.T) {
	s := newSite(t, "")
	s.fake.SnapshotsFn = func(context.Context) ([]service.Snapshot, error) { return nil, errors.New("repository is gone") }
	s.login()
	s.do("GET", "/snapshots", nil).want(t, http.StatusSeeOther)
	s.do("GET", "/", nil).want(t, http.StatusOK, "Repository is gone")
}

func TestWebOffNeedsTheManager(t *testing.T) {
	s := newSite(t, "")
	s.login()
	if strings.Contains(s.do("GET", "/", nil).body, "/web/off") {
		t.Error("the off button shows without a manager")
	}
	s.do("POST", "/web/off", url.Values{}).want(t, http.StatusSeeOther)
	s.do("GET", "/", nil).want(t, http.StatusOK, "Not available")
}

func base64(s string) string { return b64.StdEncoding.EncodeToString([]byte(s)) }

func TestOverviewShowsCardsHealthAndRuns(t *testing.T) {
	s := newSite(t, "")
	st := s.fake.StatusFn
	s.fake.StatusFn = func(ctx context.Context) (service.Status, error) {
		v, _ := st(ctx)
		v.Warnings = []service.Warning{{Code: "backup_suspicious", Severity: service.SeverityError, Message: "a snapshot is tagged suspicious"}}
		v.LastRuns = map[service.Kind]service.RunResult{
			service.KindBackup: {Kind: service.KindBackup, Result: service.ResultSuccess, Snapshot: "aaaaaaaa11", Started: time.Now().Add(-time.Hour)},
			service.KindVerify: {Kind: service.KindVerify, Result: service.ResultFailure, Started: time.Now().Add(-2 * time.Hour)},
		}
		return v, nil
	}
	s.login()
	page := s.do("GET", "/", nil)
	page.want(t, http.StatusOK,
		`class="pill bad"`, "needs attention", // the state in the header
		`<div class="card ok">`, `class="dot ok">reachable`, "Last backup", "Next backup", // the headline figures
		`class="attention bad"`, "a snapshot is tagged suspicious", // the worst severity colors the frame
		"Last runs", `class="dot ok">success`, `class="dot bad">failure`, "<code>aaaaaaaa</code>")
	if strings.Contains(page.body, "!! a snapshot") {
		t.Error("the severity mark of the text is still there")
	}
	// the repository is not listed twice: it is a card, not a row of the details
	if strings.Contains(page.body, "<dt>Repository</dt>") || strings.Contains(page.body, "<dt>Last backup</dt>") {
		t.Errorf("the details repeat the cards:\n%s", page.body)
	}
}

func TestTheBackupIsTheLastResultNotItsCopy(t *testing.T) {
	s := newSite(t, "")
	st := s.fake.StatusFn
	s.fake.StatusFn = func(ctx context.Context) (service.Status, error) {
		v, _ := st(ctx)
		// a backup with a copy records both with the same start
		v.LastRuns = map[service.Kind]service.RunResult{
			service.KindBackup: {Kind: service.KindBackup, Result: service.ResultSuccess, Started: t0},
			service.KindCopy:   {Kind: service.KindCopy, Result: service.ResultSuccess, Started: t0},
			service.KindVerify: {Kind: service.KindVerify, Result: service.ResultSuccess, Started: t0.Add(-time.Hour)},
		}
		return v, nil
	}
	s.login()
	// the order of a map is random: ask often enough to see a wrong one
	for range 20 {
		body := s.do("GET", "/ui/live", nil).body
		if !strings.Contains(body, "backup: success") {
			t.Fatalf("the last result is not the backup:\n%s", body)
		}
		b, c, v := strings.Index(body, "<td>backup</td>"), strings.Index(body, "<td>copy</td>"), strings.Index(body, "<td>verify</td>")
		if b < 0 || b > c || c > v {
			t.Fatalf("the runs are not backup, copy, verify:\n%s", body)
		}
	}
}

func TestNotesForInformationAreNotAlarms(t *testing.T) {
	s := newSite(t, "")
	var warnings []service.Warning
	st := s.fake.StatusFn
	s.fake.StatusFn = func(ctx context.Context) (service.Status, error) {
		v, _ := st(ctx)
		v.Warnings = warnings
		return v, nil
	}
	s.login()
	warnings = []service.Warning{{Code: "maintenance", Severity: service.SeverityInfo, Message: "scheduled backups are paused"}}
	body := s.do("GET", "/ui/live", nil).body
	if !strings.Contains(body, `class="attention info"`) || !strings.Contains(body, "<h2>Notes</h2>") || strings.Contains(body, "Needs attention") {
		t.Errorf("a note for information looks like an alarm:\n%s", body)
	}
	warnings = append(warnings, service.Warning{Code: "stale_locks", Severity: service.SeverityWarning, Message: "1 stale locks"})
	s.do("GET", "/ui/live", nil).want(t, http.StatusOK, `class="attention warn"`, "Needs attention")
}

func TestTheRefreshBringsTheHealthAlong(t *testing.T) {
	s := newSite(t, "")
	s.login()
	page := s.do("GET", "/", nil)
	page.want(t, http.StatusOK, `id="health"`, `src="/static/app.js"`, `class="banner stale-note"`)
	if n := strings.Count(page.body, `id="health"`); n != 1 {
		t.Errorf("the page has %d health indicators, want 1", n)
	}
	s.do("GET", "/ui/live", nil).want(t, http.StatusOK, `<span id="health" class="pill ok" hx-swap-oob="true">healthy</span>`)
	s.do("GET", "/static/app.js", nil).want(t, http.StatusOK, "htmx:afterRequest")
}

func TestSnapshotFlagsAreBadges(t *testing.T) {
	s := newSite(t, "")
	s.fake.SnapshotsFn = func(context.Context) ([]service.Snapshot, error) {
		return []service.Snapshot{
			{ID: "aaaaaaaa00", ShortID: "aaaaaaaa", Time: time.Now(), Suspicious: true},
			{ID: "bbbbbbbb00", ShortID: "bbbbbbbb", Time: time.Now().Add(-time.Hour), Keep: true},
			{ID: "cccccccc00", ShortID: "cccccccc", Time: time.Now().Add(-2 * time.Hour), PreRestore: true},
		}, nil
	}
	s.login()
	s.do("GET", "/snapshots", nil).want(t, http.StatusOK,
		`<span class="badge bad">SUSPICIOUS</span>`, `<span class="badge ok">keep</span>`, `<span class="badge warn">pre-restore</span>`)
}
