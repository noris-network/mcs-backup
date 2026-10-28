package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/noris-network/mcs-backup/v2/internal/service"
	"github.com/noris-network/mcs-backup/v2/internal/servicetest"
)

var t0 = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

// rig drives the model like Bubble Tea does: keys go in, the commands that
// come out are run and their messages go back in.
type rig struct {
	t    *testing.T
	m    *Model
	fake *servicetest.Fake
	quit bool
	// copied and opened record what the model asked the machine to do
	copied string
	opened string
}

func newRig(t *testing.T) *rig {
	t.Helper()
	fake := &servicetest.Fake{}
	r := &rig{t: t, fake: fake}
	r.m = New(service.WithSource(t.Context(), service.SourceTUI), fake, Options{
		Now:  func() time.Time { return t0 },
		Open: func(url string) error { r.opened = url; return nil },
		Copy: func(text string) error { r.copied = text; return nil },
		Hint: func(string) string { return "kubectl port-forward pod/app 8080" },
	})
	r.m.width, r.m.height = 110, 40
	return r
}

// load fills the model the way the first refresh does.
func (r *rig) load() *rig {
	r.t.Helper()
	r.exec(r.m.refresh())
	return r
}

func (r *rig) send(msg tea.Msg) {
	r.t.Helper()
	_, cmd := r.m.Update(msg)
	r.exec(cmd)
}

// exec runs a command and feeds its message back. Commands that wait
// (timers) are dropped.
func (r *rig) exec(cmd tea.Cmd) {
	r.t.Helper()
	if cmd == nil {
		return
	}
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	select {
	case msg := <-ch:
		switch msg := msg.(type) {
		case tea.BatchMsg:
			for _, c := range msg {
				r.exec(c)
			}
		case tea.QuitMsg:
			r.quit = true
		case nil:
		default:
			r.send(msg)
		}
	case <-time.After(100 * time.Millisecond):
	}
}

var special = map[string]tea.KeyPressMsg{
	"enter":     {Code: tea.KeyEnter},
	"esc":       {Code: tea.KeyEscape},
	"tab":       {Code: tea.KeyTab},
	"shift+tab": {Code: tea.KeyTab, Mod: tea.ModShift},
	"up":        {Code: tea.KeyUp},
	"down":      {Code: tea.KeyDown},
	"left":      {Code: tea.KeyLeft},
	"right":     {Code: tea.KeyRight},
}

// press sends keys: a name from special or a single character.
func (r *rig) press(keys ...string) {
	r.t.Helper()
	for _, k := range keys {
		if msg, ok := special[k]; ok {
			r.send(msg)
			continue
		}
		rs := []rune(k)
		r.send(tea.KeyPressMsg{Code: rs[0], Text: k})
	}
}

// typeText types text into the focused input.
func (r *rig) typeText(s string) {
	r.t.Helper()
	for _, c := range s {
		r.press(string(c))
	}
}

// screen returns what the user sees, without styling.
func (r *rig) screen() string { return ansi.Strip(r.m.View().Content) }

func (r *rig) wantScreen(parts ...string) {
	r.t.Helper()
	s := r.screen()
	for _, p := range parts {
		if !strings.Contains(s, p) {
			r.t.Errorf("the screen lacks %q:\n%s", p, s)
		}
	}
}

func (r *rig) wantNoScreen(parts ...string) {
	r.t.Helper()
	s := r.screen()
	for _, p := range parts {
		if strings.Contains(s, p) {
			r.t.Errorf("the screen shows %q:\n%s", p, s)
		}
	}
}

func snap(id string, age time.Duration, mod ...func(*service.Snapshot)) service.Snapshot {
	s := service.Snapshot{ID: id + strings.Repeat("0", 56), ShortID: id, Time: t0.Add(-age), Host: "app", Origin: service.OriginScheduled, HasSummary: true, Files: 10, Bytes: 4096}
	for _, f := range mod {
		f(&s)
	}
	return s
}

// healthy is a fake with a typical service behind it.
func healthy(r *rig) *rig {
	f := r.fake
	f.StatusFn = func(context.Context) (service.Status, error) {
		return service.Status{
			Version: "2.0.0", Host: "app", StartedAt: t0.Add(-time.Hour), Ready: true,
			Repository: service.RepoStatus{Up: true, ID: "abcdef0123456789", Snapshots: 3, SizeBytes: 1 << 20, LastSuccess: t0.Add(-3 * time.Hour)},
			Schedule:   service.ScheduleStatus{Enabled: true, Backup: "0 2 * * *", Next: map[service.Kind]time.Time{service.KindBackup: t0.Add(14 * time.Hour)}},
			Retention:  "the last 7 snapshots", VerifyConfigured: true,
		}, nil
	}
	f.SnapshotsFn = func(context.Context) ([]service.Snapshot, error) {
		return []service.Snapshot{snap("aaaaaaaa", 48*time.Hour), snap("bbbbbbbb", 24*time.Hour), snap("cccccccc", time.Hour, func(s *service.Snapshot) { s.Keep = true; s.Note = "before upgrade" })}, nil
	}
	f.LocksFn = func(context.Context) ([]service.Lock, error) { return nil, nil }
	f.EnvFn = func(_ context.Context, reveal bool) (service.EnvInfo, error) {
		pw := "***"
		if reveal {
			pw = "hunter2"
		}
		return service.EnvInfo{MinVersion: "0.17", Vars: []service.EnvVar{{Name: "RESTIC_REPOSITORY", Value: "/repo"}, {Name: "RESTIC_PASSWORD", Value: pw, Secret: true}}, Notes: []string{"The repository is a local path."}}, nil
	}
	return r
}
