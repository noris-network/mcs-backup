package tui

import (
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
	var body string
	switch {
	case m.quitting:
		body = ""
	case m.help:
		body = box("Keys", helpText, "any key closes this help", m.width)
	case m.dlg != nil:
		body = m.dlg.view(m.width)
	default:
		body = m.page()
	}
	// The header sits at the top, the operation panel and the legend at the
	// bottom, so that they do not move when the tab or the content changes.
	top := strings.Split(m.header()+"\n", "\n")
	var bottom []string
	if m.dlg == nil && !m.help {
		if panel := m.operationPanel(); panel != "" {
			bottom = append(strings.Split(panel, "\n"), "")
		}
	}
	foot := strings.Split(m.footer(), "\n")
	for len(foot) < m.footerHeight() { // the last line of the legend is the last line of the screen
		foot = append([]string{""}, foot...)
	}
	bottom = append(bottom, "")
	bottom = append(bottom, foot...)
	avail := max(m.height-len(top)-len(bottom), 3)
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
	case !m.status.Repository.Up:
		return "repository down", colBad
	}
	worst := service.SeverityInfo
	for _, w := range m.status.Warnings {
		if w.Severity == service.SeverityError || w.Severity == service.SeverityWarning && worst != service.SeverityError {
			worst = w.Severity
		}
	}
	switch {
	case worst == service.SeverityError:
		return "needs attention", colBad
	case worst == service.SeverityWarning:
		return "warnings", colWarn
	case m.op != nil:
		return "working", colAccent
	}
	return "healthy", colGood
}

