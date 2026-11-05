package tui

import (
	"image/color"
	"strings"

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

// exportText renders the restic environment as shell exports.
func exportText(info service.EnvInfo) string {
	var b strings.Builder
	_ = envexport.Write(&b, info, envexport.StyleExport)
	return b.String()
}

// shellLines splits shell text into lines of at most width cells. A command
// that is too long continues in a way the shell still reads as one: inside
// single quotes the line ends with '\ and goes on with ', elsewhere with a
// backslash. Comments continue as comments.
func shellLines(text string, width int) []string {
	var out []string
	for line := range strings.SplitSeq(strings.TrimRight(text, "\n"), "\n") {
		if rest, ok := strings.CutPrefix(line, "# "); ok {
			for l := range strings.SplitSeq(wrap(rest, width-2), "\n") {
				out = append(out, "# "+l)
			}
			continue
		}
		out = append(out, splitShell([]rune(line), width)...)
	}
	return out
}

func splitShell(r []rune, width int) []string {
	var out []string
	for len(r) > width && width >= 10 {
		cut, quoted := shellCut(r, width-2) // room for the continuation marker
		if cut == 0 {
			break
		}
		if quoted {
			out = append(out, string(r[:cut])+`'\`)
			r = append([]rune{'\''}, r[cut:]...)
		} else {
			out = append(out, string(r[:cut])+`\`)
			r = r[cut:]
		}
	}
	return append(out, string(r))
}

// shellCut finds the last place up to max where a shell line can be split, and
// tells whether that place is inside single quotes. It returns 0 if there is none.
func shellCut(r []rune, limit int) (cut int, quoted bool) {
	in, escaped := false, false
	for i := 0; i < len(r) && i <= limit; i++ {
		if i > 0 && (in || !escaped) {
			cut, quoted = i, in
		}
		switch c := r[i]; {
		case escaped:
			escaped = false
		case !in && c == '\\':
			escaped = true
		case c == '\'':
			in = !in
		}
	}
	return cut, quoted
}
