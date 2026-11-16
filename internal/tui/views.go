package tui

import (
	"cmp"
	"fmt"
	"image/color"
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/noris-network/mcs-backup/v2/internal/present"
	"github.com/noris-network/mcs-backup/v2/internal/service"
)

// View implements tea.Model.
func (m *Model) View() tea.View {
	if d, ok := m.dlg.(plainDialog); ok && !m.quitting {
		text := d.view(m.width, m.height)
		if m.flash != "" { // there is no legend to show it in
			text += "\n\n" + styleTitle.Render(m.flash)
		}
		lines := strings.Split(text, "\n")
		v := tea.NewView(strings.Join(lines[:min(len(lines), max(m.height, 1))], "\n"))
		v.AltScreen = true
		v.WindowTitle = "mcs-backup"
		return v
	}
	// The header sits at the top, the operation panel and the legend at the
	// bottom, so that they do not move when the tab or the content changes.
	top := strings.Split(m.header()+"\n", "\n")
	bottom := []string{""}
	if m.dlg != nil {
		// a dialog names its own keys: the legend would list keys that do not
		// work in it, and the dialog gets the room. A message keeps its line.
		bottom = append(bottom, styleTitle.Render(m.flash))
	} else {
		if panel := m.operationPanel(); panel != "" && !m.quitting {
			bottom = append(bottom, strings.Split(panel, "\n")...)
			bottom = append(bottom, "")
		}
		// a message takes the blank line above the legend, which stays
		bottom[len(bottom)-1] = styleTitle.Render(m.flash)
		foot := strings.Split(m.footer(), "\n")
		for len(foot) < m.footerHeight() { // the last line of the legend is the last line of the screen
			foot = append([]string{""}, foot...)
		}
		bottom = append(bottom, foot...)
	}
	avail := max(m.height-len(top)-len(bottom), 3)

	var body string
	switch {
	case m.quitting:
		body = ""
	case m.dlg != nil:
		body = m.dlg.view(m.width, avail)
	default:
		body = m.page(avail)
	}
	mid := strings.Split(body, "\n")
	mid = mid[:min(len(mid), avail)]
	for len(mid) < avail {
		mid = append(mid, "")
	}
	lines := slices.Concat(top, mid, bottom)
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, m.width, "…")
	}
	v := tea.NewView(strings.Join(lines, "\n"))
	v.AltScreen = true
	v.WindowTitle = "mcs-backup"
	return v
}

// health sums the state up for the pill in the header.
func (m *Model) health() (string, color.Color) {
	switch {
	case m.connErr != "":
		return "unreachable", colBad
	case m.status == nil:
		return "loading", colMuted
	}
	text, level := present.Health(*m.status)
	if level == present.LevelNone && m.op != nil {
		return text, colAccent
	}
	return text, levelColor(level)
}

func (m *Model) header() string {
	tabs := func(short bool) string {
		var out []string
		for i, n := range tabNames {
			label := strconv.Itoa(i+1) + " " + n
			switch {
			case tab(i) == m.tab:
				out = append(out, styleTabOn.Render(label))
			case short:
				out = append(out, styleTab.Render(strconv.Itoa(i+1)))
			default:
				out = append(out, styleTab.Render(label))
			}
		}
		return strings.Join(out, "")
	}
	badge := styleBadge.Render("mcs-backup") + " "
	text, c := m.health()
	health := dot(c) + " " + lipgloss.NewStyle().Foreground(c).Render(text)
	withHost := health
	if m.status != nil {
		withHost = styleDim.Render(m.status.Host) + "  " + health
	}
	// on a narrow terminal the host goes first, then the badge, then the
	// names of the other tabs: the health stays in view
	line := tabs(true)
	for _, v := range [][2]string{{badge + tabs(false), withHost}, {badge + tabs(false), health}, {tabs(false), health}, {tabs(true), health}} {
		if gap := m.width - lipgloss.Width(v[0]) - lipgloss.Width(v[1]) - 1; gap >= 1 {
			line = v[0] + strings.Repeat(" ", gap) + v[1]
			break
		}
	}
	h := line + "\n" + rule(m.width)
	if m.connErr != "" {
		h += "\n" + styleBad.Render("service not reachable: "+m.connErr)
	}
	return h
}

// hint is one entry of the key legend.
type hint struct{ key, text string }

func (m *Model) footer() string {
	return m.legend(m.tab, m.op != nil || m.retrying(), m.showForget)
}

