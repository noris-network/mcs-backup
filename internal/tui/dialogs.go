package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/noris-network/mcs-backup/v2/internal/present"
	"github.com/noris-network/mcs-backup/v2/internal/service"
)

// dialog is a modal box in front of the tabs. key returns true when the
// dialog is done.
type dialog interface {
	key(m *Model, k tea.KeyPressMsg) (cmd tea.Cmd, closed bool)
	view(width int) string
}

func newInput(placeholder string) textinput.Model {
	in := textinput.New()
	in.Placeholder = placeholder
	in.Prompt = "> "
	in.Focus()
	return in
}

// typing feeds a key to a text input. The cursor does not blink, so there
// are no timer commands to run.
func typing(in *textinput.Model, k tea.KeyPressMsg) tea.Cmd {
	*in, _ = in.Update(k)
	return nil
}

// ---- confirmation by typing a word ----

type confirmDialog struct {
	title, text, word string
	in                textinput.Model
	err               string
	ok                func(word string) tea.Cmd
}

func newConfirmDialog(title, text, word string, ok func(word string) tea.Cmd) *confirmDialog {
	return &confirmDialog{title: title, text: text, word: word, in: newInput(word), ok: ok}
}

func (d *confirmDialog) key(_ *Model, k tea.KeyPressMsg) (tea.Cmd, bool) {
	switch k.String() {
	case "esc", "ctrl+c":
		return nil, true
	case "enter":
		if d.in.Value() != d.word {
			d.err = "That is not " + d.word + "; nothing was done."
			return nil, false
		}
		return d.ok(d.word), true
	}
	return typing(&d.in, k), false
}

func (d *confirmDialog) view(width int) string {
	var b strings.Builder
	b.WriteString(wrap(d.text, width-6) + "\n\n" + d.in.View() + "\n")
	if d.err != "" {
		b.WriteString("\n" + styleBad.Render(d.err) + "\n")
	}
	return box(d.title, b.String(), "enter confirm · esc cancel", width)
}

// ---- one line of text, optionally with a checkbox ----

type promptDialog struct {
	title, label string
	in           textinput.Model
	checkbox     string // label of the checkbox, none if empty
	checked      bool
	err          string
	submit       func(value string, checked bool) (tea.Cmd, string)
}

func (d *promptDialog) key(_ *Model, k tea.KeyPressMsg) (tea.Cmd, bool) {
	switch k.String() {
	case "esc", "ctrl+c":
		return nil, true
	case "tab":
		if d.checkbox != "" {
			d.checked = !d.checked
		}
		return nil, false
	case "enter":
		cmd, problem := d.submit(strings.TrimSpace(d.in.Value()), d.checked)
		if problem != "" {
			d.err = problem
			return nil, false
		}
		return cmd, true
	}
	return typing(&d.in, k), false
}

func (d *promptDialog) view(width int) string {
	var b strings.Builder
	b.WriteString(d.label + "\n\n" + d.in.View() + "\n")
	hint := "enter ok · esc cancel"
	if d.checkbox != "" {
		mark := "[ ]"
		if d.checked {
			mark = "[x]"
		}
		b.WriteString("\n" + mark + " " + d.checkbox + "\n")
		hint = "enter ok · tab toggle · esc cancel"
	}
	if d.err != "" {
		b.WriteString("\n" + styleBad.Render(d.err) + "\n")
	}
	return box(d.title, b.String(), hint, width)
}

func newBackupDialog(m *Model) *promptDialog {
	d := &promptDialog{title: "Start a backup", label: "Note for the snapshot (optional):", in: newInput("e.g. before the upgrade"), checkbox: "keep: the retention never deletes this snapshot"}
	d.submit = func(note string, keep bool) (tea.Cmd, string) {
		return m.start("backup", func() (service.Operation, error) {
			return m.svc.StartBackup(m.ctx, service.BackupRequest{Note: note, Keep: keep})
		}), ""
	}
	return d
}

// ---- schedule menu ----

type menuItem struct {
	label string
	do    func(m *Model) tea.Cmd
}

type menuDialog struct {
	title string
	items []menuItem
	at    int
}

func (d *menuDialog) key(m *Model, k tea.KeyPressMsg) (tea.Cmd, bool) {
	switch k.String() {
	case "esc", "q", "ctrl+c":
		return nil, true
	case "up", "k":
		d.at = max(0, d.at-1)
	case "down", "j":
		d.at = min(len(d.items)-1, d.at+1)
	case "enter":
		return d.items[d.at].do(m), true
	}
	return nil, false
}

func (d *menuDialog) view(width int) string {
	var b strings.Builder
	for i, it := range d.items {
		if i == d.at {
			b.WriteString(styleSel.Render("> "+it.label) + "\n")
		} else {
			b.WriteString("  " + it.label + "\n")
		}
	}
	return box(d.title, b.String(), "enter choose · esc close", width)
}

