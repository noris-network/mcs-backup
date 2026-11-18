package tui

import (
	"fmt"
	"math"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/noris-network/mcs-backup/v2/internal/present"
	"github.com/noris-network/mcs-backup/v2/internal/service"
)

// dialog is a modal box in front of the tabs. key returns true when the
// dialog is done. view gets the room there is: a dialog that can grow longer
// than height lines scrolls or shortens its list to fit.
type dialog interface {
	key(m *Model, k tea.KeyPressMsg) (cmd tea.Cmd, closed bool)
	view(width, height int) string
}

// boxLines is how many lines box puts around a body: the frame, the title,
// the hint and the blank lines after the title and before the hint.
const boxLines = 6

// scroll returns the part of lines that fits into room lines, starting at
// *top, which it keeps in range. pos says which lines are in view, for the
// hint of the dialog; it is empty when all of them fit.
func scroll(lines []string, room int, top *int) (shown []string, pos string) {
	room = max(room, 1)
	if len(lines) <= room {
		*top = 0
		return lines, ""
	}
	*top = max(0, min(*top, len(lines)-room))
	end := *top + room
	return lines[*top:end], fmt.Sprintf("lines %d-%d of %d", *top+1, end, len(lines))
}

// scrollKey moves *top by a key that scrolls, with page lines in view, and
// tells whether k was such a key. scroll keeps *top in range.
func scrollKey(k string, top *int, page int) bool {
	page = max(page, 1)
	switch k {
	case "up", "k":
		*top--
	case "down", "j":
		*top++
	case "pgup":
		*top -= page
	case "pgdown", "space":
		*top += page
	case "g", "home":
		*top = 0
	case "G", "end":
		*top = math.MaxInt32
	default:
		return false
	}
	*top = max(*top, 0)
	return true
}

func newInput(placeholder string) textinput.Model {
	in := textinput.New()
	in.Placeholder = placeholder
	in.Prompt = "> "
	in.Focus()
	return in
}