// footerHeight is the number of lines the legend needs on the tab that needs
// the most, so that it takes the same room on every tab.
func (m *Model) footerHeight() int {
	h := 1
	for t := range numTabs {
		h = max(h, strings.Count(m.legend(t, true, false), "\n")+1)
	}
	return h
}

// legend lists the keys that work on a tab; forget is set while Snapshots
// shows the forget preview instead of the list.
func (m *Model) legend(t tab, cancel, forget bool) string {
	common := []hint{{"b", "backup"}, {"v", "restore test"}, {"s", "schedule"}, {"l", "log"}, {"w", "web UI"}, {"r", "refresh"}, {"?", "help"}, {"q", "quit"}}
	var keys []hint
	switch {
	case t == tabSnapshots && forget:
		keys = []hint{{"f", "back to the list"}}
	case t == tabSnapshots:
		keys = []hint{{"enter", "restore"}, {"t", "restore latest"}, {"o", "files"}, {"d", "what changed"}, {"K", "keep/unkeep"}, {"a", "acknowledge"}, {"f", "forget preview"}}
	case t == tabLocks:
		keys = []hint{{"u", "remove stale"}, {"U", "remove all"}}
	case t == tabAccess:
		keys = []hint{{"p", "show/hide secrets"}, {"y", "paste view"}}
	}
	if cancel {
		keys = append([]hint{{"c", "cancel"}}, keys...)
	}
	keys = append(keys, common...)
	var lines []string
	cur := make([]string, 0, len(keys))
	n := 0
	for _, h := range keys {
		item := styleKey.Render(h.key) + " " + styleDim.Render(h.text)
		w := lipgloss.Width(item) + 3
		if n+w > m.width && len(cur) > 0 {
			lines, cur, n = append(lines, strings.Join(cur, styleDim.Render(" · "))), nil, 0
		}
		cur, n = append(cur, item), n+w
	}
	return strings.Join(append(lines, strings.Join(cur, styleDim.Render(" · "))), "\n")
}

func (m *Model) page(avail int) string {
	if m.status == nil && m.connErr == "" {
		return styleDim.Render("loading …")
	}
	switch m.tab {
	case tabSnapshots:
		return m.snapshotsPage(avail)
	case tabLocks:
		return m.locksPage()
	case tabAccess:
		return m.accessPage()
	}
	return m.overviewPage(avail)
}

// ---- overview ----

// cardsFit tells if the terminal is wide enough for the cards.
func (m *Model) cardsFit() bool { return m.width >= 90 }

// card is a small framed headline figure.
func card(title, value, sub string, width int) string {
	return framed(colLine, width, styleCardT.Render(strings.ToUpper(title))+"\n"+value+"\n"+styleDim.Render(sub))
}

// levelColor and levelStyle give the color of a level.
func levelColor(l present.Level) color.Color {
	switch l {
	case present.LevelOK:
		return colGood
	case present.LevelWarn:
		return colWarn
	case present.LevelBad:
		return colBad
	}
	return colMuted
}

func (m *Model) cards(st service.Status) string {
	cards := present.Cards(st, m.o.Now())
	cw := (m.width - (len(cards) - 1)) / len(cards)
	parts := make([]string, len(cards))
	for i, c := range cards {
		value, sub := styleTitle.Render(c.Value), c.Sub
		if c.Level != present.LevelNone {
			value = lipgloss.NewStyle().Foreground(levelColor(c.Level)).Bold(true).Render(c.Value)
		}
		if c.Dot {
			value = dot(levelColor(c.Level)) + " " + value
		}
		parts[i] = card(c.Title, value, truncate(sub, cw-4), cw)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, interleave(parts)...)
}

// interleave puts a space between the parts.
func interleave(parts []string) []string {
	out := make([]string, 0, 2*len(parts))
	for i, p := range parts {
		if i > 0 {
			out = append(out, " ")
		}
		out = append(out, p)
	}
	return out
}

func truncate(s string, width int) string { return ansi.Truncate(s, max(width, 4), "…") }

