package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

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
	parts := []string{m.header(), "", body}
	if m.dlg == nil && !m.help {
		if panel := m.operationPanel(); panel != "" {
			parts = append(parts, "", panel)
		}
	}
	parts = append(parts, "", m.footer())
	lines := strings.Split(strings.Join(parts, "\n"), "\n")
	v := tea.NewView(strings.Join(lines[:min(len(lines), max(m.height, 10))], "\n"))
	v.AltScreen = true
	v.WindowTitle = "mcs-backup"
	return v
}

func (m *Model) header() string {
	var tabs []string
	for i, n := range tabNames {
		if tab(i) == m.tab {
			tabs = append(tabs, styleTabOn.Render(n))
		} else {
			tabs = append(tabs, styleTab.Render(n))
		}
	}
	h := styleTitle.Render("mcs-backup") + " " + strings.Join(tabs, "")
	if m.status != nil {
		h += styleDim.Render("  " + m.status.Host)
	}
	if m.connErr != "" {
		h += "  " + styleBad.Render("service not reachable: "+m.connErr)
	}
	return h
}

func (m *Model) footer() string {
	if m.flash != "" {
		return m.flash
	}
	keys := "b backup · v restore test · s schedule · l log · w web UI · r refresh · ? help · q quit"
	switch m.tab {
	case tabSnapshots:
		keys = "enter restore · t restore latest · K keep/unkeep · a acknowledge · f forget preview · " + keys
	case tabLocks:
		keys = "u remove stale · U remove all · " + keys
	case tabAccess:
		keys = "p show/hide secrets · y copy · " + keys
	}
	if m.op != nil {
		keys = "c cancel · " + keys
	}
	return styleDim.Render(keys)
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

func (m *Model) overviewPage() string {
	if m.status == nil {
		return ""
	}
	st := *m.status
	rs := present.StatusRows(st, m.o.Now())
	// notes are shown below, with colors and actions
	var plain, notes []present.Row
	for _, r := range rs {
		if r.Severity != "" || r.Label == "Notes" {
			notes = append(notes, r)
		} else {
			plain = append(plain, r)
		}
	}
	var b strings.Builder
	b.WriteString(rows(plain))
	b.WriteString("\n" + webLine(st.WebUI))
	if st.VerifyConfigured {
		b.WriteString("")
	} else {
		b.WriteString("\n" + styleDim.Render("Restore test is not set up (see VERIFY_MODE and VERIFY_SCRIPT)."))
	}
	if len(notes) > 0 {
		b.WriteString("\n\n" + styleTitle.Render("Needs attention") + "\n")
		for _, n := range notes {
			style := styleDim
			switch n.Severity {
			case service.SeverityError:
				style = styleBad
			case service.SeverityWarning:
				style = styleWarn
			}
			b.WriteString(style.Render(n.Text) + "\n")
		}
	}
	if st.ForgetHeld && !st.AppendOnly {
		b.WriteString("\n" + styleWarn.Render("F  approve the held forget (review it in Snapshots, f)"))
	}
	return strings.TrimRight(b.String(), "\n")
}

func webLine(w service.WebUIStatus) string {
	if !w.Enabled {
		return fmt.Sprintf("%-12s  off (w opens it and shows a login link)", "Web UI")
	}
	s := fmt.Sprintf("%-12s  on at %s", "Web UI", w.Addr)
	if !w.Until.IsZero() {
		s += " until " + present.Stamp(w.Until)
	}
	return s
}

func (m *Model) snapshotsPage() string {
	if m.showForget {
		return m.forgetPage()
	}
	if len(m.snapshots) == 0 {
		return "There are no snapshots yet. b starts a backup."
	}
	var b strings.Builder
	b.WriteString(styleDim.Render(fmt.Sprintf("%-9s %-19s %-9s %-9s %8s %10s  %s", "ID", "TIME", "AGE", "ORIGIN", "FILES", "SIZE", "FLAGS / NOTE")) + "\n")
	now := m.o.Now()
	first, last := window(m.cursor[tabSnapshots], len(m.snapshots), m.height-12)
	for i := first; i < last; i++ {
		s := m.snapshots[i]
		files, size := "-", "-"
		if s.HasSummary {
			files, size = strconv.FormatInt(s.Files, 10), present.Bytes(s.Bytes)
		}
		info := strings.Join(present.SnapshotFlags(s), ",")
		if s.Note != "" {
			info += " " + s.Note
		}
		line := fmt.Sprintf("%-9s %-19s %-9s %-9s %8s %10s  %s", s.ShortID, present.Stamp(s.Time), present.Duration(now.Sub(s.Time)), s.Origin, files, size, info)
		switch {
		case i == m.cursor[tabSnapshots]:
			line = styleSel.Render(line)
		case s.Suspicious:
			line = styleBad.Render(line)
		}
		b.WriteString(line + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
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
		return "The repository has no locks."
	}
	var b strings.Builder
	b.WriteString(styleDim.Render(fmt.Sprintf("%-9s %-8s %-10s %-24s %-8s %s", "ID", "AGE", "EXCLUSIVE", "HOST", "PID", "")) + "\n")
	for i, l := range m.locks {
		note := ""
		if l.Stale {
			note = "stale"
		}
		line := fmt.Sprintf("%-9s %-8s %-10v %-24s %-8d %s", present.Short(l.ID), present.Duration(l.Age.Std()), l.Exclusive, l.Hostname, l.PID, note)
		if i == m.cursor[tabLocks] {
			line = styleSel.Render(line)
		}
		b.WriteString(line + "\n")
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
	w := 0
	for _, v := range m.env.Vars {
		w = max(w, len(v.Name))
	}
	for _, v := range m.env.Vars {
		fmt.Fprintf(&b, "%-*s  %s\n", w, v.Name, v.Value)
	}
	if len(m.env.Args) > 0 {
		fmt.Fprintf(&b, "\nrestic options  %s\n", strings.Join(m.env.Args, " "))
	}
	fmt.Fprintf(&b, "\nrestic %s or newer is needed.\n", m.env.MinVersion)
	for _, n := range m.env.Notes {
		b.WriteString(styleDim.Render("• "+wrap(n, m.width-8)) + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// operationPanel shows the running operation or the result of the last one.
func (m *Model) operationPanel() string {
	now := m.o.Now()
	switch {
	case m.op != nil:
		var b strings.Builder
		b.WriteString(styleTitle.Render("Running: ") + present.OperationLine(now, *m.op) + "\n")
		if p := m.op.Progress; p != nil && p.Percent > 0 {
			b.WriteString(bar(p.Percent, 30))
			if p.Current != "" {
				b.WriteString("  " + styleDim.Render(p.Current))
			}
			b.WriteString("\n")
		}
		for _, l := range tail(m.log, 4) {
			b.WriteString(styleDim.Render(l) + "\n")
		}
		return strings.TrimRight(b.String(), "\n")
	case m.lastResult != nil:
		var b strings.Builder
		for i, l := range present.ResultLines(*m.lastResult) {
			if i == 0 {
				switch {
				case m.lastResult.Result.OK():
					l = styleGood.Render(l)
				default:
					l = styleBad.Render(l)
				}
			}
			b.WriteString(l + "\n")
		}
		if r := m.lastResult; r.Kind == service.KindRestore && !r.Result.OK() && m.lastRestore != nil && !m.lastRestore.NoSafetySnapshot && m.lastRestore.Target == "" {
			b.WriteString(styleWarn.Render("x  repeat the restore without the safety snapshot") + "\n")
		}
		return strings.TrimRight(b.String(), "\n")
	case m.status != nil && m.status.Retry != nil:
		r := m.status.Retry
		return fmt.Sprintf("The failed backup is retried (%d/%d) at %s. c cancels the retry.", r.Attempt, r.Max, r.At.Local().Format("15:04"))
	}
	return ""
}

func bar(frac float64, width int) string {
	frac = max(0, min(1, frac))
	n := int(frac * float64(width))
	return "[" + strings.Repeat("█", n) + strings.Repeat("░", width-n) + fmt.Sprintf("] %3.0f%%", frac*100)
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
Restore test v  restore a sample of the latest snapshot and check it
Keep        in Snapshots: K  keep / unkeep the selected snapshot
Acknowledge in Snapshots: a  accept a much smaller snapshot
Forget      in Snapshots: f  what the retention would delete
Locks       in Locks: u  remove stale locks, U  remove all
Schedule    s  enable, disable or pause scheduled backups
Log         l  log of the last run
Web UI      w  switch it on and show a one-time login link
Access      in Access: p  show or hide the secrets, y  copy them as shell exports
Other       n  test notification · r  refresh · q  quit`