func (m *Model) header() string {
	var tabs []string
	for i, n := range tabNames {
		label := strconv.Itoa(i+1) + " " + n
		if tab(i) == m.tab {
			tabs = append(tabs, styleTabOn.Render(label))
		} else {
			tabs = append(tabs, styleTab.Render(label))
		}
	}
	left := styleBadge.Render("mcs-backup") + " " + strings.Join(tabs, "")
	text, c := m.health()
	right := dot(c) + " " + lipgloss.NewStyle().Foreground(c).Render(text)
	if m.status != nil {
		right = styleDim.Render(m.status.Host) + "  " + right
	}
	line := left
	if gap := m.width - lipgloss.Width(left) - lipgloss.Width(right) - 1; gap >= 1 {
		line = left + strings.Repeat(" ", gap) + right
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
	if m.flash != "" {
		return styleTitle.Render(m.flash)
	}
	return m.legend(m.tab, m.op != nil)
}

// footerHeight is the number of lines the legend needs on the tab that needs
// the most, so that it takes the same room on every tab.
func (m *Model) footerHeight() int {
	h := 1
	for t := range numTabs {
		h = max(h, strings.Count(m.legend(t, true), "\n")+1)
	}
	return h
}

// legend lists the keys that work on a tab.
func (m *Model) legend(t tab, cancel bool) string {
	common := []hint{{"b", "backup"}, {"v", "restore test"}, {"s", "schedule"}, {"l", "log"}, {"w", "web UI"}, {"r", "refresh"}, {"?", "help"}, {"q", "quit"}}
	var keys []hint
	switch t {
	case tabSnapshots:
		keys = []hint{{"enter", "restore"}, {"t", "restore latest"}, {"K", "keep/unkeep"}, {"a", "acknowledge"}, {"f", "forget preview"}}
	case tabLocks:
		keys = []hint{{"u", "remove stale"}, {"U", "remove all"}}
	case tabAccess:
		keys = []hint{{"p", "show/hide secrets"}, {"y", "copy"}}
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

func (m *Model) page() string {
	if m.status == nil && m.connErr == "" {
		return styleDim.Render("loading …")
	}
	switch m.tab {
	case tabSnapshots:
		return m.snapshotsPage()
	case tabLocks:
		return m.locksPage()
	case tabAccess:
		return m.accessPage()
	}
	return m.overviewPage()
}

// ---- overview ----

// cardsFit tells if the terminal is wide enough for the cards.
func (m *Model) cardsFit() bool { return m.width >= 90 }

// card is a small framed headline figure.
func card(title, value, sub string, width int) string {
	return framed(colLine, width, styleCardT.Render(strings.ToUpper(title))+"\n"+value+"\n"+styleDim.Render(sub))
}

func (m *Model) cards(st service.Status) string {
	now := m.o.Now()
	repoCard := func(title string, r service.RepoStatus, w int) string {
		if !r.Up {
			return card(title, dot(colBad)+" "+styleBad.Render("unreachable"), truncate(r.Error, w-4), w)
		}
		sub := "id " + present.Short(r.ID)
		if r.Snapshots >= 0 {
			sub = fmt.Sprintf("%d snapshots", r.Snapshots)
			if r.SizeBytes >= 0 {
				sub += " · " + present.Bytes(r.SizeBytes)
			}
		}
		return card(title, dot(colGood)+" "+styleGood.Bold(true).Render("reachable"), sub, w)
	}
	last := func(w int) string {
		if st.Repository.LastSuccess.IsZero() {
			return card("Last backup", styleWarn.Bold(true).Render("never"), "no backup yet", w)
		}
		return card("Last backup", styleTitle.Render(present.Ago(now, st.Repository.LastSuccess)), present.Stamp(st.Repository.LastSuccess), w)
	}
	next := func(w int) string {
		sc := st.Schedule
		switch n, ok := sc.Next[service.KindBackup]; {
		case sc.MaintenanceUntil.After(now):
			return card("Next backup", styleWarn.Bold(true).Render("paused"), "until "+present.Stamp(sc.MaintenanceUntil), w)
		case !sc.Enabled:
			return card("Next backup", styleWarn.Bold(true).Render("disabled"), "enable with s", w)
		case !ok:
			return card("Next backup", styleTitle.Render("manual"), "no schedule", w)
		default:
			return card("Next backup", styleTitle.Render("in "+present.Duration(n.Sub(now))), present.Stamp(n), w)
		}
	}
	n := 3
	if st.Copy != nil {
		n = 4
	}
	cw := (m.width - (n - 1)) / n
	parts := []string{repoCard("Repository", st.Repository, cw), last(cw), next(cw)}
	if st.Copy != nil {
		parts = append(parts, repoCard("Secondary copy", *st.Copy, cw))
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

func (m *Model) overviewPage() string {
	if m.status == nil {
		return ""
	}
	st := *m.status
	cards := m.cardsFit()
	// notes and the last runs are shown separately, with colors
	var plain, notes []present.Row
	inRuns := false
	for _, r := range present.StatusRows(st, m.o.Now()) {
		switch {
		case r.Label == "Last runs":
			inRuns = true
		case inRuns && r.Label == "" && r.Severity == "":
		case r.Severity != "" || r.Label == "Notes":
			notes = append(notes, r)
		case cards && (r.Label == "Repository" || r.Label == "Last backup" || r.Label == "Secondary" || r.Label == "  next backup"):
		default:
			inRuns = false
			plain = append(plain, r)
		}
	}
	var b strings.Builder
	if cards {
		b.WriteString(m.cards(st) + "\n\n")
	}
	b.WriteString(rows(plain, m.width))
	b.WriteString("\n" + webLine(st.WebUI, labelWidth(plain)))
	if !st.VerifyConfigured {
		b.WriteString("\n" + styleDim.Render("Restore test is not set up (see VERIFY_MODE and VERIFY_SCRIPT)."))
	}
	if runs := m.lastRuns(st); runs != "" {
		b.WriteString("\n\n" + styleTitle.Render("Last runs") + "\n" + runs)
	}
	if len(notes) > 0 {
		b.WriteString("\n\n" + m.attention(notes))
	}
	if st.ForgetHeld && !st.AppendOnly {
		b.WriteString("\n" + styleWarn.Render("F  approve the held forget (review it in Snapshots, f)"))
	}
	return strings.TrimRight(b.String(), "\n")
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
	return framed(c, m.width, styleTitle.Render("Needs attention")+"\n"+strings.Join(lines, "\n"))
}

// lastRuns lists the latest result of every kind of operation.
func (m *Model) lastRuns(st service.Status) string {
	kinds := make([]service.Kind, 0, len(st.LastRuns))
	for k := range st.LastRuns {
		kinds = append(kinds, k)
	}
	slices.Sort(kinds)
	var b strings.Builder
	for _, k := range kinds {
		r := st.LastRuns[k]
		c := colBad
		switch r.Result {
		case service.ResultSuccess:
			c = colGood
		case service.ResultWarning:
			c = colWarn
		case service.ResultSkipped:
			c = colMuted
		}
		line := fmt.Sprintf("%s %-8s %-10s %s %s", dot(c), k, r.Result, present.Stamp(r.Started), styleDim.Render("("+present.Duration(r.Duration.Std())+")"))
		if r.Snapshot != "" {
			line += styleDim.Render("  snapshot ") + present.Short(r.Snapshot)
		}
		b.WriteString(line + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
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

func (m *Model) snapshotsPage() string {
	if m.showForget {
		return m.forgetPage()
	}
	if len(m.snapshots) == 0 {
		return "There are no snapshots yet. " + styleKey.Render("b") + " starts a backup."
	}
	var b strings.Builder
	b.WriteString(styleDim.Render(fmt.Sprintf("  %-9s %-19s %*s  %-11s %7s %10s  %-*s %s", "ID", "TIME", ageWidth, "AGE", "ORIGIN", "FILES", "SIZE", flagWidth, "FLAGS", "NOTE")) + "\n")
	now := m.o.Now()
	first, last := window(m.cursor[tabSnapshots], len(m.snapshots), m.height-14)
	for i := first; i < last; i++ {
		s := m.snapshots[i]
		files, size := "-", "-"
		if s.HasSummary {
			files, size = strconv.FormatInt(s.Files, 10), present.Bytes(s.Bytes)
		}
		cols := fmt.Sprintf("%-9s %-19s %*s  %-11s %7s %10s  ", s.ShortID, present.Stamp(s.Time), ageWidth, present.Duration(now.Sub(s.Time)), s.Origin, files, size)
		flags := present.SnapshotFlags(s)
		var line string
		if i == m.cursor[tabSnapshots] {
			line = styleSel.Render(fill("› "+cols+fmt.Sprintf("%-*s", flagWidth, strings.Join(flags, " "))+" "+s.Note, m.width))
		} else {
			tags := make([]string, len(flags))
			for k, f := range flags {
				tags[k] = flagStyle(f).Render(f)
			}
			info := strings.Join(tags, " ")
			info += strings.Repeat(" ", max(0, flagWidth-lipgloss.Width(info)))
			if s.Suspicious {
				cols = strings.Replace(cols, s.ShortID, styleBad.Render(s.ShortID), 1)
			}
			line = "  " + cols + info + " " + s.Note
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

// flagWidth fits "pre-restore" and "keep" plus a bit of room.
const flagWidth = 12

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
		fmt.Fprintf(&b, "Policy: keep %s, and every snapshot marked keep.\n", r.Policy)
	}
	fmt.Fprintf(&b, "Applies to host %s: %d snapshots", strings.Join(r.Hosts, ", "), r.Total)
	if r.Foreign > 0 {
		fmt.Fprintf(&b, " (%d snapshots of other hosts are never touched)", r.Foreign)
	}
	b.WriteString("\n\n")
	if len(r.Remove) == 0 {
		b.WriteString("The next forget would remove nothing.")
		return b.String()
	}
	fmt.Fprintf(&b, "The next forget would remove %d snapshots:\n", len(r.Remove))
	for _, s := range r.Remove {
		fmt.Fprintf(&b, "  %s  %s%s\n", s.ShortID, present.Stamp(s.Time), present.NoteSuffix(s))
	}
	if r.Held {
		b.WriteString("\n" + styleWarn.Render("That is more than the retention guard allows: forget is held back until it is approved (F on the overview)."))
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m *Model) locksPage() string {
	if len(m.locks) == 0 {
		return styleGood.Render("✓") + " The repository has no locks."
	}
	var b strings.Builder
	b.WriteString(styleDim.Render(fmt.Sprintf("  %-9s %-8s %-10s %-24s %-8s", "ID", "AGE", "EXCLUSIVE", "HOST", "PID")) + "\n")
	for i, l := range m.locks {
		cols := fmt.Sprintf("%-9s %-8s %-10v %-24s %-8d", present.Short(l.ID), present.Duration(l.Age.Std()), l.Exclusive, l.Hostname, l.PID)
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
		b.WriteString(styleAccent.Render(spinFrames[m.frame%len(spinFrames)]) + " " + styleTitle.Render("Running: ") + present.OperationLine(now, *m.op) + "\n")
		if p := m.op.Progress; p != nil && p.Percent > 0 {
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
		for i, l := range present.ResultLines(*m.lastResult) {
			if i == 0 {
				switch {
				case m.lastResult.Result == service.ResultSuccess:
					l, c = styleGood.Bold(true).Render(l), colGood
				case m.lastResult.Result.OK():
					l, c = styleWarn.Bold(true).Render(l), colWarn
				default:
					l = styleBad.Render(l)
				}
			}
			b.WriteString(truncate(l, m.width-6) + "\n")
		}
		if r := m.lastResult; r.Kind == service.KindRestore && !r.Result.OK() && m.lastRestore != nil && !m.lastRestore.NoSafetySnapshot && m.lastRestore.Target == "" {
			b.WriteString(styleWarn.Render("x  repeat the restore without the safety snapshot") + "\n")
		}
		return framed(c, m.width, strings.TrimRight(b.String(), "\n"))
	case m.status != nil && m.status.Retry != nil:
		r := m.status.Retry
		return fmt.Sprintf("The failed backup is retried (%d/%d) at %s. c cancels the retry.", r.Attempt, r.Max, r.At.Local().Format("15:04"))
	}
	return ""
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
	size = max(size, 5)
	if n <= size {
		return 0, n
	}
	first = max(0, min(cursor-size/2, n-size))
	return first, first + size
}

const helpText = `Tabs        tab / ← →  or 1 2 3 4
Move        ↑ ↓  or  j k
Backup      b  start a backup (with a note)
Restore     in Snapshots: enter restores the selected snapshot, t the latest
Cancel      c  stop the running operation or the pending retry
Verify      v  restore a sample of the latest snapshot and check it
Keep        in Snapshots: K  keep / unkeep the selected snapshot
Acknowledge in Snapshots: a  accept a much smaller snapshot
Forget      in Snapshots: f  what the retention would delete
Locks       in Locks: u  remove stale locks, U  remove all
Schedule    s  enable, disable or pause scheduled backups
Log         l  log of the last run
Web UI      w  switch it on and show a one-time login link
Access      in Access: p  show or hide the secrets, y  copy them as shell exports
Other       n  test notification · r  refresh · q  quit`
