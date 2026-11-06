package main

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/noris-network/mcs-backup/v2/internal/present"
	"github.com/noris-network/mcs-backup/v2/internal/service"
)

func (a *app) newLsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ls [<snapshot> [<path>]]",
		Short: "List the files of a directory in a snapshot",
		Long: `List what a snapshot holds in a directory, to see what a restore would bring back.

Without arguments the top of the latest snapshot is listed. What lies in
BACKUP_ROOT is found below "/" in a snapshot: BACKUP_ROOT/docs is "docs" or
"/docs". The path in the file system works as well. To restore a single
entry, pass the path shown here to "mcs-backup restore <snapshot> --include <path>".`,
		Example: `  mcs-backup ls
  mcs-backup ls latest docs/2026
  mcs-backup ls 1a2b3c4d /mnt/docs`,
		Args: usageArgs(cobra.MaximumNArgs(2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			var req service.FilesRequest
			if len(args) > 0 {
				req.Snapshot = args[0]
			}
			if len(args) > 1 {
				req.Path = args[1]
			}
			svc, closeSvc := a.service(cmd)
			defer closeSvc()
			list, err := svc.Files(cmd.Context(), req)
			if err != nil {
				return err
			}
			if a.wantJSON(cmd) {
				return a.printJSON(list)
			}
			a.renderFiles(list)
			return nil
		},
	}
}

func (a *app) newFindCmd() *cobra.Command {
	var (
		snapshot   string
		ignoreCase bool
	)
	cmd := &cobra.Command{
		Use:   "find <pattern>",
		Short: "Search the snapshots for a file",
		Long: `Search the snapshots of this instance for files and directories. The pattern
is a glob that is matched against the name and against the whole path; quote
it, so that the shell does not expand it.

Every state of a file is shown once, with the newest and the oldest snapshot
that has it: pick the snapshot from before the file was lost or damaged and
restore it with "mcs-backup restore <snapshot> --include <path>".`,
		Example: `  mcs-backup find invoice-4711.pdf
  mcs-backup find '*.sql' --snapshot latest
  mcs-backup find -i '/docs/*report*'`,
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, closeSvc := a.service(cmd)
			defer closeSvc()
			res, err := svc.Find(cmd.Context(), service.FindRequest{Pattern: args[0], Snapshot: snapshot, IgnoreCase: ignoreCase})
			if err != nil {
				return err
			}
			if a.wantJSON(cmd) {
				return a.printJSON(res)
			}
			a.renderFind(res)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&snapshot, "snapshot", "", "search only this snapshot (an ID or \"latest\") instead of all")
	f.BoolVarP(&ignoreCase, "ignore-case", "i", false, "ignore case in the pattern")
	return cmd
}

func (a *app) newDiffCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "diff [<snapshot> [<older snapshot>]]",
		Short: "Show what changed between two snapshots",
		Long: `Show what a snapshot lost, gained and changed compared with an older one.
Directories that were removed or added as a whole are listed once.

Without arguments the newest snapshot is compared with the regular snapshot
before it. That is the question behind a backup that was flagged as much
smaller: look at what is gone, then accept it with "mcs-backup acknowledge" or
restore what is missing. With one snapshot, it is compared with the regular
snapshot before it.`,
		Example: `  mcs-backup diff
  mcs-backup diff 1a2b3c4d
  mcs-backup diff latest 9f8e7d6c`,
		Args: usageArgs(cobra.MaximumNArgs(2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			var req service.DiffRequest
			if len(args) > 0 {
				req.To = args[0]
			}
			if len(args) > 1 {
				req.From = args[1]
			}
			svc, closeSvc := a.service(cmd)
			defer closeSvc()
			res, err := svc.Diff(cmd.Context(), req)
			if err != nil {
				return err
			}
			if a.wantJSON(cmd) {
				return a.printJSON(res)
			}
			for _, line := range present.DiffLines(res) {
				fmt.Fprintln(a.stdout, line)
			}
			return nil
		},
	}
}

// fileName shows a directory with a trailing slash.
func fileName(f service.File) string {
	if f.Type == "dir" {
		return f.Path + "/"
	}
	return f.Path
}

// fileSize is the size of a file; other entries have none worth showing.
func fileSize(f service.File) string {
	if f.Type != "file" {
		return "-"
	}
	return present.Bytes(f.Size)
}

func (a *app) renderFiles(list service.FileList) {
	fmt.Fprintf(a.stdout, "%s in snapshot %s of %s\n", list.Path, list.Snapshot.ShortID, present.Stamp(list.Snapshot.Time))
	if len(list.Files) == 0 {
		fmt.Fprintln(a.stdout, "The directory is empty.")
		return
	}
	w := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SIZE\tMODIFIED\tPATH")
	for _, f := range list.Files {
		fmt.Fprintf(w, "%s\t%s\t%s\n", fileSize(f), present.Stamp(f.Mtime), fileName(f))
	}
	_ = w.Flush()
	if list.Truncated {
		fmt.Fprintf(a.stdout, "Only the first %d entries are shown: list a subdirectory or use \"mcs-backup find\".\n", len(list.Files))
	}
}

func (a *app) renderFind(res service.FindResult) {
	if len(res.Versions) == 0 {
		fmt.Fprintf(a.stdout, "Nothing found in %d %s.\n", res.Searched, plural(res.Searched, "snapshot"))
		return
	}
	w := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PATH\tSIZE\tMODIFIED\tNEWEST SNAPSHOT\tOLDEST SNAPSHOT\tIN")
	for _, v := range res.Versions {
		oldest := "-"
		if v.Oldest.ID != v.Newest.ID {
			oldest = snapshotRef(v.Oldest)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%d\n", fileName(v.File), fileSize(v.File), present.Stamp(v.Mtime), snapshotRef(v.Newest), oldest, v.Snapshots)
	}
	_ = w.Flush()
	if res.Truncated {
		fmt.Fprintf(a.stdout, "Only the first %d matches are shown: narrow the pattern.\n", len(res.Versions))
	}
	fmt.Fprintf(a.stdout, "Searched %d %s. Restore with: mcs-backup restore <snapshot> --include <path>\n", res.Searched, plural(res.Searched, "snapshot"))
}

func snapshotRef(r service.SnapshotRef) string {
	return present.Short(r.ID) + " " + present.Stamp(r.Time)
}

// plural returns noun in the plural unless n is 1.
func plural(n int, noun string) string {
	if n == 1 {
		return noun
	}
	return noun + "s"
}
