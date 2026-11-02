package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"github.com/spf13/cobra"

	"github.com/noris-network/mcs-backup/v2/internal/present"
	"github.com/noris-network/mcs-backup/v2/internal/service"
)

func (a *app) newStatusCmd() *cobra.Command {
	var follow bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the state of the service",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, closeSvc := a.service(cmd)
			defer closeSvc()
			ctx := cmd.Context()
			st, err := svc.Status(ctx)
			if err != nil {
				return err
			}
			if a.wantJSON(cmd) {
				if err := a.printJSON(st); err != nil {
					return err
				}
			} else {
				a.renderStatus(st)
			}
			if !follow {
				return nil
			}
			return a.followEvents(cmd, svc)
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "keep running and print what the service does")
	return cmd
}

// followEvents prints the events of the service until interrupted.
func (a *app) followEvents(cmd *cobra.Command, svc service.Service) error {
	ctx := cmd.Context()
	asJSON := a.wantJSON(cmd)
	if !asJSON {
		fmt.Fprintln(a.stdout, "\nFollowing the service, press Ctrl-C to stop.")
	}
	for ev, err := range svc.Events(ctx) {
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if asJSON {
			if err := a.printJSONLine(ev); err != nil {
				return err
			}
			continue
		}
		if line := a.eventLine(ev); line != "" {
			fmt.Fprintln(a.stdout, line)
		}
	}
	return nil
}

func (a *app) eventLine(ev service.Event) string {
	ts := ev.Time.Local().Format("15:04:05")
	switch ev.Type {
	case service.EventStarted:
		return fmt.Sprintf("%s %s started (%s)", ts, ev.Operation.Kind, ev.Operation.Source)
	case service.EventPhase:
		return fmt.Sprintf("%s %s: %s", ts, ev.Operation.Kind, present.PhaseTitle(ev.Operation.Phase))
	case service.EventProgress:
		p := ev.Operation.Progress
		return fmt.Sprintf("%s %s: %.0f%%", ts, ev.Operation.Kind, p.Percent*100)
	case service.EventLog:
		text, ok := present.LogLine(ev.Line)
		if !ok {
			return ""
		}
		return fmt.Sprintf("%s   %s", ts, text)
	case service.EventFinished:
		return fmt.Sprintf("%s %s %s", ts, ev.Result.Kind, ev.Result.Result)
	}
	return ts + " status changed"
}

func (a *app) newSnapshotsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "snapshots",
		Short: "List the snapshots, newest first",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, closeSvc := a.service(cmd)
			defer closeSvc()
			snaps, err := svc.Snapshots(cmd.Context())
			if err != nil {
				return err
			}
			if a.wantJSON(cmd) {
				return a.printJSON(snaps)
			}
			a.renderSnapshots(snaps)
			return nil
		},
	}
}

func (a *app) newBackupCmd() *cobra.Command {
	var (
		note        string
		keep        bool
		noWait      bool
		enable      bool
		disable     bool
		showStatus  bool
		maintenance string
	)
	cmd := &cobra.Command{
		Use:   "backup",
		Short: "Start a backup, or control the backup schedule",
		Long: `Start a backup now and follow it until it ends.

The same command controls the schedule:
  --disable            stop scheduled backups until they are enabled again or the service restarts
  --enable             start scheduled backups again
  --maintenance 1h     pause scheduled backups for a while (survives a restart)
  --status             show whether scheduled backups are enabled`,
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			schedule := 0
			for _, set := range []bool{enable, disable, showStatus, maintenance != ""} {
				if set {
					schedule++
				}
			}
			if schedule > 1 {
				return usageError(errors.New("use only one of --enable, --disable, --status and --maintenance"))
			}
			if schedule == 1 && (note != "" || keep) {
				return usageError(errors.New("--note and --keep belong to a backup, not to the schedule options"))
			}

			svc, closeSvc := a.service(cmd)
			defer closeSvc()
			ctx := cmd.Context()
			if schedule == 1 {
				return a.scheduleCommand(cmd, svc, enable, disable, maintenance)
			}
			op, err := svc.StartBackup(ctx, service.BackupRequest{Note: note, Keep: keep})
			if err != nil {
				return err
			}
			return a.waitFor(ctx, cmd, svc, op, noWait)
		},
	}
	f := cmd.Flags()
	f.StringVar(&note, "note", "", "note stored with the snapshot")
	f.BoolVar(&keep, "keep", false, "protect the snapshot from forget")
	f.BoolVar(&noWait, "no-wait", false, "return as soon as the backup has started")
	f.BoolVar(&enable, "enable", false, "enable scheduled backups")
	f.BoolVar(&disable, "disable", false, "disable scheduled backups")
	f.BoolVar(&showStatus, "status", false, "show whether scheduled backups are enabled")
	f.StringVar(&maintenance, "maintenance", "", "pause scheduled backups for this long, e.g. 1h")
	return cmd
}

