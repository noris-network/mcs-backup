// Package present turns service types into the plain words that the command
// line, the TUI and the web UI show, so that all of them say the same.
package present

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/noris-network/mcs-backup/v2/internal/service"
)

// Row is one labeled line of a summary. Rows of a list value have an empty label.
type Row struct {
	Label string
	Text  string
	// Severity is set for notes that need attention.
	Severity service.Severity
}

// Duration formats a duration for people: "45s", "5m", "2h 5m", "3d 4h".
func Duration(d time.Duration) string {
	d = d.Round(time.Second)
	switch {
	case d < 0:
		return "-" + Duration(-d)
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		h, m := int(d.Hours()), int(d.Minutes())%60
		if m == 0 {
			return fmt.Sprintf("%dh", h)
		}
		return fmt.Sprintf("%dh %dm", h, m)
	}
	days, h := int(d.Hours())/24, int(d.Hours())%24
	if h == 0 {
		return fmt.Sprintf("%dd", days)
	}
	return fmt.Sprintf("%dd %dh", days, h)
}

// Bytes formats a size with binary units.
func Bytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	// a value that rounds up to 1024.0 is 1.0 of the next unit
	if float64(n)/float64(div) >= unit-0.05 && exp < len("KMGTPE")-1 {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// Stamp formats a time in the local zone.
func Stamp(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04:05")
}

// Ago says how long before now t was.
func Ago(now, t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return Duration(now.Sub(t)) + " ago"
}

// In formats a time in the future together with the time left.
func In(now, t time.Time) string {
	return Stamp(t) + " (in " + Duration(t.Sub(now)) + ")"
}

// Short shortens a snapshot or lock ID.
func Short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// StatusRows summarizes the status in plain words.
func StatusRows(st service.Status, now time.Time) []Row {
	var rows []Row
	add := func(label, format string, args ...any) {
		rows = append(rows, Row{Label: label, Text: fmt.Sprintf(format, args...)})
	}

	add("mcs-backup", "%s, host %s, up %s", st.Version, st.Host, Duration(now.Sub(st.StartedAt)))
	add("Repository", "%s", RepoLine(st.Repository))
	if st.Repository.Up && !st.Repository.LastSuccess.IsZero() {
		add("Last backup", "%s (%s)", Stamp(st.Repository.LastSuccess), Ago(now, st.Repository.LastSuccess))
	}
	if st.Copy != nil {
		add("Secondary", "%s", RepoLine(*st.Copy))
	}

	switch {
	case st.Schedule.MaintenanceUntil.After(now):
		add("Schedule", "paused for maintenance until %s", Stamp(st.Schedule.MaintenanceUntil))
	case !st.Schedule.Enabled:
		add("Schedule", "backups are disabled")
	case st.Schedule.Backup == "":
		add("Schedule", "no backup schedule, backups are started manually")
	default:
		add("Schedule", "backups enabled (%s)", st.Schedule.Backup)
	}
	for _, kind := range []service.Kind{service.KindBackup, service.KindPrune, service.KindCheck, service.KindVerify} {
		if next, ok := st.Schedule.Next[kind]; ok {
			add("  next "+string(kind), "%s", In(now, next))
		}
	}
	add("Retention", "%s", RetentionLine(st))

	switch {
	case st.Operation != nil:
		add("Operation", "%s", OperationLine(now, *st.Operation))
	case st.Retry != nil:
		add("Operation", "idle, retry %d/%d at %s", st.Retry.Attempt, st.Retry.Max, st.Retry.At.Local().Format("15:04"))
	default:
		add("Operation", "idle")
	}

	kinds := make([]service.Kind, 0, len(st.LastRuns))
	for k := range st.LastRuns {
		kinds = append(kinds, k)
	}
	slices.Sort(kinds)
	for i, k := range kinds {
		r := st.LastRuns[k]
		label := ""
		if i == 0 {
			label = "Last runs"
		}
		add(label, "%-12s %-10s %s (%s)%s", KindName(k), r.Result, Stamp(r.Started), Duration(r.Duration.Std()), snapshotSuffix(r))
	}

	for i, warn := range st.Warnings {
		label := ""
		if i == 0 {
			label = "Notes"
		}
		rows = append(rows, Row{Label: label, Text: SeverityMark(warn.Severity) + " " + warn.Message, Severity: warn.Severity})
	}
	return rows
}

// RepoLine describes a repository in one line.
func RepoLine(r service.RepoStatus) string {
	if !r.Up {
		if r.Error != "" {
			return "NOT REACHABLE: " + r.Error
		}
		return "not reachable"
	}
	parts := []string{"reachable"}
	if r.ID != "" {
		parts = append(parts, "id "+Short(r.ID))
	}
	if r.Snapshots >= 0 {
		parts = append(parts, Count(r.Snapshots, "snapshot"))
	}
	if r.SizeBytes >= 0 {
		parts = append(parts, Bytes(r.SizeBytes))
	}
	return strings.Join(parts, ", ")
}

// RetentionLine describes the retention policy in one line.
func RetentionLine(st service.Status) string {
	switch {
	case st.AppendOnly:
		return "none, the repository is append-only"
	case st.Retention == "":
		return "-"
	}
	return "keeps " + st.Retention
}

// SeverityMark is a two character marker for a warning.
func SeverityMark(s service.Severity) string {
	switch s {
	case service.SeverityError:
		return "!!"
	case service.SeverityWarning:
		return "! "
	}
	return "- "
}

func snapshotSuffix(r service.RunResult) string {
	if r.Snapshot == "" {
		return ""
	}
	return ", snapshot " + Short(r.Snapshot)
}

// OperationLine describes a running operation.
func OperationLine(now time.Time, op service.Operation) string {
	s := fmt.Sprintf("%s running for %s (started by %s)", op.Kind, Duration(now.Sub(op.Started)), op.Source)
	if op.Phase != "" {
		s += ", phase " + op.Phase
	}
	if p := op.Progress; p != nil && p.Percent > 0 {
		s += fmt.Sprintf(", %.0f%%", p.Percent*100)
	}
	return s
}

// SnapshotFlags lists the markers of a snapshot.
func SnapshotFlags(s service.Snapshot) []string {
	var f []string
	if s.Keep {
		f = append(f, "keep")
	}
	if s.Suspicious {
		f = append(f, "SUSPICIOUS")
	}
	if s.PreRestore {
		f = append(f, "pre-restore")
	}
	return f
}

// NoteSuffix returns ", note: …" for a snapshot with a note.
func NoteSuffix(s service.Snapshot) string {
	if s.Note == "" {
		return ""
	}
	return ", note: " + s.Note
}

// PreflightRows describes what a restore would do.
func PreflightRows(p service.RestorePreflight, now time.Time) []Row {
	var rows []Row
	add := func(label, format string, args ...any) {
		rows = append(rows, Row{Label: label, Text: fmt.Sprintf(format, args...)})
	}
	s := p.Snapshot
	add("Snapshot", "%s from %s (%s)%s", s.ShortID, Stamp(s.Time), Ago(now, s.Time), NoteSuffix(s))
	switch {
	case p.PipeMode:
		add("Target", "streamed to the restore script (PIPE_OUT_SCRIPT), replaces the application's data")
	case p.InPlace:
		add("Target", "%s (in place, replaces the current data)", p.Target)
	default:
		add("Target", "%s", p.Target)
	}
	// a restore of a few paths leaves the rest of the target alone: the
	// target line alone would sound as if all of it were replaced
	for i, path := range p.Includes {
		add(map[bool]string{true: "Only"}[i == 0], "%s", path)
	}
	if !p.PipeMode {
		add("Mode", "%s", p.Mode)
		// the need is what comes on top of what the target holds now
		switch {
		case p.NeedBytes > 0:
			add("Space", "about %s more needed, %s free", Bytes(p.NeedBytes), Bytes(p.FreeBytes))
		case p.FreeBytes > 0:
			add("Space", "no extra space needed, %s free", Bytes(p.FreeBytes))
		}
	}
	if p.SafetySnapshot {
		add("Safety snapshot", "a snapshot of the current data is taken first")
	} else if p.InPlace {
		add("Safety snapshot", "none")
	}
	if len(p.Hooks) > 0 {
		add("Hooks", "%s", strings.Join(p.Hooks, ", "))
	}
	if c := p.Changes; c != nil {
		add("Changes", "%d overwritten, %d deleted, %d created, %d unchanged (files and directories)", c.Overwritten, c.Deleted, c.Created, c.Unchanged)
		list := func(label string, paths []string, total int) {
			if len(paths) == 0 {
				return
			}
			// one row per path: the label stands in front of the first only
			for i, path := range paths {
				add(map[bool]string{true: label}[i == 0], "%s", path)
			}
			if more := total - len(paths); more > 0 {
				add("", "… and %d more", more)
			}
		}
		list("Deletes", c.DeletedPaths, c.Deleted)
		list("Overwrites", c.OverwrittenPaths, c.Overwritten)
	}
	for _, warn := range p.Warnings {
		rows = append(rows, Row{Label: "!", Text: warn, Severity: service.SeverityWarning})
	}
	return rows
}

// ResultLines describes the outcome of an operation.
func ResultLines(r service.RunResult) []string {
	head := fmt.Sprintf("%s: %s", KindName(r.Kind), r.Result)
	if r.Duration > 0 {
		head += fmt.Sprintf(" (%s)", Duration(r.Duration.Std()))
	}
	lines := []string{head}
	// a text of several lines, such as an error of restic, keeps its further
	// lines under its first
	add := func(text string) {
		for i, l := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
			if i == 0 {
				lines = append(lines, "  "+l)
			} else {
				lines = append(lines, "    "+l)
			}
		}
	}
	if r.Result == service.ResultCancelled {
		// someone stopped it: "failed" and the error that the stop caused in
		// restic would only alarm
		note := "stopped before it finished"
		if r.Source != "" {
			note = "stopped by " + r.Source + " before it finished"
		}
		add(note)
		for _, w := range r.Warnings {
			add("warning: " + w)
		}
		return lines
	}
	if r.Snapshot != "" && !strings.Contains(r.Message, Short(r.Snapshot)) {
		add("snapshot " + Short(r.Snapshot))
	}
	if r.Message != "" {
		add(r.Message)
	}
	if r.Error != "" {
		add("error: " + r.Error)
	}
	for _, w := range r.Warnings {
		add("warning: " + w)
	}
	if r.WillRetry {
		lines = append(lines, "  the backup will be retried")
	}
	return lines
}

