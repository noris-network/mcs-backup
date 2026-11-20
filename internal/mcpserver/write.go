package mcpserver

import (
	"context"
	"errors"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/noris-network/mcs-backup/v2/internal/service"
)

// started describes an operation that was started, for the agent.
func started(op service.Operation) (*mcp.CallToolResult, any, error) {
	return reply(struct {
		Started   bool              `json:"started"`
		Operation service.Operation `json:"operation"`
		Next      string            `json:"next"`
	}{true, op, "the operation runs in the background; wait for it with get_operation (wait_seconds)"})
}

func (s *Server) registerWrite() {
	type backupIn struct {
		Note string `json:"note,omitempty" jsonschema:"a note stored with the snapshot, e.g. why it was made"`
		Keep bool   `json:"keep,omitempty" jsonschema:"protect the snapshot from the retention"`
	}
	mcp.AddTool(s.srv, &mcp.Tool{
		Name:        "start_backup",
		Description: "Start a backup now. Returns at once; the backup runs in the background. A backup that is much smaller than the one before is kept but marked suspicious and stops the retention until someone acknowledges it.",
		Annotations: additive,
	}, func(c context.Context, _ *mcp.CallToolRequest, in backupIn) (*mcp.CallToolResult, any, error) {
		op, err := s.svc.StartBackup(ctx(c), service.BackupRequest{Note: in.Note, Keep: in.Keep})
		if err != nil {
			return nil, nil, explain(err)
		}
		return started(op)
	})

	type verifyIn struct {
		Mode string `json:"mode,omitempty" jsonschema:"sample (a random part of the latest snapshot, default) or full"`
	}
	mcp.AddTool(s.srv, &mcp.Tool{
		Name:        "start_verify",
		Description: "Start the restore test: restores a sample (or all) of the latest snapshot into a scratch directory, checks it and deletes it again. Touches no live data.",
		Annotations: additive,
	}, func(c context.Context, _ *mcp.CallToolRequest, in verifyIn) (*mcp.CallToolResult, any, error) {
		op, err := s.svc.StartVerify(ctx(c), service.VerifyRequest{Mode: in.Mode})
		if err != nil {
			return nil, nil, explain(err)
		}
		return started(op)
	})

	mcp.AddTool(s.srv, &mcp.Tool{
		Name: "start_check",
		Description: "Start a check of the repositories: that every snapshot is complete and the index is sound (restic check). It runs on its schedule anyway. " +
			"The repository is locked while it runs, so a scheduled backup waits. Returns at once; wait with get_operation.",
		Annotations: additive,
	}, func(c context.Context, _ *mcp.CallToolRequest, _ none) (*mcp.CallToolResult, any, error) {
		op, err := s.svc.StartCheck(ctx(c))
		if err != nil {
			return nil, nil, explain(err)
		}
		return started(op)
	})

	if s.prunable {
		mcp.AddTool(s.srv, &mcp.Tool{
			Name: "start_prune",
			Description: "Start a prune: removes the data that no snapshot needs any more (left by forget) from the repositories that are not append-only. It deletes no snapshot. " +
				"It runs on its schedule anyway; start it when space has to be freed now. The repository is locked while it runs, which can take long on a big repository, so a scheduled backup waits. Returns at once; wait with get_operation.",
			Annotations: additive,
		}, func(c context.Context, _ *mcp.CallToolRequest, _ none) (*mcp.CallToolResult, any, error) {
			op, err := s.svc.StartPrune(ctx(c))
			if err != nil {
				return nil, nil, explain(err)
			}
			return started(op)
		})
	}

	mcp.AddTool(s.srv, &mcp.Tool{
		Name:        "cancel_operation",
		Description: "Stop the running operation and discard a pending retry. restic releases its lock first, which can take a few seconds.",
		Annotations: additive,
	}, func(c context.Context, _ *mcp.CallToolRequest, _ none) (*mcp.CallToolResult, any, error) {
		if err := s.svc.Cancel(ctx(c)); err != nil {
			return nil, nil, explain(err)
		}
		return say("cancel requested")
	})

	type restoreIn struct {
		Snapshot           string   `json:"snapshot" jsonschema:"snapshot ID, or latest"`
		Target             string   `json:"target" jsonschema:"an empty directory to restore into"`
		Include            []string `json:"include,omitempty" jsonschema:"restore only these paths of the snapshot"`
		IgnoreSpaceWarning bool     `json:"ignore_space_warning,omitempty" jsonschema:"restore although the free space looks too small"`
	}
	mcp.AddTool(s.srv, &mcp.Tool{
		Name: "restore_to_directory",
		Description: "Restore a snapshot (or parts of it) into a new or empty directory, e.g. to look at old data. Refused for a directory that has files and for the live data; those need restore_overwrite, which is only available at the destructive access level. " +
			"Check get_restore_preflight first. Returns at once; wait with get_operation.",
		Annotations: additive,
	}, func(c context.Context, _ *mcp.CallToolRequest, in restoreIn) (*mcp.CallToolResult, any, error) {
		c = ctx(c)
		if in.Target == "" {
			return nil, nil, errors.New("target is required: restoring over the live data needs restore_overwrite")
		}
		req := service.RestoreRequest{Snapshot: in.Snapshot, Target: in.Target, Includes: in.Include, Mode: service.ModeMerge, IgnoreSpaceWarning: in.IgnoreSpaceWarning}
		pre, err := s.svc.RestorePreflight(c, req)
		if err != nil {
			return nil, nil, explain(err)
		}
		if pre.InPlace || pre.PipeMode || !pre.TargetEmpty {
			return nil, nil, errors.New("refused: the target is not an empty directory, so this restore could overwrite data; that is only possible with restore_overwrite at the destructive access level")
		}
		op, err := s.svc.StartRestore(c, req)
		if err != nil {
			return nil, nil, explain(err)
		}
		return started(op)
	})

	if !s.appendOnly {
		type snapIn struct {
			Snapshot string `json:"snapshot" jsonschema:"snapshot ID"`
		}
		tag := func(name, what string, fn func(context.Context, string) (service.TagResult, error)) {
			mcp.AddTool(s.srv, &mcp.Tool{
				Name:        name,
				Description: what + " restic writes a snapshot anew when its tags change, so the snapshot gets a new ID; the result names it.",
				Annotations: additive,
			}, func(c context.Context, _ *mcp.CallToolRequest, in snapIn) (*mcp.CallToolResult, any, error) {
				r, err := fn(ctx(c), in.Snapshot)
				if err != nil {
					return nil, nil, explain(err)
				}
				return reply(r)
			})
		}
		tag("keep_snapshot", "Protect a snapshot from the retention for good.", s.svc.Keep)
		tag("unkeep_snapshot", "Let the retention treat a snapshot like any other again.", s.svc.Unkeep)
	}

	mcp.AddTool(s.srv, &mcp.Tool{
		Name:        "unlock_stale_locks",
		Description: "Remove locks of crashed processes. Locks of running processes are renewed and stay. Safe to call when list_locks shows stale locks.",
		Annotations: additive,
	}, func(c context.Context, _ *mcp.CallToolRequest, _ none) (*mcp.CallToolResult, any, error) {
		if err := s.svc.Unlock(ctx(c), false, ""); err != nil {
			return nil, nil, explain(err)
		}
		return say("stale locks removed")
	})

	type scheduleIn struct {
		Action string `json:"action" jsonschema:"enable, disable (until enabled again or the service restarts) or maintenance (pause for a while)"`
		For    string `json:"for,omitempty" jsonschema:"length of the maintenance window, e.g. 90m or 2h; only for maintenance"`
	}
	mcp.AddTool(s.srv, &mcp.Tool{
		Name:        "set_schedule",
		Description: "Switch scheduled backups on, off, or pause them for a maintenance window (the window survives restarts). Manual backups still work.",
		Annotations: additive,
	}, func(c context.Context, _ *mcp.CallToolRequest, in scheduleIn) (*mcp.CallToolResult, any, error) {
		req := service.ScheduleRequest{Action: service.ScheduleAction(in.Action)}
		switch req.Action {
		case service.ScheduleEnable, service.ScheduleDisable:
		case service.ScheduleMaintenance:
			d, err := time.ParseDuration(in.For)
			if err != nil || d <= 0 {
				return nil, nil, errors.New("for is required for maintenance, e.g. 90m or 2h")
			}
			req.For = service.Duration(d)
		default:
			return nil, nil, errors.New("action must be enable, disable or maintenance")
		}
		st, err := s.svc.SetSchedule(ctx(c), req)
		if err != nil {
			return nil, nil, explain(err)
		}
		return reply(st.Schedule)
	})

	mcp.AddTool(s.srv, &mcp.Tool{
		Name:        "test_notification",
		Description: "Send a test notification through the configured notification command.",
		Annotations: additive,
	}, func(c context.Context, _ *mcp.CallToolRequest, _ none) (*mcp.CallToolResult, any, error) {
		if err := s.svc.NotifyTest(ctx(c)); err != nil {
			return nil, nil, explain(err)
		}
		return say("test notification sent")
	})
}

