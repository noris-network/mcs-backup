package mcpserver

import (
	"context"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/noris-network/mcs-backup/v2/internal/service"
)

// maxWait is the longest get_operation waits, so that a call never hangs for
// minutes.
const maxWait = 60 * time.Second

func (s *Server) registerRead() {
	mcp.AddTool(s.srv, &mcp.Tool{
		Name:        "get_status",
		Description: "Summary of the backup service: repository state, last backup, schedule, retention, the running operation, last results and warnings that need attention. Look here first.",
		Annotations: readOnly,
	}, func(c context.Context, _ *mcp.CallToolRequest, _ none) (*mcp.CallToolResult, any, error) {
		st, err := s.svc.Status(ctx(c))
		if err != nil {
			return nil, nil, explain(err)
		}
		return reply(st)
	})

	mcp.AddTool(s.srv, &mcp.Tool{
		Name:        "list_snapshots",
		Description: "The snapshots of this instance. Flags: keep (never deleted by the retention), suspicious (much smaller than the one before, waits for acknowledgement), pre_restore (safety snapshot taken before a restore). 'latest' in other tools means the newest snapshot without the suspicious and pre_restore flags.",
		Annotations: readOnly,
	}, func(c context.Context, _ *mcp.CallToolRequest, _ none) (*mcp.CallToolResult, any, error) {
		snaps, err := s.svc.Snapshots(ctx(c))
		if err != nil {
			return nil, nil, explain(err)
		}
		return reply(snaps)
	})

	mcp.AddTool(s.srv, &mcp.Tool{
		Name:        "get_retention",
		Description: "The retention policy in plain words and which snapshots the next forget would delete. Shows whether the retention guard holds the forget back because it would delete too much.",
		Annotations: readOnly,
	}, func(c context.Context, _ *mcp.CallToolRequest, _ none) (*mcp.CallToolResult, any, error) {
		r, err := s.svc.Retention(ctx(c))
		if err != nil {
			return nil, nil, explain(err)
		}
		return reply(r)
	})

	type filesIn struct {
		Snapshot string `json:"snapshot,omitempty" jsonschema:"snapshot ID, or latest (the default)"`
		Path     string `json:"path,omitempty" jsonschema:"directory in the snapshot, such as /docs; empty lists the top"`
	}
	mcp.AddTool(s.srv, &mcp.Tool{
		Name:        "list_files",
		Description: "The files and directories in one directory of a snapshot (not recursive). What lies in BACKUP_ROOT is found below / in a snapshot. Use the paths it returns as include paths of a restore.",
		Annotations: readOnly,
	}, func(c context.Context, _ *mcp.CallToolRequest, in filesIn) (*mcp.CallToolResult, any, error) {
		l, err := s.svc.Files(ctx(c), service.FilesRequest{Snapshot: in.Snapshot, Path: in.Path})
		if err != nil {
			return nil, nil, explain(err)
		}
		return reply(l)
	})

	type findIn struct {
		Pattern    string `json:"pattern" jsonschema:"glob matched against the file name and the whole path, such as invoice.pdf, *.sql or /docs/*"`
		Snapshot   string `json:"snapshot,omitempty" jsonschema:"search only this snapshot (ID or latest); all snapshots if empty"`
		IgnoreCase bool   `json:"ignore_case,omitempty"`
	}
	mcp.AddTool(s.srv, &mcp.Tool{
		Name:        "find_files",
		Description: "Search the snapshots for files by name. Every state of a file (size and modification time) is returned once, with the newest and the oldest snapshot that has it: to bring back a lost or damaged file, restore its path from the newest snapshot that still has the good version.",
		Annotations: readOnly,
	}, func(c context.Context, _ *mcp.CallToolRequest, in findIn) (*mcp.CallToolResult, any, error) {
		r, err := s.svc.Find(ctx(c), service.FindRequest{Pattern: in.Pattern, Snapshot: in.Snapshot, IgnoreCase: in.IgnoreCase})
		if err != nil {
			return nil, nil, explain(err)
		}
		return reply(r)
	})

	type diffIn struct {
		Snapshot string `json:"snapshot,omitempty" jsonschema:"the newer snapshot (ID or latest); empty is the newest snapshot, also a suspicious one"`
		Older    string `json:"older,omitempty" jsonschema:"the snapshot to compare with; empty is the regular snapshot before the newer one"`
	}
	mcp.AddTool(s.srv, &mcp.Tool{
		Name:        "get_snapshot_diff",
		Description: "What a snapshot lost, gained and changed compared with an older one. Directories removed or added as a whole are listed once, the largest first. Without arguments it tells what the newest backup lost against the last regular one: the way to judge a snapshot that is flagged suspicious before it is acknowledged.",
		Annotations: readOnly,
	}, func(c context.Context, _ *mcp.CallToolRequest, in diffIn) (*mcp.CallToolResult, any, error) {
		d, err := s.svc.Diff(ctx(c), service.DiffRequest{To: in.Snapshot, From: in.Older})
		if err != nil {
			return nil, nil, explain(err)
		}
		return reply(d)
	})

	type preflightIn struct {
		Snapshot string   `json:"snapshot" jsonschema:"snapshot ID, or latest"`
		Target   string   `json:"target,omitempty" jsonschema:"directory to restore to; empty means replacing the live data"`
		Include  []string `json:"include,omitempty" jsonschema:"restore only these paths of the snapshot"`
		Mode     string   `json:"mode,omitempty" jsonschema:"exact (files not in the snapshot are removed, default) or merge; applies only when replacing the live data"`
	}
	mcp.AddTool(s.srv, &mcp.Tool{
		Name:        "get_restore_preflight",
		Description: "Check a restore without performing it: the snapshot, whether it would replace live data, whether the target is empty, the space needed and free, the hooks that would run and warnings. Does not change anything.",
		Annotations: readOnly,
	}, func(c context.Context, _ *mcp.CallToolRequest, in preflightIn) (*mcp.CallToolResult, any, error) {
		p, err := s.svc.RestorePreflight(ctx(c), service.RestoreRequest{Snapshot: in.Snapshot, Target: in.Target, Includes: in.Include, Mode: mode(in.Mode)})
		if err != nil {
			return nil, nil, explain(err)
		}
		return reply(p)
	})

	type opIn struct {
		WaitSeconds int `json:"wait_seconds,omitempty" jsonschema:"wait up to this many seconds (at most 60) for the running operation to end"`
	}
	mcp.AddTool(s.srv, &mcp.Tool{
		Name:        "get_operation",
		Description: "The operation that is running now, if any. With wait_seconds it waits until the operation ends (or the time is up) and then returns its result. Use it after a start_* tool or a restore.",
		Annotations: readOnly,
	}, func(c context.Context, _ *mcp.CallToolRequest, in opIn) (*mcp.CallToolResult, any, error) {
		return s.operation(ctx(c), time.Duration(in.WaitSeconds)*time.Second)
	})

	type logIn struct {
		Kind string `json:"kind,omitempty" jsonschema:"backup, copy, prune, check, verify or restore; default backup"`
	}
	mcp.AddTool(s.srv, &mcp.Tool{
		Name:        "get_log",
		Description: "The log of the latest run of one kind of operation, for finding out why something failed.",
		Annotations: readOnly,
	}, func(c context.Context, _ *mcp.CallToolRequest, in logIn) (*mcp.CallToolResult, any, error) {
		kind := service.Kind(in.Kind)
		if kind == "" {
			kind = service.KindBackup
		}
		l, err := s.svc.Log(ctx(c), kind)
		if err != nil {
			return nil, nil, explain(err)
		}
		return reply(l)
	})

	mcp.AddTool(s.srv, &mcp.Tool{
		Name:        "list_locks",
		Description: "Locks in the repository. A lock of a running restic process is renewed and normal; stale locks (a crashed process) block operations until removed.",
		Annotations: readOnly,
	}, func(c context.Context, _ *mcp.CallToolRequest, _ none) (*mcp.CallToolResult, any, error) {
		l, err := s.svc.Locks(ctx(c))
		if err != nil {
			return nil, nil, explain(err)
		}
		return reply(l)
	})

	mcp.AddTool(s.srv, &mcp.Tool{
		Name:        "get_restic_env",
		Description: "The names of the environment variables that restic needs for this repository, which of them are secrets, restic options and notes about credentials that live in files or roles. Values are never returned.",
		Annotations: readOnly,
	}, func(c context.Context, _ *mcp.CallToolRequest, _ none) (*mcp.CallToolResult, any, error) {
		info, err := s.svc.Env(ctx(c), false)
		if err != nil {
			return nil, nil, explain(err)
		}
		type variable struct {
			Name   string `json:"name"`
			Secret bool   `json:"secret"`
		}
		out := struct {
			Variables  []variable `json:"variables"`
			Options    []string   `json:"restic_options,omitempty"`
			Notes      []string   `json:"notes,omitempty"`
			MinVersion string     `json:"min_restic_version"`
		}{Options: info.Args, Notes: info.Notes, MinVersion: info.MinVersion}
		for _, v := range info.Vars {
			out.Variables = append(out.Variables, variable{v.Name, v.Secret})
		}
		return reply(out)
	})
}