// field draws a text input as wide as the text of a dialog, textWidth. Without
// a width the input shows only the first letter of its placeholder.
func field(in *textinput.Model, textWidth int) string {
	in.SetWidth(textWidth - lipgloss.Width(in.Prompt) - 1)
	return in.View()
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

func (d *confirmDialog) view(width, _ int) string {
	var b strings.Builder
	b.WriteString(wrap(d.text, boxText(width)) + "\n\n" + field(&d.in, boxText(width)) + "\n")
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

func (d *promptDialog) view(width, _ int) string {
	var b strings.Builder
	b.WriteString(d.label + "\n\n" + field(&d.in, boxText(width)) + "\n")
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
	text  string // above the items, if set
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
		d.at = max(0, min(len(d.items)-1, d.at+1))
	case "enter":
		if len(d.items) == 0 {
			return nil, true
		}
		return d.items[d.at].do(m), true
	}
	return nil, false
}

func (d *menuDialog) view(width, _ int) string {
	var b strings.Builder
	if d.text != "" {
		b.WriteString(wrap(d.text, boxText(width)) + "\n\n")
	}
	for i, it := range d.items {
		if i == d.at {
			b.WriteString(styleSel.Render("> "+it.label) + "\n")
		} else {
			b.WriteString("  " + it.label + "\n")
		}
	}
	return box(d.title, b.String(), "enter choose · esc close", width)
}

// newScheduleDialog says how the scheduled backups run and offers what fits:
// to disable or pause them when they run, to enable them otherwise (and to
// change a pause), nothing when there is no backup schedule.
func newScheduleDialog(st *service.Status, now time.Time) *menuDialog {
	set := func(req service.ScheduleRequest) func(*Model) tea.Cmd {
		return func(m *Model) tea.Cmd {
			return act("schedule", func() error { _, err := m.svc.SetSchedule(m.ctx, req); return err })
		}
	}
	window := func(d time.Duration) menuItem {
		return menuItem{"Pause for " + present.Duration(d) + " (maintenance)", set(service.ScheduleRequest{Action: service.ScheduleMaintenance, For: service.Duration(d)})}
	}
	d := &menuDialog{title: "Scheduled backups"}
	enabled, paused := true, false
	if st != nil {
		for _, r := range present.StatusRows(*st, now) {
			if r.Label == "Schedule" {
				d.text = "Now: " + r.Text
			}
		}
		if st.Schedule.Backup == "" {
			return d
		}
		enabled, paused = st.Schedule.Enabled, st.Schedule.MaintenanceUntil.After(now)
	}
	if !enabled || paused {
		d.items = append(d.items, menuItem{"Enable", set(service.ScheduleRequest{Action: service.ScheduleEnable})})
	}
	if !enabled && !paused {
		return d
	}
	d.items = append(d.items,
		menuItem{"Disable until enabled again or restarted", set(service.ScheduleRequest{Action: service.ScheduleDisable})},
		window(time.Hour), window(4*time.Hour), window(24*time.Hour),
		menuItem{"Pause for another time …", func(m *Model) tea.Cmd {
			p := &promptDialog{title: "Maintenance window", label: "How long? (for example 90m or 2h)", in: newInput("2h")}
			p.submit = func(v string, _ bool) (tea.Cmd, string) {
				dur, err := time.ParseDuration(v)
				if err != nil || dur <= 0 {
					return nil, "Not a duration. Use something like 90m or 2h."
				}
				return set(service.ScheduleRequest{Action: service.ScheduleMaintenance, For: service.Duration(dur)})(m), ""
			}
			m.dlg = p
			return nil
		}},
	)
	return d
}

// ---- scrollable text (the log) ----

type textDialog struct {
	title  string
	lines  []string
	top    int
	page   int  // lines in view at the last drawing
	follow bool // new lines are appended and the end stays in view
	log    bool // the log of a run: a key opens the log of another kind
}

// logKeys open the log of another kind of run from the log dialog.
var logKeys = map[string]service.Kind{"b": service.KindBackup, "p": service.KindPrune, "c": service.KindCheck, "v": service.KindVerify, "r": service.KindRestore}

func (d *textDialog) key(m *Model, k tea.KeyPressMsg) (tea.Cmd, bool) {
	page := max(3, d.page)
	if kind, ok := logKeys[k.String()]; ok && d.log {
		return m.wait("reading the log", fetchLog(m.ctx, m.svc, kind)), false
	}
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

func (d *textDialog) view(width, height int) string {
	// a long line wraps, so that nothing of it is lost (an error of restic,
	// for example); the scrolling goes by the lines on the screen
	shown := make([]string, 0, len(d.lines))
	for _, l := range d.lines {
		shown = append(shown, wrapAtSpaces(l, listText(width))...)
	}
	if d.follow {
		d.top = len(shown) // scroll moves it back to the last page
	}
	lines, pos := scroll(shown, height-boxLines, &d.top)
	d.page = len(lines)
	body := "(the log is empty)"
	if len(shown) > 0 {
		body = strings.Join(lines, "\n")
	}
	hint := "↑↓ pgup pgdn scroll · G end · esc close"
	if d.log {
		hint = "b p c v r other logs · ↑↓ scroll · G end · esc close"
	}
	if pos != "" {
		hint = pos + " · " + hint
	}
	return listBox(d.title, body, hint, width)
}

// ---- the keys ----

// helpLabel is the width of the labels in helpText.
const helpLabel = 12

// helpLines returns helpText with the lines that are longer than width
// wrapped under the column of the keys.
func helpLines(width int) []string {
	var out []string
	indent := strings.Repeat(" ", helpLabel)
	for line := range strings.SplitSeq(helpText, "\n") {
		if len([]rune(line)) <= width || len(line) <= helpLabel {
			out = append(out, line)
			continue
		}
		// Wordwrap keeps the two spaces after a key, and breaks at spaces only
		for i, l := range strings.Split(ansi.Wordwrap(line[helpLabel:], width-helpLabel, ""), "\n") {
			if i == 0 {
				out = append(out, line[:helpLabel]+l)
			} else {
				out = append(out, indent+l)
			}
		}
	}
	return out
}

// helpDialog lists the keys. Any key closes it; only when the terminal is too
// low for the whole list do the keys that scroll scroll it.
type helpDialog struct {
	top, page int
	scrolls   bool
}

func (d *helpDialog) key(_ *Model, k tea.KeyPressMsg) (tea.Cmd, bool) {
	if d.scrolls && scrollKey(k.String(), &d.top, d.page) {
		return nil, false
	}
	return nil, true
}

func (d *helpDialog) view(width, height int) string {
	lines, pos := scroll(helpLines(boxText(width)), height-boxLines, &d.top)
	d.page, d.scrolls = len(lines), pos != ""
	hint := "any key closes this help"
	if d.scrolls {
		hint = pos + " · ↑↓ scroll · any other key closes"
	}
	return box("Keys", strings.Join(lines, "\n"), hint, width)
}

// ---- screens for selecting and pasting ----

// plainDialog is a dialog that fills the whole screen with plain text, with
// no frame, colors or padding in the way of selecting the text in a terminal
// (the console of a browser, for example, which has no clipboard access).
type plainDialog interface {
	dialog
	plain()
}

// pasteDialog shows text that is meant to be copied by selecting it.
type pasteDialog struct {
	title, intro, text string
}

func (d *pasteDialog) plain() {}

func (d *pasteDialog) key(_ *Model, k tea.KeyPressMsg) (tea.Cmd, bool) {
	switch k.String() {
	case "esc", "q", "enter", "ctrl+c":
		return nil, true
	}
	return nil, false
}

func (d *pasteDialog) view(width, _ int) string {
	var b strings.Builder
	b.WriteString(styleTitle.Render(d.title) + "\n" + styleDim.Render(wrap(d.intro, width)) + "\n\n")
	b.WriteString(strings.Join(shellLines(d.text, width), "\n") + "\n\n")
	b.WriteString(styleDim.Render("esc closes"))
	return b.String()
}

// ---- web UI login link ----

type linkDialog struct {
	url, hint string
	// canOpen tells if this machine has a browser to open the link in: not
	// in a container or an SSH session
	canOpen bool
}

func (d *linkDialog) plain() {}

func (d *linkDialog) key(m *Model, k tea.KeyPressMsg) (tea.Cmd, bool) {
	switch k.String() {
	case "esc", "q", "enter", "ctrl+c":
		return nil, true
	case "o":
		if !d.canOpen || m.o.Open == nil {
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

// view puts the link on a line of its own, so that a triple click selects
// exactly the link.
func (d *linkDialog) view(_, _ int) string {
	text := styleTitle.Render("Web UI") + "\n" + styleDim.Render("Open this link in a browser. It works once and for two minutes:") + "\n\n" + d.url + "\n"
	if d.hint != "" {
		text += "\n" + styleDim.Render("From another machine:") + "\n" + d.hint + "\n"
	}
	keys := "d switch the web UI off · esc close"
	if d.canOpen {
		keys = "o open here · " + keys
	}
	return text + "\n" + styleDim.Render(keys)
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
	busy    string // what is being worked out, while it is
	// starting is set while the restore is asked to start
	starting bool
	// top and page scroll the preview: its lists of paths can be longer
	// than the screen
	top, page int
	// picked is a path chosen in the file browser: it is one path, also if
	// it contains spaces, as long as the field still holds it.
	picked string
}

// newRestoreDialogFor opens the restore of one path of a snapshot.
func newRestoreDialogFor(snapshot, include string) *restoreDialog {
	d := newRestoreDialog(snapshot)
	d.picked = include
	d.fields[2].SetValue(include)
	return d
}

// includes returns the paths to restore.
func (d *restoreDialog) includes() []string {
	if v := d.fields[2].Value(); d.picked != "" && v == d.picked {
		return []string{v}
	}
	return strings.Fields(d.fields[2].Value())
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

// focus marks the field the keys go to: only it has the prompt and a
// bright label, the others are dimmed.
func (d *restoreDialog) focus() {
	for i := range d.fields {
		if i == d.at {
			d.fields[i].Focus()
			d.fields[i].Prompt = "> "
		} else {
			d.fields[i].Blur()
			d.fields[i].Prompt = "  "
		}
	}
}

// label draws the label of field i, bright if the field has the focus.
func (d *restoreDialog) label(i int, text string) string {
	if i == d.at {
		return styleAccent.Bold(true).Render(text)
	}
	return styleDim.Render(text)
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
		req := service.RestoreRequest{Snapshot: snap, Target: strings.TrimSpace(d.fields[1].Value()), Includes: d.includes(), Mode: service.ModeExact}
		if d.merge {
			req.Mode = service.ModeMerge
		}
		d.err, d.busy = "", "Checking the restore …"
		return fetchPreflight(m.ctx, m.svc, req), false
	default:
		if d.at < 3 {
			return typing(&d.fields[d.at], k), false
		}
	}
	return nil, false
}

func (d *restoreDialog) previewKey(m *Model, k tea.KeyPressMsg) (tea.Cmd, bool) {
	if scrollKey(k.String(), &d.top, d.page) {
		return nil, false
	}
	switch k.String() {
	case "esc":
		d.stage, d.err = stageForm, ""
	case "f":
		if d.pre.SpaceWarning {
			d.ignore = !d.ignore
		}
	case "p":
		if d.pre.Changes == nil && !d.pre.PipeMode {
			req := d.req
			req.Preview = true
			d.err, d.busy = "", "Working out what would change: restic reads the files in the target …"
			return fetchPreflight(m.ctx, m.svc, req), false
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
			return d.start(m, req), false
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
		return d.start(m, req), false
	default:
		return typing(&d.confirm, k), false
	}
	return nil, false
}

// start asks for the restore. The dialog stays until it is known whether it
// started (see restoreStarted): if not, what was entered is still there.
func (d *restoreDialog) start(m *Model, req service.RestoreRequest) tea.Cmd {
	if d.starting {
		return nil
	}
	d.starting, d.err = true, ""
	return m.startRestore(req)
}

func (d *restoreDialog) view(width, height int) string {
	var b strings.Builder
	hint := ""
	problem := ""
	switch {
	case d.busy != "":
		problem = "\n" + styleDim.Render(wrap(d.busy, boxText(width))) + "\n"
	case d.err != "":
		problem = "\n" + styleBad.Render(wrap(d.err, boxText(width))) + "\n"
	}
	switch d.stage {
	case stageForm:
		for i := range d.fields {
			b.WriteString(d.label(i, d.labels[i]) + "\n" + field(&d.fields[i], boxText(width)) + "\n\n")
		}
		mode := "exact (files that are not in the snapshot are removed)"
		if d.merge {
			mode = "merge (files that are not in the snapshot stay)"
		}
		cursor := "  "
		if d.at == 3 {
			cursor = "> "
		}
		b.WriteString(d.label(3, "Mode when replacing the live data") + "\n" + cursor + mode + "\n")
		hint = "tab next field · ←→ change mode · enter check · esc cancel"
	case stagePreview:
		lines := strings.Split(rows(present.PreflightRows(*d.pre, d.now), boxText(width)), "\n")
		if d.pre.SpaceWarning {
			mark := "[ ]"
			if d.ignore {
				mark = "[x]"
			}
			lines = append(lines, "", mark+" restore although the space looks too small (f)")
		}
		lines, pos := scroll(lines, height-boxLines-strings.Count(problem, "\n"), &d.top)
		d.page = len(lines)
		b.WriteString(strings.Join(lines, "\n") + "\n")
		hint = "enter continue · esc back"
		if d.pre.Changes == nil && !d.pre.PipeMode {
			hint = "enter continue · p what would change · esc back"
		}
		if pos != "" {
			hint = pos + " · ↑↓ scroll · " + hint
		}
	case stageConfirm:
		b.WriteString(wrap("This replaces the current data. Type "+d.req.Snapshot+" to confirm.", boxText(width)) + "\n\n" + field(&d.confirm, boxText(width)) + "\n")
		hint = "enter restore · esc back"
	}
	b.WriteString(problem)
	return box("Restore", b.String(), hint, width)
}

// rows renders labeled rows as aligned text. Values that are longer than
// width wrap under the value column.
func rows(rs []present.Row, width int) string {
	w := labelWidth(rs)
	indent := strings.Repeat(" ", w+2)
	var b strings.Builder
	for _, r := range rs {
		style := lipgloss.NewStyle()
		if r.Severity == service.SeverityWarning || r.Severity == service.SeverityError {
			style = styleBad
		}
		lines := strings.Split(wrap(r.Text, width-w-2), "\n")
		b.WriteString(styleLabel.Render(fmt.Sprintf("%-*s", w, r.Label)) + "  " + style.Render(lines[0]) + "\n")
		for _, l := range lines[1:] {
			b.WriteString(indent + style.Render(l) + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// labelWidth is the width of the widest label.
func labelWidth(rs []present.Row) int {
	w := 0
	for _, r := range rs {
		w = max(w, len(r.Label))
	}
	return w
}
