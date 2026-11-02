package main

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/noris-network/mcs-backup/v2/internal/present"
	"github.com/noris-network/mcs-backup/v2/internal/service"
)

// renderRows prints labeled rows as an aligned table.
func (a *app) renderRows(rows []present.Row) {
	w := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
	for _, r := range rows {
		fmt.Fprintf(w, "%s\t%s\n", r.Label, r.Text)
	}
	_ = w.Flush()
}

func (a *app) renderStatus(st service.Status) {
	a.renderRows(present.StatusRows(st, a.now()))
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
			files, size = strconv.FormatInt(s.Files, 10), present.Bytes(s.Bytes)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", s.ShortID, present.Stamp(s.Time), present.Duration(a.now().Sub(s.Time)), s.Origin,
			files, size, strings.Join(present.SnapshotFlags(s), ","), s.Note)
	}
	_ = w.Flush()
}

func (a *app) renderLocks(locks []service.Lock) {
	if len(locks) == 0 {
		fmt.Fprintln(a.stdout, "The repository has no locks.")
		return
	}
	w := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tAGE\tEXCLUSIVE\tHOST\tUSER\tPID\tSTALE")
	for _, l := range locks {
		fmt.Fprintf(w, "%s\t%s\t%v\t%s\t%s\t%d\t%v\n", present.Short(l.ID), present.Duration(l.Age.Std()), l.Exclusive, l.Hostname, l.Username, l.PID, l.Stale)
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
	a.renderRows(present.PreflightRows(p, a.now()))
}

// renderResult prints the outcome of an operation.
func (a *app) renderResult(r service.RunResult) {
	lines := present.ResultLines(r)
	lines[0] = present.ResultMark(r.Result) + " " + lines[0]
	writeLines(a.stdout, lines)
}

func writeLines(w io.Writer, lines []string) {
	for _, l := range lines {
		fmt.Fprintln(w, l)
	}
}
