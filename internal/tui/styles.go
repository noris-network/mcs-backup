package tui

import (
	"image/color"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/noris-network/mcs-backup/v2/internal/envexport"
	"github.com/noris-network/mcs-backup/v2/internal/service"
)

// The palette uses mid-tone colors that read well on dark and light terminals.
var (
	colAccent = lipgloss.Color("#7C6CF0")
	colGood   = lipgloss.Color("#2FAE7B")
	colWarn   = lipgloss.Color("#D29922")
	colBad    = lipgloss.Color("#E5484D")
	colMuted  = lipgloss.Color("#8A8F98")
	colLine   = lipgloss.Color("#5A5F69")
	colOnDark = lipgloss.Color("#FFFFFF")
)

var (
	styleTitle  = lipgloss.NewStyle().Bold(true)
	styleDim    = lipgloss.NewStyle().Foreground(colMuted)
	styleBad    = lipgloss.NewStyle().Foreground(colBad).Bold(true)
	styleWarn   = lipgloss.NewStyle().Foreground(colWarn)
	styleGood   = lipgloss.NewStyle().Foreground(colGood)
	styleAccent = lipgloss.NewStyle().Foreground(colAccent)
	styleLabel  = lipgloss.NewStyle().Foreground(colAccent)
	styleKey    = lipgloss.NewStyle().Foreground(colAccent).Bold(true)
	styleSel    = lipgloss.NewStyle().Background(colAccent).Foreground(colOnDark).Bold(true)
	styleBadge  = lipgloss.NewStyle().Background(colAccent).Foreground(colOnDark).Bold(true).Padding(0, 1)
	styleTab    = lipgloss.NewStyle().Foreground(colMuted).Padding(0, 1)
	styleTabOn  = lipgloss.NewStyle().Foreground(colAccent).Bold(true).Underline(true).Padding(0, 1)
	styleLink   = lipgloss.NewStyle().Underline(true).Foreground(colAccent)
	styleRule   = lipgloss.NewStyle().Foreground(colLine)
	styleCardT  = lipgloss.NewStyle().Foreground(colMuted)
)

// framed draws text in a rounded frame of the given total width.
func framed(c color.Color, width int, content string) string {
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(c).Padding(0, 1).Width(width).Render(content)
}

// boxWidth is the outer width of a dialog on a terminal of the given width.
func boxWidth(width int) int { return min(max(width, 24), 92) }

// boxText is the width of the text inside a dialog: without border and padding.
func boxText(width int) int { return boxWidth(width) - 4 }

// box frames the body of a dialog.
func box(title, body, hint string, width int) string {
	inner := boxWidth(width)
	content := styleAccent.Bold(true).Render(title) + "\n\n" + strings.TrimRight(body, "\n") + "\n\n" + styleDim.Render(hint)
	return framed(colAccent, inner, content)
}

// dot is a colored status dot.
func dot(c color.Color) string { return lipgloss.NewStyle().Foreground(c).Render("●") }

// rule is a thin horizontal line.
func rule(width int) string { return styleRule.Render(strings.Repeat("─", max(width, 0))) }

// fill pads a line with spaces to the width, so that a background covers it.
func fill(line string, width int) string {
	return line + strings.Repeat(" ", max(0, width-lipgloss.Width(line)))
}

// wrap breaks text into lines of at most width cells. A word that is longer
// than a line, such as a path, is cut.
func wrap(text string, width int) string {
	if width < 10 {
		return text
	}
	var out, line []string
	n := 0
	flush := func() { out, line, n = append(out, strings.Join(line, " ")), nil, 0 }
	for w := range strings.FieldsSeq(text) {
		for r := []rune(w); ; {
			if n+len(r)+1 > width && len(line) > 0 {
				flush()
			}
			if len(r) <= width {
				line, n = append(line, string(r)), n+len(r)+1
				break
			}
			out = append(out, string(r[:width]))
			r = r[width:]
		}
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
