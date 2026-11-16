package tui

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

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

// formWidth is the widest a dialog with a form or a short text gets: longer
// lines are hard to read. listWidth is the widest a dialog with a list of
// files or a log gets: a path that is cut short is of no use.
const (
	formWidth = 92
	listWidth = 160
)

// boxWidth is the outer width of a dialog on a terminal of the given width.
func boxWidth(width int) int { return min(max(width, 24), formWidth) }

// boxText is the width of the text inside a dialog: without border and padding.
func boxText(width int) int { return boxWidth(width) - 4 }

// listBoxWidth and listText are boxWidth and boxText of a dialog with a list.
func listBoxWidth(width int) int { return min(max(width, 24), listWidth) }

func listText(width int) int { return listBoxWidth(width) - 4 }

// box frames the body of a dialog.
func box(title, body, hint string, width int) string {
	return framed(colAccent, boxWidth(width), boxContent(title, body, hint))
}

// listBox frames the body of a dialog with a list, which may be wider.
func listBox(title, body, hint string, width int) string {
	return framed(colAccent, listBoxWidth(width), boxContent(title, body, hint))
}

func boxContent(title, body, hint string) string {
	return styleAccent.Bold(true).Render(title) + "\n\n" + strings.TrimRight(body, "\n") + "\n\n" + styleDim.Render(hint)
}

// dot is a colored status dot.
func dot(c color.Color) string { return lipgloss.NewStyle().Foreground(c).Render("●") }

// rule is a thin horizontal line.
func rule(width int) string { return styleRule.Render(strings.Repeat("─", max(width, 0))) }

// wrapAtSpaces breaks a line of a log into lines of at most width cells. It
// breaks at spaces only, never inside a date or a path at a hyphen, keeps the
// spaces inside a line (the columns of restic's output) and cuts a word that
// is longer than a line.
func wrapAtSpaces(line string, width int) []string {
	width = max(width, 1)
	var out []string
	cur, n := "", 0
	flush := func() { out, cur, n = append(out, strings.TrimRight(cur, " ")), "", 0 }
	for _, tok := range tokens(line) {
		w := ansi.StringWidth(tok)
		switch {
		case n+w <= width:
			cur, n = cur+tok, n+w
		case strings.TrimLeft(tok, " ") == "": // spaces at a break go
			flush()
		default:
			if n > 0 {
				flush()
			}
			for ansi.StringWidth(tok) > width {
				out = append(out, ansi.Truncate(tok, width, ""))
				tok = ansi.TruncateLeft(tok, width, "")
			}
			cur, n = tok, ansi.StringWidth(tok)
		}
	}
	return append(out, cur)
}

// tokens splits a line into words and the runs of spaces between them.
func tokens(line string) []string {
	var out []string
	start := 0
	for i := 1; i <= len(line); i++ {
		if i == len(line) || (line[i] == ' ') != (line[start] == ' ') {
			out = append(out, line[start:i])
			start = i
		}
	}
	return out
}

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
