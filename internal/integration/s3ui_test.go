//go:build integration

package integration

import (
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/noris-network/mcs-backup/v2/internal/api"
	"github.com/noris-network/mcs-backup/v2/internal/service"
	"github.com/noris-network/mcs-backup/v2/internal/tui"
)

func TestS3BackupRestoreAndVerify(t *testing.T) {
	i := newS3Inst(t, "basic")
	i.fillData(1)
	i.serve()
	i.cli("backup", "--note", "first")
	i.blob("db/main.dat", 300<<10, 2)
	changed := i.read("db/main.dat")
	i.cli("backup", "--note", "second")
	if n := len(i.cliSnapshots()); n != 2 {
		t.Fatalf("%d snapshots, want 2", n)
	}
	i.remove("db")
	i.cli("restore", "--confirm", "latest")
	if got := i.read("db/main.dat"); got != changed {
		t.Errorf("db/main.dat was not restored (%d bytes)", len(got))
	}
	if out := i.cli("verify"); strings.Contains(strings.ToLower(out), "fail") {
		t.Errorf("verify:\n%s", out)
	}
}

func TestS3WebUIShowsRealData(t *testing.T) {
	i := newS3Inst(t, "web", "MCS_BACKUP_WEB_ADDR="+freeAddr(t))
	base := "http://" + i.webAddr()
	i.serve()
	i.history()

	c := browser(t)
	if code, body := do(t, c, http.MethodGet, i.loginLink(), nil, nil); code != http.StatusOK {
		t.Fatalf("login: %d\n%s", code, body)
	}
	get := func(page string, want ...string) {
		t.Helper()
		code, body := do(t, c, http.MethodGet, base+page, nil, nil)
		if code != http.StatusOK {
			t.Fatalf("GET %s: %d", page, code)
		}
		for _, w := range want {
			if !strings.Contains(body, w) {
				t.Errorf("GET %s lacks %q:\n%s", page, w, body)
			}
		}
	}
	get("/", "suspicious")
	get("/snapshots", "initial import", "after the release", "volume came up half empty", "pre-restore", "class=\"bad\"")
	get("/access", "RESTIC_REPOSITORY")

	// keep a snapshot from the browser
	nightly, ok := findNote(i.cliSnapshots(), "nightly")
	if !ok {
		t.Fatal("no nightly snapshot")
	}
	get("/restore?snapshot="+nightly.ShortID, nightly.ShortID)
	form := http.Header{"Origin": {base}}
	if code, body := do(t, c, http.MethodPost, base+"/snapshots/"+nightly.ID+"/keep", form, url.Values{}); code != http.StatusOK {
		t.Fatalf("keep: %d\n%s", code, body)
	}
	if s, _ := findNote(i.cliSnapshots(), "nightly"); !s.Keep {
		t.Errorf("the snapshot is not kept after the click: %+v", s)
	}

	// the suspicious snapshot is accepted with the typed word
	sus, ok := findNote(i.cliSnapshots(), "volume came up half empty")
	if !ok {
		t.Fatal("no suspicious snapshot")
	}
	if _, body := do(t, c, http.MethodPost, base+"/snapshots/"+sus.ID+"/acknowledge", form, url.Values{"confirm": {"wrong"}}); strings.Contains(body, "acknowledged") {
		t.Errorf("a wrong word acknowledged the snapshot:\n%s", body)
	}
	if s, _ := findNote(i.cliSnapshots(), "volume came up half empty"); !s.Suspicious {
		t.Fatal("a wrong confirmation changed the snapshot")
	}
	do(t, c, http.MethodPost, base+"/snapshots/"+sus.ID+"/acknowledge", form, url.Values{"confirm": {"acknowledge"}})
	if s, _ := findNote(i.cliSnapshots(), "volume came up half empty"); s.Suspicious {
		t.Error("the snapshot is still suspicious after it was acknowledged")
	}
}

// webAddr is the address of the web UI of the instance.
func (i *inst) webAddr() string {
	for k := len(i.env) - 1; k >= 0; k-- {
		if v, ok := strings.CutPrefix(i.env[k], "MCS_BACKUP_WEB_ADDR="); ok {
			return v
		}
	}
	i.t.Fatal("MCS_BACKUP_WEB_ADDR is not set")
	return ""
}