func (a *app) scheduleCommand(cmd *cobra.Command, svc service.Service, enable, disable bool, maintenance string) error {
	ctx := cmd.Context()
	var st service.Status
	var err error
	switch {
	case enable:
		st, err = svc.SetSchedule(ctx, service.ScheduleRequest{Action: service.ScheduleEnable})
	case disable:
		st, err = svc.SetSchedule(ctx, service.ScheduleRequest{Action: service.ScheduleDisable})
	case maintenance != "":
		d, perr := time.ParseDuration(maintenance)
		if perr != nil || d <= 0 {
			return usageError(fmt.Errorf("--maintenance: %q is not a duration like 30m or 2h", maintenance))
		}
		st, err = svc.SetSchedule(ctx, service.ScheduleRequest{Action: service.ScheduleMaintenance, For: service.Duration(d)})
	default:
		st, err = svc.Status(ctx)
	}
	if err != nil {
		return err
	}
	if a.wantJSON(cmd) {
		return a.printJSON(st.Schedule)
	}
	switch s := st.Schedule; {
	case s.MaintenanceUntil.After(a.now()):
		fmt.Fprintf(a.stdout, "Scheduled backups are paused for maintenance until %s.\n", present.Stamp(s.MaintenanceUntil))
	case !s.Enabled:
		fmt.Fprintln(a.stdout, "Scheduled backups are disabled until they are enabled again or the service restarts.")
	default:
		fmt.Fprintln(a.stdout, "Scheduled backups are enabled.")
	}
	return nil
}