// overviewPage shows as much of the overview as fits into avail lines. What
// is left out is the least important part, and a frame is never cut.
func (m *Model) overviewPage(avail int) string {
	if m.status == nil {
		return ""
	}
	st := *m.status
	cards := m.cardsFit()
	// notes and the last runs are shown separately, with colors, and the
	// operation and a retry in the panel at the bottom
	var plain, notes []present.Row
	inRuns := false
	for _, r := range present.StatusRows(st, m.o.Now()) {
		switch {
		case r.Label == "Last runs":
			inRuns = true
		case inRuns && r.Label == "" && r.Severity == "":
		case r.Severity != "" || r.Label == "Notes":
			notes = append(notes, r)
		case r.Label == "Operation":
		case cards && (r.Label == "Repository" || r.Label == "Last backup" || r.Label == "Secondary" || r.Label == "  next backup"):
		default:
			inRuns = false
			plain = append(plain, r)
		}
	}
	// the rows that can go when the room is short: the next runs of the
	// maintenance jobs
	var compact []present.Row
	for _, r := range plain {
		if !strings.HasPrefix(r.Label, "  ") {
			compact = append(compact, r)
		}
	}
	extras := webLine(st.WebUI, labelWidth(plain))
	if !st.VerifyConfigured {
		extras += "\n" + styleDim.Render("Restore test is not set up (see VERIFY_MODE and VERIFY_SCRIPT).")
	}
	runs := m.lastRuns(st)
	attention := ""
	if len(notes) > 0 {
		attention = m.attention(notes)
	}
	if st.ForgetHeld && !st.AppendOnly {
		attention += "\n" + styleWarn.Render("F  approve the held forget (review it in Snapshots, f)")
	}
	build := func(withExtras bool, nRuns, detail int) string {
		var parts []string
		if cards {
			parts = append(parts, m.cards(st))
		}
		switch detail {
		case 2:
			parts = append(parts, rows(plain, m.width))
		case 1:
			parts = append(parts, rows(compact, m.width))
		}
		if withExtras && detail > 0 {
			parts[len(parts)-1] += "\n" + extras
		}
		if nRuns > 0 && len(runs) > 0 {
			parts = append(parts, styleTitle.Render("Last runs")+"\n"+strings.Join(runs[:min(nRuns, len(runs))], "\n"))
		}
		if attention != "" {
			parts = append(parts, strings.TrimLeft(attention, "\n"))
		}
		return strings.Join(parts, "\n\n")
	}
	// what goes first when the room is short: the web UI line, then the
	// least important of the last runs, one by one, then the next runs of
	// the maintenance jobs and at last all rows
	type variant struct {
		extras       bool
		runs, detail int
	}
	variants := []variant{{true, len(runs), 2}}
	for n := len(runs); n >= 0; n-- {
		variants = append(variants, variant{false, n, 2})
	}
	variants = append(variants, variant{false, 0, 1}, variant{false, 0, 0})
	var out string
	for _, v := range variants {
		out = build(v.extras, v.runs, v.detail)
		if strings.Count(out, "\n")+1 <= avail {
			break
		}
	}
	return out
}

// attention frames the notes, in the color of the worst one.
func (m *Model) attention(notes []present.Row) string {
	c := colMuted
	lines := make([]string, 0, len(notes))
	for _, n := range notes {
		style := styleDim
		switch n.Severity {
		case service.SeverityError:
			style, c = styleBad, colBad
		case service.SeverityWarning:
			style = styleWarn
			if c != colBad {
				c = colWarn
			}
		}
		text := strings.TrimPrefix(n.Text, present.SeverityMark(n.Severity)+" ")
		lines = append(lines, style.Render("• "+wrap(text, m.width-8)))
	}
	title := "Needs attention"
	if c == colMuted {
		title = "Notes" // only for information, like a pause for maintenance
	}
	return framed(c, m.width, styleTitle.Render(title)+"\n"+strings.Join(lines, "\n"))
}

// runOrder ranks the kinds of operations for the last runs: when not all fit,
// the backup stays and quick actions such as tag and unlock go first.
var runOrder = []service.Kind{service.KindBackup, service.KindCopy, service.KindVerify, service.KindPrune, service.KindCheck,
	service.KindRestore, service.KindForget, service.KindTag, service.KindUnlock}

// lastRuns lists the latest result of every kind of operation, one line
// each, the most important first.
func (m *Model) lastRuns(st service.Status) []string {
	kinds := make([]service.Kind, 0, len(st.LastRuns))
	for k := range st.LastRuns {
		kinds = append(kinds, k)
	}
	rank := func(k service.Kind) int {
		if i := slices.Index(runOrder, k); i >= 0 {
			return i
		}
		return len(runOrder)
	}
	slices.SortFunc(kinds, func(a, b service.Kind) int { return cmp.Or(cmp.Compare(rank(a), rank(b)), cmp.Compare(a, b)) })
	out := make([]string, 0, len(kinds))
	for _, k := range kinds {
		r := st.LastRuns[k]
		c := colBad
		switch r.Result {
		case service.ResultSuccess:
			c = colGood
		case service.ResultWarning:
			c = colWarn
		case service.ResultSkipped, service.ResultCancelled:
			c = colMuted
		}
		line := fmt.Sprintf("%s %-8s %-10s %s %s", dot(c), k, r.Result, present.Stamp(r.Started), styleDim.Render("("+present.Duration(r.Duration.Std())+")"))
		if r.Snapshot != "" {
			line += styleDim.Render("  snapshot ") + present.Short(r.Snapshot)
		}
		out = append(out, line)
	}
	return out
}