// operation returns the running operation, waiting for its end if asked.
func (s *Server) operation(c context.Context, wait time.Duration) (*mcp.CallToolResult, any, error) {
	op, err := s.svc.Operation(c)
	if err != nil {
		return nil, nil, explain(err)
	}
	if op == nil {
		return s.idle(c)
	}
	if wait <= 0 {
		return reply(struct {
			Running   bool               `json:"running"`
			Operation *service.Operation `json:"operation"`
		}{true, op})
	}
	wc, cancel := context.WithTimeout(c, min(wait, maxWait))
	defer cancel()
	for ev, err := range s.svc.Events(wc) {
		if err != nil {
			break
		}
		if ev.Type == service.EventFinished && ev.Result != nil {
			return reply(struct {
				Running bool               `json:"running"`
				Result  *service.RunResult `json:"result"`
			}{false, ev.Result})
		}
	}
	// The time is up or the stream broke; the operation may also have ended
	// before the stream started.
	if op, err = s.svc.Operation(c); err != nil {
		return nil, nil, explain(err)
	}
	if op == nil {
		return s.idle(c)
	}
	return reply(struct {
		Running   bool               `json:"running"`
		Operation *service.Operation `json:"operation"`
		Note      string             `json:"note"`
	}{true, op, "the operation did not end within the time asked for; call get_operation again"})
}

// idle reports that nothing runs, with the latest results.
func (s *Server) idle(c context.Context) (*mcp.CallToolResult, any, error) {
	st, err := s.svc.Status(c)
	if err != nil {
		return nil, nil, explain(err)
	}
	return reply(struct {
		Running  bool                               `json:"running"`
		LastRuns map[service.Kind]service.RunResult `json:"last_runs,omitempty"`
		Retry    *service.RetryInfo                 `json:"retry,omitempty"`
	}{false, st.LastRuns, st.Retry})
}

func mode(s string) service.RestoreMode {
	if strings.EqualFold(s, string(service.ModeMerge)) {
		return service.ModeMerge
	}
	return service.ModeExact
}