func newScheduleDialog() *menuDialog {
	set := func(req service.ScheduleRequest) func(*Model) tea.Cmd {
		return func(m *Model) tea.Cmd {
			return act("schedule", func() error { _, err := m.svc.SetSchedule(m.ctx, req); return err })
		}
	}
	window := func(d time.Duration) menuItem {
		return menuItem{"Pause for " + present.Duration(d) + " (maintenance)", set(service.ScheduleRequest{Action: service.ScheduleMaintenance, For: service.Duration(d)})}
	}
	return &menuDialog{title: "Scheduled backups", items: []menuItem{
		{"Enable", set(service.ScheduleRequest{Action: service.ScheduleEnable})},
		{"Disable until enabled again or restarted", set(service.ScheduleRequest{Action: service.ScheduleDisable})},
		window(time.Hour), window(4 * time.Hour), window(24 * time.Hour),
		{"Pause for another time …", func(m *Model) tea.Cmd {
			d := &promptDialog{title: "Maintenance window", label: "How long? (for example 90m or 2h)", in: newInput("2h")}
			d.submit = func(v string, _ bool) (tea.Cmd, string) {
				dur, err := time.ParseDuration(v)
				if err != nil || dur <= 0 {
					return nil, "Not a duration. Use something like 90m or 2h."
				}
				return set(service.ScheduleRequest{Action: service.ScheduleMaintenance, For: service.Duration(dur)})(m), ""
			}
			m.dlg = d
			return nil
		}},
	}}
}

// ---- scrollable text (the log) ----

type textDialog struct {
	title  string
	lines  []string
	top    int
	follow bool // new lines are appended and the end stays in view
}

func (d *textDialog) key(m *Model, k tea.KeyPressMsg) (tea.Cmd, bool) {
	page := max(3, m.height-10)
	switch k.String() {
	case "esc", "q", "ctrl+c", "enter":
		return nil, true
	case "up", "k":
		d.top, d.follow = max(0, d.top-1), false
	case "down", "j":
		d.top++
	case "pgup":
		d.top, d.follow = max(0, d.top-page), false
	case "pgdown", "space":
		d.top += page
	case "G", "end":
		d.follow = true
	case "g", "home":
		d.top, d.follow = 0, false
	}
	return nil, false
}

func (d *textDialog) view(width int) string {
	const height = 18
	top := d.top
	if d.follow || top > len(d.lines)-height {
		top = max(0, len(d.lines)-height)
	}
	d.top = top
	end := min(len(d.lines), top+height)
	body := "(the log is empty)"
	if len(d.lines) > 0 {
		body = strings.Join(clip(d.lines[top:end], width-6), "\n")
	}
	return box(d.title, body, "↑↓ pgup pgdn scroll · G end · esc close", width)
}

// ---- web UI login link ----

type linkDialog struct {
	url, hint string
}

func (d *linkDialog) key(m *Model, k tea.KeyPressMsg) (tea.Cmd, bool) {
	switch k.String() {
	case "esc", "q", "enter", "ctrl+c":
		return nil, true
	case "c":
		return copyText(m.o, d.url, "login link copied"), false
	case "o":
		if m.o.Open == nil {
			m.say("this machine cannot open a browser; copy the link instead")
			return nil, false
		}
		return act("open browser", func() error { return m.o.Open(d.url) }), false
	case "d":
		return act("web UI off", func() error {
			_, err := m.svc.SetWebUI(m.ctx, service.WebUIRequest{Enable: false})
			return err
		}), true
	}
	return nil, false
}

func (d *linkDialog) view(width int) string {
	text := "Open this link in a browser. It works once and for two minutes:\n\n" + styleLink.Render(d.url) + "\n"
	if d.hint != "" {
		text += "\nFrom another machine:\n" + d.hint + "\n"
	}
	return box("Web UI", text, "c copy · o open here · d switch the web UI off · esc close", width)
}

// ---- restore ----

type restoreStage int

const (
	stageForm restoreStage = iota
	stagePreview
	stageConfirm
)

type restoreDialog struct {
	stage   restoreStage
	fields  [3]textinput.Model // snapshot, target, include
	labels  [3]string
	at      int  // focused field, 3 is the mode
	merge   bool // mode: merge instead of exact
	req     service.RestoreRequest
	pre     *service.RestorePreflight
	ignore  bool // restore despite the space warning
	now     time.Time
	confirm textinput.Model
	err     string
}

func newRestoreDialog(snapshot string) *restoreDialog {
	d := &restoreDialog{
		labels: [3]string{"Snapshot (ID or latest)", "Target directory (empty: replace the live data)", "Only these paths (optional, separated by spaces)"},
		fields: [3]textinput.Model{newInput("latest"), newInput("/path/to/directory"), newInput("/path/in/snapshot")},
	}
	d.fields[0].SetValue(snapshot)
	d.focus()
	return d
}

func (d *restoreDialog) focus() {
	for i := range d.fields {
		if i == d.at {
			d.fields[i].Focus()
		} else {
			d.fields[i].Blur()
		}
	}
}

