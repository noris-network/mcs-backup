package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/noris-network/mcs-backup/v2/internal/restic"
	"github.com/noris-network/mcs-backup/v2/internal/service"
)

// spaceReserve is the extra space required on top of the restore size.
const spaceReserve = 1.1

type restorePlan struct {
	service.RestorePreflight
	snap     restic.Snapshot
	includes []string
	pipeFile string
}

// RestorePreflight checks a restore without performing it.
func (e *Engine) RestorePreflight(ctx context.Context, req service.RestoreRequest) (service.RestorePreflight, error) {
	plan, err := e.planRestore(ctx, req)
	return plan.RestorePreflight, err
}

// RestoreNeedsConfirm reports whether req replaces the live data and therefore
// has to be confirmed. It needs no access to the repository, so callers can
// refuse an unconfirmed restore before anything starts.
func (e *Engine) RestoreNeedsConfirm(req service.RestoreRequest) bool {
	if e.cfg.PipeMode() || req.Target == "" {
		return true
	}
	target, err := filepath.Abs(req.Target)
	return err != nil || filepath.Clean(target) == filepath.Clean(e.cfg.Root)
}

func (e *Engine) planRestore(ctx context.Context, req service.RestoreRequest) (restorePlan, error) {
	var p restorePlan
	snaps, err := e.main.Snapshots(ctx)
	if err != nil {
		return p, fmt.Errorf("list snapshots: %w", err)
	}
	if p.snap, err = e.resolve(snaps, req.Snapshot); err != nil {
		return p, err
	}
	p.Snapshot = service.FromRestic(p.snap)
	p.includes = req.Includes
	p.Includes = req.Includes
	p.PipeMode = e.cfg.PipeMode()
	p.Target = e.cfg.Root
	if req.Target != "" {
		if p.Target, err = filepath.Abs(req.Target); err != nil {
			return p, err
		}
	}
	p.InPlace = filepath.Clean(p.Target) == filepath.Clean(e.cfg.Root)

	if p.PipeMode {
		// the data goes into the application, not into a directory
		p.InPlace, p.Target = true, ""
		p.Mode = service.ModeExact
		if len(req.Includes) > 0 || req.Target != "" {
			return p, errors.New("a restore in pipe mode streams the whole snapshot: no target or include paths")
		}
		if req.Preview {
			p.Warnings = append(p.Warnings, "a restore in pipe mode cannot be previewed: the data goes to the restore script")
		}
		p.pipeFile = "/" + e.cfg.Pipe.InFilename
		if len(p.snap.Paths) == 1 {
			p.pipeFile = p.snap.Paths[0]
		}
		p.SafetySnapshot = !req.NoSafetySnapshot
		p.Hooks = e.restoreHooks()
		return p, nil
	}

	if p.TargetEmpty, err = isEmptyDir(p.Target); err != nil {
		return p, fmt.Errorf("check target: %w", err)
	}
	p.Mode = service.ModeMerge
	if p.InPlace {
		p.Mode = service.ModeExact
		if req.Mode != "" {
			p.Mode = req.Mode
		}
	} else if !p.TargetEmpty {
		p.Warnings = append(p.Warnings, fmt.Sprintf("the target %s is not empty: the snapshot is merged into it", p.Target))
	}
	if p.Mode != service.ModeExact && p.Mode != service.ModeMerge {
		return p, fmt.Errorf("unknown restore mode %q", p.Mode)
	}
	p.SafetySnapshot = p.InPlace && !p.TargetEmpty && !req.NoSafetySnapshot
	p.Hooks = e.restoreHooks()

	e.checkSpace(ctx, &p)
	if req.Preview {
		e.previewChanges(ctx, &p)
	}
	return p, nil
}

// previewPaths is how many overwritten and deleted paths a preview lists.
const previewPaths = 20