func webLine(w service.WebUIStatus, width int) string {
	label := styleLabel.Render(fmt.Sprintf("%-*s", width, "Web UI"))
	if !w.Enabled {
		return label + "  " + styleDim.Render("off (w opens it and shows a login link)")
	}
	s := label + "  " + styleGood.Render("on") + " at " + w.Addr
	if !w.Until.IsZero() {
		s += " until " + present.Stamp(w.Until)
	}
	return s
}

// ---- snapshots ----

func (m *Model) snapshotsPage(avail int) string {
	if m.showForget {
		return m.forgetPage()
	}
	if len(m.snapshots) == 0 {
		return "There are no snapshots yet. " + styleKey.Render("b") + " starts a backup."
	}
	// a narrow terminal leaves out the age, so that the flags stay in view
	withAge := m.width >= 90
	columns := func(id, time, age, origin, files, size string) string {
		if !withAge {
			return fmt.Sprintf("%-9s %-19s  %-11s %7s %10s  ", id, time, origin, files, size)
		}
		return fmt.Sprintf("%-9s %-19s %*s  %-11s %7s %10s  ", id, time, ageWidth, age, origin, files, size)
	}
	// the flags take the room they need in the whole list, none if no
	// snapshot has one, so that the notes keep theirs
	fw := 0
	for _, s := range m.snapshots {
		fw = max(fw, len(strings.Join(present.SnapshotFlags(s), " ")))
	}
	if fw > 0 {
		fw = max(fw, len("FLAGS")) + 1
	}
	pad := func(text string) string { return text + strings.Repeat(" ", max(0, fw-lipgloss.Width(text))) }
	header := "  " + columns("ID", "TIME", "AGE", "ORIGIN", "FILES", "SIZE")
	if fw > 0 {
		header += pad("FLAGS")
	}
	var b strings.Builder
	b.WriteString(styleDim.Render(header+"NOTE") + "\n")
	now := m.o.Now()
	first, last := window(m.cursor[tabSnapshots], len(m.snapshots), avail-2) // the column header and the summary take two lines
	m.listRows = last - first
	for i := first; i < last; i++ {
		s := m.snapshots[i]
		files, size := "-", "-"
		if s.HasSummary {
			files, size = strconv.FormatInt(s.Files, 10), present.Bytes(s.Bytes)
		}
		cols := columns(s.ShortID, present.Stamp(s.Time), present.Duration(now.Sub(s.Time)), string(s.Origin), files, size)
		flags := present.SnapshotFlags(s)
		var line string
		if i == m.cursor[tabSnapshots] {
			line = styleSel.Render(fill("› "+cols+pad(strings.Join(flags, " "))+s.Note, m.width))
		} else {
			tags := make([]string, len(flags))
			for k, f := range flags {
				tags[k] = flagStyle(f).Render(f)
			}
			if s.Suspicious {
				cols = strings.Replace(cols, s.ShortID, styleBad.Render(s.ShortID), 1)
			}
			line = "  " + cols + pad(strings.Join(tags, " ")) + s.Note
		}
		b.WriteString(line + "\n")
	}
	more := fmt.Sprintf("%d snapshots", len(m.snapshots))
	if first > 0 {
		more += fmt.Sprintf(" · %d newer above", first)
	}
	if last < len(m.snapshots) {
		more += fmt.Sprintf(" · %d older below", len(m.snapshots)-last)
	}
	return b.String() + styleDim.Render(more)
}

// ageWidth fits "100d 23h".
const ageWidth = 8

func flagStyle(flag string) lipgloss.Style {
	switch flag {
	case "keep":
		return styleGood
	case "SUSPICIOUS":
		return styleBad
	case "pre-restore":
		return styleWarn
	}
	return styleDim
}

