package tui

import (
	"context"
	"fmt"
	"path"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/noris-network/mcs-backup/v2/internal/present"
	"github.com/noris-network/mcs-backup/v2/internal/service"
)

// filesRows is how many entries the file browser shows at once.
const filesRows = 14

type (
	filesMsg struct {
		list service.FileList
		err  error
	}
	findMsg struct {
		pattern string
		res     service.FindResult
		err     error
	}
)

func fetchFiles(ctx context.Context, svc service.Service, snapshot, dir string) tea.Cmd {
	return func() tea.Msg {
		list, err := svc.Files(ctx, service.FilesRequest{Snapshot: snapshot, Path: dir})
		return filesMsg{list, err}
	}
}

func fetchFind(ctx context.Context, svc service.Service, pattern string) tea.Cmd {
	return func() tea.Msg {
		res, err := svc.Find(ctx, service.FindRequest{Pattern: pattern, IgnoreCase: true})
		return findMsg{pattern, res, err}
	}
}

// filesDialog browses the files of a snapshot and searches all snapshots for
// a file. From either list a restore of the selected entry can be started.
type filesDialog struct {
	snapshot string // as asked for, until the first directory arrived
	list     *service.FileList
	found    *service.FindResult // the result of a search, shown instead of the directory
	pattern  string
	cursor   int
	busy     string // what is being fetched
	err      string

	searching bool // the pattern is being typed
	input     textinput.Model
}

// openFiles opens the file browser at the top of a snapshot.
func (m *Model) openFiles(snapshot string) tea.Cmd {
	m.dlg = &filesDialog{snapshot: snapshot, busy: "reading snapshot " + snapshot}
	return fetchFiles(m.ctx, m.svc, snapshot, "")
}

// gotFiles and gotFind hand what was fetched to the file browser.
func (m *Model) gotFiles(msg filesMsg) {
	d, ok := m.dlg.(*filesDialog)
	if !ok {
		return
	}
	d.busy = ""
	if msg.err != nil {
		d.err = describe(msg.err)
		return
	}
	d.list, d.found, d.cursor, d.err = &msg.list, nil, 0, ""
}

func (m *Model) gotFind(msg findMsg) {
	d, ok := m.dlg.(*filesDialog)
	if !ok {
		return
	}
	d.busy = ""
	if msg.err != nil {
		d.err = describe(msg.err)
		return
	}
	d.found, d.pattern, d.cursor, d.err = &msg.res, msg.pattern, 0, ""
}

func (d *filesDialog) rows() int {
	if d.found != nil {
		return len(d.found.Versions)
	}
	if d.list != nil {
		return len(d.list.Files)
	}
	return 0
}

// selected returns the snapshot and the path of the entry under the cursor.
func (d *filesDialog) selected() (snapshot string, f service.File, ok bool) {
	if d.cursor >= d.rows() {
		return "", service.File{}, false
	}
	if d.found != nil {
		v := d.found.Versions[d.cursor]
		return v.Newest.ID, v.File, true
	}
	return d.list.Snapshot.ID, d.list.Files[d.cursor], true
}

func (d *filesDialog) open(m *Model, snapshot, dir string) tea.Cmd {
	d.busy, d.err = "reading "+dir, ""
	return fetchFiles(m.ctx, m.svc, snapshot, dir)
}

func (d *filesDialog) key(m *Model, k tea.KeyPressMsg) (tea.Cmd, bool) {
	s := k.String()
	if s == "ctrl+c" {
		return nil, true
	}
	if d.searching {
		switch s {
		case "esc":
			d.searching = false
		case "enter":
			pattern := strings.TrimSpace(d.input.Value())
			if d.searching = false; pattern == "" {
				return nil, false
			}
			d.busy, d.err = "searching all snapshots for "+pattern, ""
			return fetchFind(m.ctx, m.svc, pattern), false
		default:
			return typing(&d.input, k), false
		}
		return nil, false
	}
	switch s {
	case "esc", "q":
		if d.found != nil { // back from the search to the directory
			d.found, d.cursor, d.err = nil, 0, ""
			return nil, d.list == nil
		}
		return nil, true
	case "up", "k":
		d.cursor = max(0, d.cursor-1)
	case "down", "j":
		d.cursor = max(0, min(d.rows()-1, d.cursor+1))
	case "pgup":
		d.cursor = max(0, d.cursor-filesRows)
	case "pgdown", "space":
		d.cursor = max(0, min(d.rows()-1, d.cursor+filesRows))
	case "/":
		d.searching, d.input = true, newInput("a name or a pattern such as *.sql")
		d.input.SetValue(d.pattern)
	case "enter", "right", "l":
		snap, f, ok := d.selected()
		switch {
		case !ok:
		case d.found != nil: // show the file where it lies in its newest snapshot
			d.found = nil
			return d.open(m, snap, path.Dir(f.Path)), false
		case f.Type == "dir":
			return d.open(m, snap, f.Path), false
		}
	case "left", "h", "backspace":
		if d.found == nil && d.list != nil && d.list.Path != "/" {
			return d.open(m, d.list.Snapshot.ID, path.Dir(d.list.Path)), false
		}
	case "r":
		if snap, f, ok := d.selected(); ok {
			m.dlg = newRestoreDialogFor(present.Short(snap), f.Path)
			return nil, true
		}
	}
	return nil, false
}