// previewChanges works out what the restore would do to the target. It only
// ever warns.
func (e *Engine) previewChanges(ctx context.Context, p *restorePlan) {
	var c service.RestoreChanges
	opts := e.restoreOptions(*p)
	opts.DryRun = true
	opts.OnItem = func(action, item string) {
		switch action {
		case "restored":
			// restic also reports a directory that is already there as
			// restored: only its metadata is set again
			if _, err := os.Lstat(filepath.Join(p.Target, filepath.FromSlash(item))); err == nil {
				c.Unchanged++
				return
			}
			c.Created++
		case "updated":
			if c.Overwritten++; len(c.OverwrittenPaths) < previewPaths {
				c.OverwrittenPaths = append(c.OverwrittenPaths, item)
			}
		case "deleted":
			if c.Deleted++; len(c.DeletedPaths) < previewPaths {
				c.DeletedPaths = append(c.DeletedPaths, inTarget(p.Target, item))
			}
		case "unchanged":
			c.Unchanged++
		}
	}
	if _, err := e.main.Restore(ctx, opts, nil); err != nil {
		p.Warnings = append(p.Warnings, fmt.Sprintf("cannot work out what the restore would change: %v", err))
		return
	}
	p.Changes = &c
}

// restoredMessage says what a restore did, and where to: the target is the
// first thing to check after a restore into a directory.
func restoredMessage(plan restorePlan, sum restic.RestoreSummary) string {
	msg := "snapshot " + short(plan.snap.ID) + " restored"
	switch {
	case plan.PipeMode:
		return msg + " through the restore script"
	case plan.InPlace:
		msg += " over the live data in " + plan.Target
	default:
		msg += " to " + plan.Target
	}
	if sum.TotalFiles > 0 {
		msg += fmt.Sprintf(": %d %s, %s", sum.FilesRestored, plural(int(sum.FilesRestored), "file"), formatBytes(sum.BytesRestored))
		if sum.FilesSkipped > 0 {
			msg += fmt.Sprintf(", %d unchanged", sum.FilesSkipped)
		}
	}
	return msg
}

// inTarget turns a path that restic reports in the target into the path in
// the snapshot, the form it reports the other changes in: /data/files/a.txt
// with the target /data/files is /a.txt.
func inTarget(target, path string) string {
	rel, err := filepath.Rel(target, filepath.FromSlash(path))
	if err != nil || !filepath.IsLocal(rel) {
		return path
	}
	return "/" + filepath.ToSlash(rel)
}

func (e *Engine) restoreOptions(plan restorePlan) restic.RestoreOptions {
	return restic.RestoreOptions{
		Snapshot: plan.snap.ID,
		Target:   plan.Target,
		Includes: plan.includes,
		Delete:   plan.InPlace && plan.Mode == service.ModeExact,
		Extra:    e.cfg.Restic.Restore,
	}
}

func (e *Engine) restoreHooks() []string {
	var hooks []string
	if e.cfg.Hooks.PreRestore != "" {
		hooks = append(hooks, "pre-restore")
	}
	if e.cfg.Hooks.PostRestore != "" {
		hooks = append(hooks, "post-restore")
	}
	return hooks
}

// checkSpace estimates the space a restore needs. It only ever warns.
func (e *Engine) checkSpace(ctx context.Context, p *restorePlan) {
	size, err := e.restoreSize(ctx, p.snap, p.includes)
	if err != nil {
		p.Warnings = append(p.Warnings, fmt.Sprintf("cannot determine the size of the restore: %v", err))
		return
	}
	need := size
	if p.InPlace && p.Mode == service.ModeExact {
		// files that are replaced do not need extra space
		var current int64
		if len(p.includes) == 0 {
			current, _ = dirSize(ctx, p.Target)
		}
		for _, inc := range p.includes {
			// an include that leads out of the target replaces nothing there
			rel := strings.TrimLeft(filepath.FromSlash(inc), string(filepath.Separator))
			if !filepath.IsLocal(rel) {
				continue
			}
			n, _ := dirSize(ctx, filepath.Join(p.Target, rel))
			current += n
		}
		need = max(0, size-current)
	}
	p.NeedBytes = int64(float64(need) * spaceReserve)
	free, err := freeBytes(p.Target)
	if err != nil {
		p.Warnings = append(p.Warnings, fmt.Sprintf("cannot determine the free space: %v", err))
		return
	}
	p.FreeBytes = free
	if p.NeedBytes > free {
		p.SpaceWarning = true
		p.Warnings = append(p.Warnings, fmt.Sprintf("the restore needs about %s more space but only %s are free",
			formatBytes(p.NeedBytes), formatBytes(free)))
	}
}