func TestS3TUIShowsRealData(t *testing.T) {
	i := newS3Inst(t, "tui")
	i.serve()
	i.history()

	client := api.NewClient(filepath.Join(i.home, "s.sock"), service.SourceTUI)
	t.Cleanup(client.Close)
	d := newTUIDriver(t, tui.New(service.WithSource(t.Context(), service.SourceTUI), client, tui.Options{}))

	d.until("the overview with the warning", func(s string) bool { return strings.Contains(strings.ToLower(s), "suspicious") })

	d.press("2")
	d.until("the snapshots", func(s string) bool {
		for _, want := range []string{"initial import", "after the release", "volume came up half empty", "SUSPICIOUS", "pre-restore"} {
			if !strings.Contains(s, want) {
				return false
			}
		}
		return true
	})

	// secrets stay masked on the access tab
	d.press("4")
	d.until("the access tab", func(s string) bool { return strings.Contains(s, "RESTIC_REPOSITORY") })
	secret := strings.TrimPrefix(i.cloud[1], "AWS_SECRET_ACCESS_KEY=")
	if s := d.screen(); strings.Contains(s, "it-password") || strings.Contains(s, secret) {
		t.Errorf("the access tab shows a secret:\n%s", s)
	}

	// acknowledge the suspicious snapshot: newest first, so it is the third row
	d.press("2", "down", "down", "a")
	d.until("the confirmation", func(s string) bool { return strings.Contains(s, "Acknowledge the size change") })
	d.typeText("acknowledge")
	d.press("enter")
	d.until("the snapshot to be regular", func(s string) bool {
		return strings.Contains(s, "volume came up half empty") && !strings.Contains(s, "SUSPICIOUS")
	})
	if s, _ := findNote(i.cliSnapshots(), "volume came up half empty"); s.Suspicious {
		t.Error("the service still has the snapshot as suspicious")
	}
}

// ---- driving the TUI model with a real service behind it ----

type tuiDriver struct {
	t    *testing.T
	m    *tui.Model
	msgs chan tea.Msg
}

func newTUIDriver(t *testing.T, m *tui.Model) *tuiDriver {
	t.Helper()
	d := &tuiDriver{t: t, m: m, msgs: make(chan tea.Msg, 64)}
	_, cmd := m.Update(tea.WindowSizeMsg{Width: 110, Height: 40})
	d.start(cmd)
	d.start(m.Init())
	return d
}

// start runs a command in the background; its message arrives in until.
func (d *tuiDriver) start(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	go func() {
		select {
		case d.msgs <- cmd():
		case <-d.t.Context().Done():
		}
	}()
}

func (d *tuiDriver) handle(msg tea.Msg) {
	switch msg := msg.(type) {
	case nil, tea.QuitMsg:
	case tea.BatchMsg:
		for _, c := range msg {
			d.start(c)
		}
	default:
		_, cmd := d.m.Update(msg)
		d.start(cmd)
	}
}

func (d *tuiDriver) screen() string { return ansi.Strip(d.m.View().Content) }

// until feeds the model with the results of its commands until the screen
// satisfies cond.
func (d *tuiDriver) until(what string, cond func(screen string) bool) {
	d.t.Helper()
	deadline := time.After(30 * time.Second)
	for !cond(d.screen()) {
		select {
		case msg := <-d.msgs:
			d.handle(msg)
		case <-deadline:
			d.t.Fatalf("timed out waiting for %s; the screen shows:\n%s", what, d.screen())
		}
	}
}

var keyNames = map[string]tea.KeyPressMsg{
	"enter": {Code: tea.KeyEnter},
	"esc":   {Code: tea.KeyEscape},
	"up":    {Code: tea.KeyUp},
	"down":  {Code: tea.KeyDown},
}

func (d *tuiDriver) press(keys ...string) {
	d.t.Helper()
	for _, k := range keys {
		msg, ok := keyNames[k]
		if !ok {
			r := []rune(k)
			msg = tea.KeyPressMsg{Code: r[0], Text: k}
		}
		_, cmd := d.m.Update(msg)
		d.start(cmd)
	}
}

func (d *tuiDriver) typeText(s string) {
	d.t.Helper()
	for _, c := range s {
		d.press(string(c))
	}
}