func (d *restoreDialog) key(m *Model, k tea.KeyPressMsg) (tea.Cmd, bool) {
	if k.String() == "ctrl+c" {
		return nil, true
	}
	switch d.stage {
	case stageForm:
		return d.formKey(m, k)
	case stagePreview:
		return d.previewKey(m, k)
	default:
		return d.confirmKey(m, k)
	}
}

func (d *restoreDialog) formKey(m *Model, k tea.KeyPressMsg) (tea.Cmd, bool) {
	switch k.String() {
	case "esc":
		return nil, true
	case "tab", "down":
		d.at = (d.at + 1) % 4
		d.focus()
	case "shift+tab", "up":
		d.at = (d.at + 3) % 4
		d.focus()
	case "left", "right", "space":
		if d.at == 3 {
			d.merge = !d.merge
			return nil, false
		}
		if d.at < 3 {
			return typing(&d.fields[d.at], k), false
		}
	case "enter":
		snap := strings.TrimSpace(d.fields[0].Value())
		if snap == "" {
			d.err = "Name a snapshot, or latest."
			return nil, false
		}
		req := service.RestoreRequest{Snapshot: snap, Target: strings.TrimSpace(d.fields[1].Value()), Includes: strings.Fields(d.fields[2].Value()), Mode: service.ModeExact}
		if d.merge {
			req.Mode = service.ModeMerge
		}
		d.err = ""
		return fetchPreflight(m.ctx, m.svc, req), false
	default:
		if d.at < 3 {
			return typing(&d.fields[d.at], k), false
		}
	}
	return nil, false
}

func (d *restoreDialog) previewKey(m *Model, k tea.KeyPressMsg) (tea.Cmd, bool) {
	switch k.String() {
	case "esc":
		d.stage, d.err = stageForm, ""
	case "f":
		if d.pre.SpaceWarning {
			d.ignore = !d.ignore
		}
	case "enter":
		if d.pre.SpaceWarning && !d.ignore {
			d.err = "There is not enough free space. Press f to restore anyway."
			return nil, false
		}
		req := d.req
		req.IgnoreSpaceWarning = d.ignore
		d.req = req
		if !d.pre.InPlace {
			return m.startRestore(req), true
		}
		d.stage, d.err = stageConfirm, ""
		d.confirm = newInput(req.Snapshot)
	}
	return nil, false
}

func (d *restoreDialog) confirmKey(m *Model, k tea.KeyPressMsg) (tea.Cmd, bool) {
	switch k.String() {
	case "esc":
		d.stage, d.err = stagePreview, ""
	case "enter":
		if d.confirm.Value() != d.req.Snapshot {
			d.err = "That is not " + d.req.Snapshot + "; nothing was done."
			return nil, false
		}
		req := d.req
		req.Confirm = d.confirm.Value()
		return m.startRestore(req), true
	default:
		return typing(&d.confirm, k), false
	}
	return nil, false
}

func (d *restoreDialog) view(width int) string {
	var b strings.Builder
	hint := ""
	switch d.stage {
	case stageForm:
		for i := range d.fields {
			b.WriteString(d.labels[i] + "\n" + d.fields[i].View() + "\n\n")
		}
		mode := "exact (files that are not in the snapshot are removed)"
		if d.merge {
			mode = "merge (files that are not in the snapshot stay)"
		}
		cursor := "  "
		if d.at == 3 {
			cursor = "> "
		}
		b.WriteString("Mode when replacing the live data\n" + cursor + mode + "\n")
		hint = "tab next field · ←→ change mode · enter check · esc cancel"
	case stagePreview:
		b.WriteString(rows(present.PreflightRows(*d.pre, d.now)) + "\n")
		if d.pre.SpaceWarning {
			mark := "[ ]"
			if d.ignore {
				mark = "[x]"
			}
			b.WriteString("\n" + mark + " restore although the space looks too small (f)\n")
		}
		hint = "enter continue · esc back"
	case stageConfirm:
		b.WriteString(wrap("This replaces the current data. Type "+d.req.Snapshot+" to confirm.", width-6) + "\n\n" + d.confirm.View() + "\n")
		hint = "enter restore · esc back"
	}
	if d.err != "" {
		b.WriteString("\n" + styleBad.Render(d.err) + "\n")
	}
	return box("Restore", b.String(), hint, width)
}

// rows renders labeled rows as aligned text.
func rows(rs []present.Row) string {
	w := 0
	for _, r := range rs {
		w = max(w, len(r.Label))
	}
	var b strings.Builder
	for _, r := range rs {
		text := r.Text
		if r.Severity == service.SeverityWarning || r.Severity == service.SeverityError {
			text = styleBad.Render(text)
		}
		fmt.Fprintf(&b, "%-*s  %s\n", w, r.Label, text)
	}
	return strings.TrimRight(b.String(), "\n")
}