// restoreSize returns the size of the data a restore of snap (or the include
// paths of it) writes.
func (e *Engine) restoreSize(ctx context.Context, snap restic.Snapshot, includes []string) (int64, error) {
	if len(includes) == 0 {
		st, err := e.main.Stats(ctx, restic.StatsRestoreSize, snap.ID)
		return st.TotalSize, err
	}
	var total int64
	for n, err := range e.main.Ls(ctx, snap.ID, includes...) {
		if err != nil {
			return 0, err
		}
		if n.Type == "file" {
			total += n.Size
		}
	}
	return total, nil
}

// RestoreResult is the outcome of a restore.
type RestoreResult struct {
	Result  service.Result
	Message string
	Error   string
	// Snapshot is the ID of the restored snapshot.
	Snapshot string
	// SafetySnapshot is the ID of the snapshot taken before an in-place restore.
	SafetySnapshot string
	Summary        restic.RestoreSummary
	Warnings       []string
	Duration       time.Duration
}

// Restore restores a snapshot. An in-place restore needs Confirm to equal
// Snapshot and takes a safety snapshot first (unless NoSafetySnapshot is set).
func (e *Engine) Restore(ctx context.Context, req service.RestoreRequest, sink Sink) RestoreResult {
	return e.restore(ctx, req, sink, false)
}

// restore does the work. trusted skips the confirmation (restore-if-empty only
// restores into an empty target).
func (e *Engine) restore(ctx context.Context, req service.RestoreRequest, sink Sink, trusted bool) RestoreResult {
	start := e.now()
	parent := ctx
	ctx, cancel := withTimeout(ctx, e.cfg.Timeouts.Restore)
	defer cancel()

	req.Preview = false // a preview belongs to the preflight, not to the restore itself
	var res RestoreResult
	began, err := e.restorePhases(ctx, req, sink, trusted, &res)
	if err != nil {
		res.Result = classify(ctx, err)
		res.Error = err.Error()
		res.Message = "restore failed"
	} else {
		res.Result = service.ResultSuccess
		if len(res.Warnings) > 0 {
			res.Result = service.ResultWarning
		}
	}
	// The post-hook runs whenever the pre-hook was due, also after a failure:
	// it may have to undo what the pre-hook did, such as a maintenance mode.
	if began {
		herr := e.postHook(parent, e.cfg.Hooks.PostRestore, "post-restore", req.HookArgs, sink, res.Result, res.Error, res.Snapshot)
		switch {
		case herr != nil && res.Result.OK():
			res.Result = service.ResultFailure
			res.Error = fmt.Sprintf("post-restore hook: %v", herr)
			res.Message = "restore failed"
		case herr != nil:
			res.Warnings = append(res.Warnings, fmt.Sprintf("post-restore hook failed: %v", herr))
		}
	}
	res.Duration = e.now().Sub(start)
	return res
}

// restorePhases does everything but the post-hook. began reports whether the
// restore got past its checks, to the point where the pre-hook runs.
func (e *Engine) restorePhases(ctx context.Context, req service.RestoreRequest, sink Sink, trusted bool, res *RestoreResult) (began bool, err error) {
	sink.phase("prepare")
	plan, err := e.planRestore(ctx, req)
	if err != nil {
		return false, err
	}
	res.Snapshot = plan.snap.ID
	res.Warnings = append(res.Warnings, plan.Warnings...)

	if plan.InPlace && !trusted && (req.Confirm == "" || req.Confirm != req.Snapshot) {
		return false, fmt.Errorf("%w: an in-place restore replaces the current data, confirm with %q", ErrConfirm, req.Snapshot)
	}
	if plan.SpaceWarning && !req.IgnoreSpaceWarning {
		return false, fmt.Errorf("not enough free space: %s needed, %s free (override to restore anyway)",
			formatBytes(plan.NeedBytes), formatBytes(plan.FreeBytes))
	}

	if script := e.cfg.Hooks.PreRestore; script != "" {
		if err := e.runHook(ctx, "pre-restore", script, req.HookArgs, sink); err != nil {
			return true, fmt.Errorf("pre-restore hook: %w", err)
		}
	}

	if plan.SafetySnapshot {
		sink.phase("safety snapshot")
		sink.logf("taking a snapshot of the current data before it is replaced")
		snaps, err := e.main.Snapshots(ctx)
		if err != nil {
			return true, fmt.Errorf("list snapshots: %w", err)
		}
		sum, err := e.runBackup(ctx, snaps, service.OriginPreRestore, "before restore of "+short(plan.snap.ID), false, sink)
		if err != nil && !errors.Is(err, restic.ErrIncomplete) {
			return true, fmt.Errorf("safety snapshot failed, nothing was restored (restore without a safety snapshot to continue): %w", err)
		}
		res.SafetySnapshot = sum.SnapshotID
		sink.logf("safety snapshot %s taken", short(sum.SnapshotID))
	}

	sink.phase("restore")
	if plan.PipeMode {
		err = e.restorePipe(ctx, plan, sink)
	} else {
		err = e.restoreFiles(ctx, plan, sink, res)
	}
	if err != nil {
		return true, err
	}
	res.Message = restoredMessage(plan, res.Summary)
	return true, nil
}