func (s *Server) registerDestructive() {
	type overwriteIn struct {
		Snapshot           string   `json:"snapshot" jsonschema:"snapshot ID, or latest"`
		Target             string   `json:"target,omitempty" jsonschema:"directory to restore into; empty replaces the live data"`
		Include            []string `json:"include,omitempty" jsonschema:"restore only these paths of the snapshot"`
		Mode               string   `json:"mode,omitempty" jsonschema:"exact (files not in the snapshot are removed, default) or merge; applies only when replacing the live data"`
		SafetySnapshot     *bool    `json:"safety_snapshot,omitempty" jsonschema:"take a snapshot of the current data first (default true). Setting it to false loses the current data for good"`
		IgnoreSpaceWarning bool     `json:"ignore_space_warning,omitempty" jsonschema:"restore although the free space looks too small"`
		Confirm            string   `json:"confirm,omitempty" jsonschema:"when replacing the live data: the same text as snapshot"`
	}
	mcp.AddTool(s.srv, &mcp.Tool{
		Name: "restore_overwrite",
		Description: "DESTRUCTIVE. Restore a snapshot over live data (no target) or into a directory that already has files. Replacing the live data first takes a safety snapshot of the current data (snapshot tag pre-restore) unless safety_snapshot is false, and needs confirm equal to snapshot. " +
			"Do this only when the user asked for exactly this restore. Check get_restore_preflight first. Returns at once; wait with get_operation.",
		Annotations: destructive,
	}, func(c context.Context, _ *mcp.CallToolRequest, in overwriteIn) (*mcp.CallToolResult, any, error) {
		req := service.RestoreRequest{
			Snapshot: in.Snapshot, Target: in.Target, Includes: in.Include, Mode: mode(in.Mode),
			IgnoreSpaceWarning: in.IgnoreSpaceWarning, Confirm: in.Confirm,
			NoSafetySnapshot: in.SafetySnapshot != nil && !*in.SafetySnapshot,
		}
		op, err := s.svc.StartRestore(ctx(c), req)
		if err != nil {
			return nil, nil, explain(err)
		}
		return started(op)
	})

	type confirmIn struct {
		Confirm string `json:"confirm" jsonschema:"the confirmation word named in the description"`
	}
	type ackIn struct {
		Snapshot string `json:"snapshot" jsonschema:"ID of the suspicious snapshot"`
		Confirm  string `json:"confirm" jsonschema:"the word acknowledge"`
	}
	if !s.appendOnly {
		mcp.AddTool(s.srv, &mcp.Tool{
			Name:        "acknowledge_snapshot",
			Description: "DESTRUCTIVE. Accept a suspicious snapshot (one that is much smaller than the one before) as a regular snapshot. From then on the retention may delete older snapshots in favor of it. Do this only after a person confirmed the size change is expected. confirm must be the word acknowledge.",
			Annotations: destructive,
		}, func(c context.Context, _ *mcp.CallToolRequest, in ackIn) (*mcp.CallToolResult, any, error) {
			r, err := s.svc.Acknowledge(ctx(c), in.Snapshot, in.Confirm)
			if err != nil {
				return nil, nil, explain(err)
			}
			return reply(r)
		})

		mcp.AddTool(s.srv, &mcp.Tool{
			Name:        "approve_forget",
			Description: "DESTRUCTIVE. Run the forget that the retention guard held back because it would delete many snapshots. Check get_retention for what would be deleted. The snapshots are gone for good afterwards. confirm must be the word forget.",
			Annotations: destructive,
		}, func(c context.Context, _ *mcp.CallToolRequest, in confirmIn) (*mcp.CallToolResult, any, error) {
			op, err := s.svc.ApproveForget(ctx(c), in.Confirm)
			if err != nil {
				return nil, nil, explain(err)
			}
			return started(op)
		})

		mcp.AddTool(s.srv, &mcp.Tool{
			Name:        "unlock_all_locks",
			Description: "DESTRUCTIVE. Remove every lock, including those of running processes. Removing the lock of a running backup can corrupt it. Use unlock_stale_locks unless a person asked for this. confirm must be the word remove-all.",
			Annotations: destructive,
		}, func(c context.Context, _ *mcp.CallToolRequest, in confirmIn) (*mcp.CallToolResult, any, error) {
			if err := s.svc.Unlock(ctx(c), true, in.Confirm); err != nil {
				return nil, nil, explain(err)
			}
			return say("all locks removed")
		})
	}
}