func (d *filesDialog) view(width int) string {
	w := boxText(width)
	var b strings.Builder
	title := "Files"
	switch {
	case d.found != nil:
		title = fmt.Sprintf("Files matching %s in %d snapshots", d.pattern, d.found.Searched)
		if len(d.found.Versions) == 0 {
			b.WriteString("Nothing found.\n")
		}
		first, last := window(d.cursor, len(d.found.Versions), filesRows)
		for i := first; i < last; i++ {
			v := d.found.Versions[i]
			in := fmt.Sprintf("%s  in %d, newest %s %s", fileSize(v.File), v.Snapshots, present.Short(v.Newest.ID), present.Stamp(v.Newest.Time))
			b.WriteString(d.row(i, fileName(v.File, v.Path), in, w) + "\n")
		}
		if d.found.Truncated {
			b.WriteString(styleDim.Render("There are more matches than are shown: narrow the pattern.") + "\n")
		}
	case d.list != nil:
		title = "Files in snapshot " + d.list.Snapshot.ShortID + " of " + present.Stamp(d.list.Snapshot.Time)
		b.WriteString(styleDim.Render(truncate(d.list.Path, w)) + "\n\n")
		if len(d.list.Files) == 0 {
			b.WriteString("The directory is empty.\n")
		}
		first, last := window(d.cursor, len(d.list.Files), filesRows)
		for i := first; i < last; i++ {
			f := d.list.Files[i]
			b.WriteString(d.row(i, fileName(f, path.Base(f.Path)), fileSize(f)+"  "+present.Stamp(f.Mtime), w) + "\n")
		}
		if d.list.Truncated {
			b.WriteString(styleDim.Render(fmt.Sprintf("Only the first %d entries are shown: search with /.", len(d.list.Files))) + "\n")
		}
	}
	if d.searching {
		b.WriteString("\nFind in all snapshots (case is ignored)\n" + field(&d.input, width) + "\n")
	}
	if d.busy != "" {
		b.WriteString("\n" + styleDim.Render(d.busy+" …") + "\n")
	}
	if d.err != "" {
		b.WriteString("\n" + styleBad.Render(wrap(d.err, w)) + "\n")
	}
	hint := "↑↓ move · enter open · ← up · / find · r restore · esc close"
	switch {
	case d.searching:
		hint = "enter find · esc cancel"
	case d.found != nil:
		hint = "↑↓ move · enter show in its snapshot · r restore · / find · esc back"
	}
	return box(title, b.String(), hint, width)
}

// row draws one entry: the name on the left, the details on the right.
func (d *filesDialog) row(i int, name, details string, width int) string {
	cursor, style := "  ", lipgloss.NewStyle()
	if i == d.cursor {
		cursor, style = "> ", styleAccent
	}
	name = truncate(name, max(width-lipgloss.Width(cursor)-lipgloss.Width(details)-2, 12))
	pad := max(width-lipgloss.Width(cursor)-lipgloss.Width(name)-lipgloss.Width(details), 1)
	return style.Render(cursor+name) + strings.Repeat(" ", pad) + styleDim.Render(details)
}

// fileName shows a directory with a trailing slash.
func fileName(f service.File, name string) string {
	if f.Type == "dir" {
		return name + "/"
	}
	return name
}

// fileSize is the size of a file; other entries have none worth showing.
func fileSize(f service.File) string {
	if f.Type != "file" {
		return "-"
	}
	return present.Bytes(f.Size)
}