func (e *Engine) restoreFiles(ctx context.Context, plan restorePlan, sink Sink, res *RestoreResult) error {
	opts := e.restoreOptions(plan)
	return e.retryLocked(ctx, e.main, e.cfg.AppendOnly, sink, func() error {
		sum, err := e.main.Restore(ctx, opts, func(s restic.RestoreStatus) {
			sink.progress(service.Progress{Percent: s.PercentDone, FilesDone: s.FilesRestored, FilesTotal: s.TotalFiles, BytesDone: s.BytesRestored, BytesTotal: s.TotalBytes})
		})
		res.Summary = sum
		return err
	})
}

// restorePipe streams the snapshot into PIPE_OUT_SCRIPT.
func (e *Engine) restorePipe(ctx context.Context, plan restorePlan, sink Sink) error {
	r := *e.hooks
	r.Output = func(_, line string) { sink.logf("pipe-out: %s", line) }
	return e.retryLocked(ctx, e.main, e.cfg.AppendOnly, sink, func() error {
		return r.Pipe(ctx, "pipe-out", e.cfg.Pipe.OutScript, nil, func(w io.Writer) error {
			return e.main.Dump(ctx, plan.snap.ID, plan.pipeFile, w)
		})
	})
}

// RestoreIfEmptyAction says what restore-if-empty did.
type RestoreIfEmptyAction string

const (
	ActionRestored       RestoreIfEmptyAction = "restored"
	ActionTargetNotEmpty RestoreIfEmptyAction = "target-not-empty"
	ActionNoRepository   RestoreIfEmptyAction = "no-repository"
	ActionNoSnapshot     RestoreIfEmptyAction = "no-snapshot"
)

// RestoreIfEmpty restores the latest snapshot into BACKUP_ROOT if that is
// empty, for disaster recovery before an application starts. A target with
// data is left alone without even contacting the repository, and a repository
// is never created. It does not need the daemon.
func (e *Engine) RestoreIfEmpty(ctx context.Context, sink Sink) (RestoreIfEmptyAction, error) {
	if e.cfg.PipeMode() {
		return "", ErrPipeMode
	}
	empty, err := isEmptyDir(e.cfg.Root)
	if err != nil {
		return "", fmt.Errorf("check %s: %w", e.cfg.Root, err)
	}
	if !empty {
		return ActionTargetNotEmpty, nil
	}

	snaps, err := e.main.Snapshots(ctx)
	switch {
	case errors.Is(err, restic.ErrRepoMissing):
		return ActionNoRepository, nil
	case err != nil:
		return "", fmt.Errorf("read the repository: %w", err)
	}
	own := service.Own(snaps, service.OwnHosts(e.cfg.Host, snaps))
	if _, ok := service.Latest(service.Regular(own)); !ok {
		return ActionNoSnapshot, nil
	}

	res := e.restore(ctx, service.RestoreRequest{Snapshot: "latest", Mode: service.ModeMerge, IgnoreSpaceWarning: true, NoSafetySnapshot: true}, sink, true)
	if !res.Result.OK() {
		return "", errors.New(res.Error)
	}
	return ActionRestored, nil
}
