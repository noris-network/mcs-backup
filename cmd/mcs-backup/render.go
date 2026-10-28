package main

import (
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/noris-network/mcs-backup/v2/internal/service"
)

// humanDuration formats a duration for people: "45s", "5m", "2h 5m", "3d 4h".
func humanDuration(d time.Duration) string {
	d = d.Round(time.Second)
	switch {
	case d < 0:
		return "-" + humanDuration(-d)
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

// humanBytes formats a size with binary units.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// stamp formats a time in the local zone.
func stamp(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04:05")
}

func (a *app) ago(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return humanDuration(a.now().Sub(t)) + " ago"
}

func (a *app) in(t time.Time) string {
	return stamp(t) + " (in " + humanDuration(t.Sub(a.now())) + ")"
}

func (a *app) renderStatus(st service.Status) {
	w := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
	defer func() { _ = w.Flush() }()
	row := func(label, format string, args ...any) {
		fmt.Fprintf(w, "%s\t%s\n", label, fmt.Sprintf(format, args...))
	}

	row("mcs-backup", "%s, host %s, up %s", st.Version, st.Host, humanDuration(a.now().Sub(st.StartedAt)))
	row("Repository", "%s", a.repoLine(st.Repository))
	if st.Repository.Up && !st.Repository.LastSuccess.IsZero() {
		row("Last backup", "%s (%s)", stamp(st.Repository.LastSuccess), a.ago(st.Repository.LastSuccess))
	}
	if st.Copy != nil {
		row("Secondary", "%s", a.repoLine(*st.Copy))
	}

	switch {
	case st.Schedule.MaintenanceUntil.After(a.now()):
		row("Schedule", "paused for maintenance until %s", stamp(st.Schedule.MaintenanceUntil))
	case !st.Schedule.Enabled:
		row("Schedule", "backups are disabled")
	case st.Schedule.Backup == "":
		row("Schedule", "no backup schedule, backups are started manually")
	default:
		row("Schedule", "backups enabled (%s)", st.Schedule.Backup)
	}
	for _, kind := range []service.Kind{service.KindBackup, service.KindPrune, service.KindCheck, service.KindVerify} {
		if next, ok := st.Schedule.Next[kind]; ok {
			row("  next "+string(kind), "%s", a.in(next))
		}
	}
	row("Retention", "%s", retentionLine(st))

	switch {
	case st.Operation != nil:
		row("Operation", "%s", a.operationLine(*st.Operation))
	case st.Retry != nil:
		row("Operation", "idle, retry %d/%d at %s", st.Retry.Attempt, st.Retry.Max, st.Retry.At.Local().Format("15:04"))
	default:
		row("Operation", "idle")
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
		row(label, "%-8s %-10s %s (%s)%s", k, r.Result, stamp(r.Started), humanDuration(r.Duration.Std()), snapshotSuffix(r))
	}

	for i, warn := range st.Warnings {
		label := ""
		if i == 0 {
			label = "Notes"
		}
		row(label, "%s %s", severityMark(warn.Severity), warn.Message)
	}
}

func (a *app) repoLine(r service.RepoStatus) string {
	if !r.Up {
		if r.Error != "" {
			return "NOT REACHABLE: " + r.Error
		}
		return "not reachable"
	}
	parts := []string{"reachable"}
	if r.ID != "" {
		parts = append(parts, "id "+short(r.ID))
	}
	if r.Snapshots >= 0 {
		parts = append(parts, fmt.Sprintf("%d snapshots", r.Snapshots))
	}
	if r.SizeBytes >= 0 {
		parts = append(parts, humanBytes(r.SizeBytes))
	}
	return strings.Join(parts, ", ")
}

func retentionLine(st service.Status) string {
	switch {
	case st.AppendOnly:
		return "none, the repository is append-only"
	case st.Retention == "":
		return "-"
	}
	return "keeps " + st.Retention
}

func severityMark(s service.Severity) string {
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
	return ", snapshot " + short(r.Snapshot)
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func (a *app) operationLine(op service.Operation) string {
	s := fmt.Sprintf("%s running for %s (started by %s)", op.Kind, humanDuration(a.now().Sub(op.Started)), op.Source)
	if op.Phase != "" {
		s += ", phase " + op.Phase
	}
	if p := op.Progress; p != nil && p.Percent > 0 {
		s += fmt.Sprintf(", %.0f%%", p.Percent*100)
	}
	return s
}

func (a *app) renderSnapshots(snaps []service.Snapshot) {
	if len(snaps) == 0 {
		fmt.Fprintln(a.stdout, "There are no snapshots yet.")
		return
	}
	w := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tTIME\tAGE\tORIGIN\tFILES\tSIZE\tFLAGS\tNOTE")
	for _, s := range snaps {
		files, size := "-", "-"
		if s.HasSummary {
			files, size = strconv.FormatInt(s.Files, 10), humanBytes(s.Bytes)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", s.ShortID, stamp(s.Time), humanDuration(a.now().Sub(s.Time)), s.Origin,
			files, size, strings.Join(snapshotFlags(s), ","), s.Note)
	}
	_ = w.Flush()
}

func snapshotFlags(s service.Snapshot) []string {
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
	if s.CopiedFrom != "" {
		f = append(f, "copy")
	}
	return f
}

func (a *app) renderLocks(locks []service.Lock) {
	if len(locks) == 0 {
		fmt.Fprintln(a.stdout, "The repository has no locks.")
		return
	}
	w := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tAGE\tEXCLUSIVE\tHOST\tUSER\tPID\tSTALE")
	for _, l := range locks {
		fmt.Fprintf(w, "%s\t%s\t%v\t%s\t%s\t%d\t%v\n", short(l.ID), humanDuration(l.Age.Std()), l.Exclusive, l.Hostname, l.Username, l.PID, l.Stale)
	}
	_ = w.Flush()
}

func (a *app) renderRetention(r service.RetentionInfo) {
	switch {
	case r.AppendOnly:
		fmt.Fprintln(a.stdout, "The repository is append-only: nothing is ever forgotten.")
		return
	case r.Policy != "":
		fmt.Fprintf(a.stdout, "Policy: keep %s, and every snapshot marked keep.\n", r.Policy)
	}
	fmt.Fprintf(a.stdout, "Applies to host %s: %d snapshots", strings.Join(r.Hosts, ", "), r.Total)
	if r.Foreign > 0 {
		fmt.Fprintf(a.stdout, " (%d snapshots of other hosts are never touched)", r.Foreign)
	}
	fmt.Fprintln(a.stdout)
	if len(r.Remove) == 0 {
		fmt.Fprintln(a.stdout, "The next forget would remove nothing.")
		return
	}
	fmt.Fprintf(a.stdout, "The next forget would remove %d snapshots:\n", len(r.Remove))
	a.renderSnapshots(r.Remove)
	if r.Held {
		fmt.Fprintln(a.stdout, "\nThis is more than the retention guard allows: forget is held back until it is approved (mcs-backup approve-forget).")
	}
}

func (a *app) renderPreflight(p service.RestorePreflight) {
	w := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
	defer func() { _ = w.Flush() }()
	row := func(label, format string, args ...any) {
		fmt.Fprintf(w, "%s\t%s\n", label, fmt.Sprintf(format, args...))
	}
	s := p.Snapshot
	row("Snapshot", "%s from %s (%s)%s", s.ShortID, stamp(s.Time), a.ago(s.Time), noteSuffix(s))
	switch {
	case p.PipeMode:
		row("Target", "streamed to the restore script (PIPE_OUT_SCRIPT), replaces the application's data")
	case p.InPlace:
		row("Target", "%s (in place, replaces the current data)", p.Target)
	default:
		row("Target", "%s", p.Target)
	}
	if !p.PipeMode {
		row("Mode", "%s", p.Mode)
		if p.NeedBytes > 0 || p.FreeBytes > 0 {
			row("Space", "about %s needed, %s free", humanBytes(p.NeedBytes), humanBytes(p.FreeBytes))
		}
	}
	if p.SafetySnapshot {
		row("Safety snapshot", "a snapshot of the current data is taken first")
	} else if p.InPlace {
		row("Safety snapshot", "none")
	}
	if len(p.Hooks) > 0 {
		row("Hooks", "%s", strings.Join(p.Hooks, ", "))
	}
	for _, warn := range p.Warnings {
		row("!", "%s", warn)
	}
}

func noteSuffix(s service.Snapshot) string {
	if s.Note == "" {
		return ""
	}
	return ", note: " + s.Note
}

// renderResult prints the outcome of an operation.
func (a *app) renderResult(r service.RunResult) {
	fmt.Fprintf(a.stdout, "%s: %s", r.Kind, r.Result)
	if r.Duration > 0 {
		fmt.Fprintf(a.stdout, " (%s)", humanDuration(r.Duration.Std()))
	}
	fmt.Fprintln(a.stdout)
	if r.Snapshot != "" {
		fmt.Fprintf(a.stdout, "  snapshot %s\n", short(r.Snapshot))
	}
	if r.Message != "" {
		fmt.Fprintf(a.stdout, "  %s\n", r.Message)
	}
	if r.Error != "" {
		fmt.Fprintf(a.stdout, "  error: %s\n", r.Error)
	}
	for _, w := range r.Warnings {
		fmt.Fprintf(a.stdout, "  warning: %s\n", w)
	}
	if r.WillRetry {
		fmt.Fprintln(a.stdout, "  the backup will be retried")
	}
}

func writeLines(w io.Writer, lines []string) {
	for _, l := range lines {
		fmt.Fprintln(w, l)
	}
}