// DiffLines tells in words what changed from one snapshot to another: what
// is gone first, as that is what matters for a backup that shrank.
func DiffLines(d service.DiffResult) []string {
	lines := []string{
		fmt.Sprintf("From snapshot %s of %s to snapshot %s of %s:", d.From.ShortID, Stamp(d.From.Time), d.To.ShortID, Stamp(d.To.Time)),
		"  removed  " + diffCount(d.Removed),
		"  added    " + diffCount(d.Added),
		fmt.Sprintf("  changed  %d %s", d.Changed, plural(d.Changed, "file")),
	}
	if d.Removed.Files+d.Removed.Dirs+d.Added.Files+d.Added.Dirs+d.Changed == 0 {
		return append(lines, "", "The snapshots hold the same files.")
	}
	lines = append(lines, diffPaths("Removed", d.Removed)...)
	lines = append(lines, diffPaths("Added", d.Added)...)
	if len(d.ChangedPaths) > 0 {
		lines = append(lines, "", "Changed:")
		for _, p := range d.ChangedPaths {
			lines = append(lines, "  "+p)
		}
		if more := d.Changed - len(d.ChangedPaths); more > 0 {
			lines = append(lines, fmt.Sprintf("  … and %d more", more))
		}
	}
	return lines
}

func diffCount(s service.DiffSide) string {
	return fmt.Sprintf("%d %s, %d %s (%s in the repository)", s.Files, plural(s.Files, "file"), s.Dirs, plural(s.Dirs, "directory"), Bytes(s.Bytes))
}

func diffPaths(title string, s service.DiffSide) []string {
	if len(s.Paths) == 0 {
		return nil
	}
	lines := []string{"", title + ":"}
	for _, p := range s.Paths {
		line := "  " + p.Path
		if p.Entries > 1 {
			line += fmt.Sprintf("  (%d entries)", p.Entries)
		}
		lines = append(lines, line)
	}
	if s.More > 0 {
		lines = append(lines, fmt.Sprintf("  … and %d more", s.More))
	}
	return lines
}

// plural returns noun in the plural unless n is 1.
// Count says how many of a thing there are: "1 snapshot", "3 snapshots".
func Count(n int, noun string) string { return fmt.Sprintf("%d %s", n, plural(n, noun)) }

func plural(n int, noun string) string {
	switch {
	case n == 1:
		return noun
	case strings.HasSuffix(noun, "y"):
		return strings.TrimSuffix(noun, "y") + "ies"
	}
	return noun + "s"
}