func (a *app) newRestoreCmd() *cobra.Command {
	var (
		target    string
		includes  []string
		merge     bool
		assumeYes bool
		noSafety  bool
		force     bool
		noWait    bool
	)
	cmd := &cobra.Command{
		Use:   "restore [<snapshot> [<hook argument>...]]",
		Short: "Restore a snapshot (the latest if none is given)",
		Long: `Restore a snapshot, by default the latest regular one.

Without --target the data is restored in place into BACKUP_ROOT (or streamed to
the restore script in pipe mode). That replaces the current data, so it needs
confirmation, and a snapshot of the current data is taken first.

Arguments after the snapshot are passed to the pre- and post-restore hooks.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := "latest"
			var hookArgs []string
			if len(args) > 0 {
				ref, hookArgs = args[0], args[1:]
			}
			req := service.RestoreRequest{
				Snapshot: ref, Includes: includes, NoSafetySnapshot: noSafety, IgnoreSpaceWarning: force, HookArgs: hookArgs,
			}
			if merge {
				req.Mode = service.ModeMerge
			}
			if target != "" {
				abs, err := filepath.Abs(target)
				if err != nil {
					return usageError(err)
				}
				req.Target = abs
			}

			svc, closeSvc := a.service(cmd)
			defer closeSvc()
			ctx := cmd.Context()
			pre, err := svc.RestorePreflight(ctx, req)
			if err != nil {
				return err
			}
			if !a.wantJSON(cmd) {
				a.renderPreflight(pre)
				fmt.Fprintln(a.stdout)
			}
			if pre.SpaceWarning && !force {
				return errors.New("there is not enough free space (restore anyway with --force)")
			}
			if pre.InPlace {
				what := "This replaces the current data with snapshot " + ref + "."
				if req.Confirm, err = a.confirm(assumeYes, ref, what); err != nil {
					return err
				}
			}
			op, err := svc.StartRestore(ctx, req)
			if err != nil {
				return err
			}
			return a.waitFor(ctx, cmd, svc, op, noWait)
		},
	}
	f := cmd.Flags()
	f.StringVar(&target, "target", "", "restore into this directory instead of in place")
	f.StringArrayVar(&includes, "include", nil, "restore only this path of the snapshot (repeatable)")
	f.BoolVar(&merge, "merge", false, "in place: keep files that are not in the snapshot instead of removing them")
	f.BoolVar(&assumeYes, "confirm", false, "confirm an in-place restore without asking")
	f.BoolVar(&noSafety, "no-safety-snapshot", false, "do not take a snapshot of the current data first")
	f.BoolVar(&force, "force", false, "restore even if the free space looks too small")
	f.BoolVar(&noWait, "no-wait", false, "return as soon as the restore has started")
	return cmd
}

func (a *app) newCancelCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "cancel",
		Short: "Stop the running operation and discard a pending retry",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, closeSvc := a.service(cmd)
			defer closeSvc()
			if err := svc.Cancel(cmd.Context()); err != nil {
				return err
			}
			if !a.wantJSON(cmd) {
				fmt.Fprintln(a.stdout, "Cancel requested.")
			}
			return nil
		},
	}
}

func (a *app) newVerifyCmd() *cobra.Command {
	var full, noWait bool
	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Test that the latest snapshot can be restored",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, closeSvc := a.service(cmd)
			defer closeSvc()
			req := service.VerifyRequest{}
			if full {
				req.Mode = "full"
			}
			op, err := svc.StartVerify(cmd.Context(), req)
			if err != nil {
				return err
			}
			return a.waitFor(cmd.Context(), cmd, svc, op, noWait)
		},
	}
	cmd.Flags().BoolVar(&full, "full", false, "restore the whole snapshot instead of a sample")
	cmd.Flags().BoolVar(&noWait, "no-wait", false, "return as soon as the test has started")
	return cmd
}

func (a *app) newKeepCmd(name string) *cobra.Command {
	short := "Protect a snapshot from forget"
	if name == "unkeep" {
		short = "Remove the protection of a snapshot"
	}
	return &cobra.Command{
		Use:   name + " <snapshot>",
		Short: short,
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, closeSvc := a.service(cmd)
			defer closeSvc()
			var res service.TagResult
			var err error
			if name == "keep" {
				res, err = svc.Keep(cmd.Context(), args[0])
			} else {
				res, err = svc.Unkeep(cmd.Context(), args[0])
			}
			if err != nil {
				return err
			}
			return a.printTagResult(cmd, name, args[0], res)
		},
	}
}

func (a *app) printTagResult(cmd *cobra.Command, action, ref string, res service.TagResult) error {
	if a.wantJSON(cmd) {
		return a.printJSON(res)
	}
	if res.ID != "" {
		fmt.Fprintf(a.stdout, "%s: snapshot %s is now %s (restic gives a snapshot a new ID when its tags change).\n", action, present.Short(ref), present.Short(res.ID))
	} else {
		fmt.Fprintf(a.stdout, "%s: snapshot %s changed.\n", action, present.Short(ref))
	}
	for _, w := range res.Warnings {
		fmt.Fprintf(a.stdout, "warning: %s\n", w)
	}
	return nil
}

func (a *app) newAcknowledgeCmd() *cobra.Command {
	var assumeYes bool
	cmd := &cobra.Command{
		Use:   "acknowledge <snapshot>",
		Short: "Accept a snapshot that was tagged suspicious",
		Long: `A backup that is much smaller than the one before is tagged suspicious. Until it is
acknowledged it is not used by "latest", forget is paused, and later backups are
compared with the last regular snapshot.`,
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			word, err := a.confirm(assumeYes, service.ConfirmAcknowledge,
				"Snapshot "+args[0]+" becomes a regular snapshot and the baseline for later comparisons.")
			if err != nil {
				return err
			}
			svc, closeSvc := a.service(cmd)
			defer closeSvc()
			res, err := svc.Acknowledge(cmd.Context(), args[0], word)
			if err != nil {
				return err
			}
			return a.printTagResult(cmd, "acknowledge", args[0], res)
		},
	}
	cmd.Flags().BoolVar(&assumeYes, "confirm", false, "confirm without asking")
	return cmd
}

func (a *app) newApproveForgetCmd() *cobra.Command {
	var assumeYes, noWait bool
	cmd := &cobra.Command{
		Use:   "approve-forget",
		Short: "Run the forget that the retention guard held back",
		Long: `When forget would delete many snapshots at once, the retention guard holds it back.
"mcs-backup forget" shows what it would delete; this command runs it once.`,
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			word, err := a.confirm(assumeYes, service.ConfirmForget, "This permanently removes the snapshots shown by \"mcs-backup forget\".")
			if err != nil {
				return err
			}
			svc, closeSvc := a.service(cmd)
			defer closeSvc()
			op, err := svc.ApproveForget(cmd.Context(), word)
			if err != nil {
				return err
			}
			return a.waitFor(cmd.Context(), cmd, svc, op, noWait)
		},
	}
	cmd.Flags().BoolVar(&assumeYes, "confirm", false, "confirm without asking")
	cmd.Flags().BoolVar(&noWait, "no-wait", false, "return as soon as forget has started")
	return cmd
}

func (a *app) newForgetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "forget",
		Short: "Show what the retention policy would delete (changes nothing)",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, closeSvc := a.service(cmd)
			defer closeSvc()
			info, err := svc.Retention(cmd.Context())
			if err != nil {
				return err
			}
			if a.wantJSON(cmd) {
				return a.printJSON(info)
			}
			a.renderRetention(info)
			return nil
		},
	}
}

func (a *app) newLocksCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "locks",
		Short: "List the locks in the repository",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, closeSvc := a.service(cmd)
			defer closeSvc()
			locks, err := svc.Locks(cmd.Context())
			if err != nil {
				return err
			}
			if a.wantJSON(cmd) {
				return a.printJSON(locks)
			}
			a.renderLocks(locks)
			return nil
		},
	}
}

func (a *app) newUnlockCmd() *cobra.Command {
	var all, assumeYes bool
	cmd := &cobra.Command{
		Use:   "unlock",
		Short: "Remove stale locks (with --all: every lock)",
		Long: `Remove the locks that restic considers stale. With --all every lock is removed, even
those of a process that is still working, which can corrupt a running backup or prune.`,
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			word := ""
			if all {
				var err error
				if word, err = a.confirm(assumeYes, service.ConfirmRemoveAll, "This removes every lock, also those of processes that are still working."); err != nil {
					return err
				}
			}
			svc, closeSvc := a.service(cmd)
			defer closeSvc()
			if err := svc.Unlock(cmd.Context(), all, word); err != nil {
				return err
			}
			if !a.wantJSON(cmd) {
				if all {
					fmt.Fprintln(a.stdout, "All locks removed.")
				} else {
					fmt.Fprintln(a.stdout, "Stale locks removed.")
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "remove all locks, not only stale ones")
	cmd.Flags().BoolVar(&assumeYes, "confirm", false, "confirm --all without asking")
	return cmd
}

func (a *app) newLogCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "log [backup|copy|prune|check|verify|restore|forget]",
		Short: "Show the log of the latest run of an operation (default: backup)",
		Args:  usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			kind := service.KindBackup
			if len(args) == 1 {
				kind = service.Kind(args[0])
				valid := []service.Kind{service.KindBackup, service.KindCopy, service.KindPrune, service.KindCheck, service.KindVerify, service.KindRestore, service.KindForget}
				if !slices.Contains(valid, kind) {
					return usageError(fmt.Errorf("unknown operation %q", args[0]))
				}
			}
			svc, closeSvc := a.service(cmd)
			defer closeSvc()
			l, err := svc.Log(cmd.Context(), kind)
			if err != nil {
				return err
			}
			if a.wantJSON(cmd) {
				return a.printJSON(l)
			}
			if len(l.Lines) == 0 {
				fmt.Fprintf(a.stdout, "No %s has run since the service started.\n", kind)
				return nil
			}
			writeLines(a.stdout, l.Lines)
			return nil
		},
	}
}

func (a *app) newNotifyCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "notify", Short: "Notification commands", Args: usageArgs(cobra.NoArgs)}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error { return cmd.Help() }
	cmd.AddCommand(&cobra.Command{
		Use:   "test",
		Short: "Send a test notification through NOTIFY_COMMAND",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, closeSvc := a.service(cmd)
			defer closeSvc()
			if err := svc.NotifyTest(cmd.Context()); err != nil {
				return err
			}
			if !a.wantJSON(cmd) {
				fmt.Fprintln(a.stdout, "Test notification sent.")
			}
			return nil
		},
	})
	return cmd
}