func (m *Model) forgetPage() string {
	r := m.retention
	if r == nil {
		return styleDim.Render("loading …")
	}
	var b strings.Builder
	switch {
	case r.AppendOnly:
		return "The repository is append-only: nothing is ever forgotten."
	case r.Policy != "":
		b.WriteString(wrap(fmt.Sprintf("Policy: keep %s, and every snapshot marked keep.", r.Policy), m.width) + "\n")
	}
	applies := fmt.Sprintf("Applies to host %s: %d snapshots", strings.Join(r.Hosts, ", "), r.Total)
	if r.Foreign > 0 {
		applies += fmt.Sprintf(" (%d snapshots of other hosts are never touched)", r.Foreign)
	}
	b.WriteString(wrap(applies, m.width) + "\n\n")
	if len(r.Remove) == 0 {
		b.WriteString("The next forget would remove nothing.")
		return b.String()
	}
	fmt.Fprintf(&b, "The next forget would remove %d snapshots:\n", len(r.Remove))
	for _, s := range r.Remove {
		fmt.Fprintf(&b, "  %s  %s%s\n", s.ShortID, present.Stamp(s.Time), present.NoteSuffix(s))
	}
	if r.Held {
		b.WriteString("\n" + styleWarn.Render(wrap("That is more than the retention guard allows: forget is held back until it is approved (F on the overview).", m.width)))
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m *Model) locksPage() string {
	if len(m.locks) == 0 {
		return styleGood.Render("✓") + " The repository has no locks."
	}
	// a host is often the name of a pod, which is long
	host := len("HOST")
	for _, l := range m.locks {
		host = max(host, len(l.Hostname))
	}
	var b strings.Builder
	b.WriteString(styleDim.Render(fmt.Sprintf("  %-9s %-8s %-10s %-*s %-8s", "ID", "AGE", "EXCLUSIVE", host, "HOST", "PID")) + "\n")
	for i, l := range m.locks {
		cols := fmt.Sprintf("%-9s %-8s %-10v %-*s %-8d", present.Short(l.ID), present.Duration(l.Age.Std()), l.Exclusive, host, l.Hostname, l.PID)
		note := ""
		if l.Stale {
			note = "stale"
		}
		if i == m.cursor[tabLocks] {
			b.WriteString(styleSel.Render(fill("› "+cols+note, m.width)) + "\n")
			continue
		}
		b.WriteString("  " + cols + styleWarn.Render(note) + "\n")
	}
	b.WriteString("\n" + styleDim.Render("u removes stale locks only. Locks of running processes are renewed and stay."))
	return b.String()
}

func (m *Model) accessPage() string {
	if m.env == nil {
		return styleDim.Render("loading …")
	}
	var b strings.Builder
	b.WriteString("What it takes to run restic against this repository from another place.\n")
	if m.revealed {
		b.WriteString(styleWarn.Render("The secrets are shown. ") + "p hides them again.\n\n")
	} else {
		b.WriteString("The secrets are hidden. p shows them.\n\n")
	}
	vars := make([]present.Row, len(m.env.Vars))
	for i, v := range m.env.Vars {
		vars[i] = present.Row{Label: v.Name, Text: v.Value}
	}
	b.WriteString(rows(vars, m.width) + "\n")
	if len(m.env.Args) > 0 {
		fmt.Fprintf(&b, "\n%s  %s\n", styleLabel.Render("restic options"), strings.Join(m.env.Args, " "))
	}
	fmt.Fprintf(&b, "\nrestic %s or newer is needed.\n", m.env.MinVersion)
	for _, n := range m.env.Notes {
		b.WriteString(styleDim.Render("• "+wrap(n, m.width-8)) + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// spinner frames, one per tick
var spinFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// operationPanel shows the running operation or the result of the last one.
func (m *Model) operationPanel() string {
	now := m.o.Now()
	switch {
	case m.op != nil:
		var b strings.Builder
		b.WriteString(styleAccent.Render(spinFrames[m.frame%len(spinFrames)]) + " " + styleTitle.Render(present.OperationTitle(*m.op)) +
			styleDim.Render(" · "+present.OperationMeta(now, *m.op)) + "\n")
		if p := m.op.Progress; p != nil && p.Percent > 0 { // the bar says the percentage
			b.WriteString(bar(p.Percent, max(10, min(40, m.width-30))))
			if p.Current != "" {
				b.WriteString("  " + styleDim.Render(truncate(p.Current, m.width-60)))
			}
			b.WriteString("\n")
		}
		for _, l := range tail(m.log, 4) {
			b.WriteString(styleDim.Render(truncate(l, m.width-6)) + "\n")
		}
		return framed(colAccent, m.width, strings.TrimRight(b.String(), "\n"))
	case m.lastResult != nil:
		var b strings.Builder
		c := colBad
		// the reason of a failure or a warning is wrapped, not cut: it is
		// what the user has to read. A very long one ends after a few lines,
		// the attention notes and the log have it in full.
		var lines []string
		for _, l := range present.ResultLines(*m.lastResult) {
			lines = append(lines, wrapIndented(l, m.width-6)...)
		}
		if len(lines) > maxResultLines {
			lines = append(lines[:maxResultLines-1], styleDim.Render("…"))
		}
		for i, l := range lines {
			if i == 0 {
				switch {
				case m.lastResult.Result == service.ResultSuccess:
					l, c = styleGood.Bold(true).Render(l), colGood
				case m.lastResult.Result.OK():
					l, c = styleWarn.Bold(true).Render(l), colWarn
				case m.lastResult.Result == service.ResultCancelled: // stopped on purpose
					l, c = styleTitle.Render(l), colMuted
				default:
					l = styleBad.Render(l)
				}
			}
			b.WriteString(l + "\n")
		}
		if r := m.lastResult; r.Kind == service.KindRestore && !r.Result.OK() && m.lastRestore != nil && !m.lastRestore.NoSafetySnapshot && m.lastRestore.Target == "" {
			b.WriteString(styleWarn.Render("x  repeat the restore without the safety snapshot") + "\n")
		}
		// the failed backup is the last result while its retry waits
		if retry := m.retryLine(); retry != "" {
			b.WriteString(styleWarn.Render(retry) + "\n")
		}
		return framed(c, m.width, strings.TrimRight(b.String(), "\n"))
	case m.retrying():
		return m.retryLine()
	}
	return ""
}

// maxResultLines limits the result in the panel, so that the content above
// keeps its room.
const maxResultLines = 6

// wrapIndented wraps a line at width and puts the lines it adds two columns
// further in than the line itself.
func wrapIndented(line string, width int) []string {
	text := strings.TrimLeft(line, " ")
	indent := line[:len(line)-len(text)]
	parts := strings.Split(wrap(text, width-len(indent)-2), "\n")
	for i := range parts {
		if i == 0 {
			parts[i] = indent + parts[i]
		} else {
			parts[i] = indent + "  " + parts[i]
		}
	}
	return parts
}

func (m *Model) retrying() bool { return m.op == nil && m.status != nil && m.status.Retry != nil }

// retryLine says when the failed backup is tried again, if it is.
func (m *Model) retryLine() string {
	if !m.retrying() {
		return ""
	}
	r := m.status.Retry
	return fmt.Sprintf("The failed backup is tried again (%d/%d) at %s. c cancels the retry.", r.Attempt, r.Max, r.At.Local().Format("15:04"))
}

func bar(frac float64, width int) string {
	frac = max(0, min(1, frac))
	n := int(frac * float64(width))
	return styleAccent.Render(strings.Repeat("█", n)) + styleRule.Render(strings.Repeat("░", width-n)) + fmt.Sprintf(" %3.0f%%", frac*100)
}

func tail(lines []string, n int) []string {
	if len(lines) > n {
		return lines[len(lines)-n:]
	}
	return lines
}

// window returns the part of a list of n rows that fits into size lines and
// contains the cursor.
func window(cursor, n, size int) (first, last int) {
	size = max(size, 1)
	if n <= size {
		return 0, n
	}
	first = max(0, min(cursor-size/2, n-size))
	return first, first + size
}

const helpText = `Tabs        tab / ← →  or 1 2 3 4
Move        ↑ ↓  or  j k, pgup pgdn, home end
Backup      b  start a backup (with a note)
Restore     in Snapshots: enter restores the selected snapshot, t the latest
Files       in Snapshots: o  browse the selected snapshot, / finds a file in all
Changes     in Snapshots: d  what the selected snapshot lost, gained and changed
Cancel      c  stop the running operation or the pending retry
Verify      v  restore a sample of the latest snapshot and check it
Keep        in Snapshots: K  keep / unkeep the selected snapshot
Acknowledge in Snapshots: a  accept a much smaller snapshot
Forget      in Snapshots: f  what the retention would delete
Locks       in Locks: u  remove stale locks, U  remove all
Schedule    s  enable, disable or pause scheduled backups
Log         l  log of the last run, in it b p c v r the log of another kind
Web UI      w  switch it on and show a one-time login link
Access      in Access: p  show or hide the secrets, y  export lines to paste
Other       n  test notification · r  refresh · q  quit`
