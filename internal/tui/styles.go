package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/noris-network/mcs-backup/v2/internal/envexport"
	"github.com/noris-network/mcs-backup/v2/internal/service"
)

var (
	styleTitle = lipgloss.NewStyle().Bold(true)
	styleDim   = lipgloss.NewStyle().Faint(true)
	styleBad   = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true)
	styleWarn  = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	styleGood  = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	styleSel   = lipgloss.NewStyle().Reverse(true)
	styleTab   = lipgloss.NewStyle().Padding(0, 1)
	styleTabOn = lipgloss.NewStyle().Padding(0, 1).Bold(true).Reverse(true)
	styleLink  = lipgloss.NewStyle().Underline(true)
	styleBox   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
)

// box frames the body of a dialog.
func box(title, body, hint string, width int) string {
	inner := min(max(width-4, 20), 90)
	content := styleTitle.Render(title) + "\n\n" + strings.TrimRight(body, "\n") + "\n\n" + styleDim.Render(hint)
	return styleBox.Width(inner).Render(content)
}

// wrap breaks text into lines of at most width cells.
func wrap(text string, width int) string {
	if width < 10 {
		return text
	}
	var out, line []string
	n := 0
	for w := range strings.FieldsSeq(text) {
		if n+len(w)+1 > width && len(line) > 0 {
			out, line, n = append(out, strings.Join(line, " ")), nil, 0
		}
		line, n = append(line, w), n+len(w)+1
	}
	return strings.Join(append(out, strings.Join(line, " ")), "\n")
}

// clip shortens lines that are wider than width.
func clip(lines []string, width int) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		if r := []rune(l); len(r) > width && width > 1 {
			l = string(r[:width-1]) + "…"
		}
		out[i] = l
	}
	return out
}

// exportText renders the restic environment as shell exports.
func exportText(info service.EnvInfo) string {
	var b strings.Builder
	_ = envexport.Write(&b, info, envexport.StyleExport)
	return b.String()
}

// copyText puts text on the clipboard, with OSC 52 and, if configured, a
// helper for terminals that cannot do it.
func copyText(o Options, text, note string) tea.Cmd {
	done := func() tea.Msg {
		if o.Copy != nil {
			if err := o.Copy(text); err != nil {
				return doneMsg{what: "copy", err: err}
			}
		}
		return doneMsg{what: "copy", note: note}
	}
	return tea.Batch(tea.SetClipboard(text), done)
}
