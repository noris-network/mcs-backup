package engine

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"github.com/noris-network/mcs-backup/v2/internal/restic"
	"github.com/noris-network/mcs-backup/v2/internal/service"
)

// BackupResult is the outcome of a backup run.
type BackupResult struct {
	Result  service.Result
	Message string
	Error   string
	// Snapshot is the ID of the snapshot the run ended with, empty if there is none.
	Snapshot string
	Summary  restic.BackupSummary
	Warnings []string
	// Suspicious is set if the snapshot was tagged as suspicious.
	Suspicious bool
	Forget     ForgetOutcome
	// Copy is nil if no secondary repository is configured.
	Copy       *PhaseResult
	CopyForget ForgetOutcome
	Duration   time.Duration
}

func (r *BackupResult) warn(format string, args ...any) {
	r.Warnings = append(r.Warnings, fmt.Sprintf(format, args...))
}

// Backup runs a backup: pre-hook, backup, plausibility check, forget, copy and
// post-hook. The post-hook runs even if an earlier phase failed.
func (e *Engine) Backup(ctx context.Context, req service.BackupRequest, sink Sink) BackupResult {
	start := e.now()
	parent := ctx
	ctx, cancel := withTimeout(ctx, e.cfg.Timeouts.Backup)
	defer cancel()

	var res BackupResult
	err := e.backupPhases(ctx, req, sink, &res)
	if err != nil {
		res.Result = classify(ctx, err)
		res.Error = err.Error()
		if res.Message == "" {
			res.Message = "backup failed"
		}
	}
	if err := e.postHook(parent, e.cfg.Hooks.PostBackup, "post-backup", nil, sink, res.Result, res.Error, res.Snapshot); err != nil {
		res.warn("post-backup hook failed: %v", err)
	}

	if len(res.Warnings) > 0 && res.Result == service.ResultSuccess {
		res.Result = service.ResultWarning
	}
	res.Duration = e.now().Sub(start)
	return res
}

func (e *Engine) backupPhases(ctx context.Context, req service.BackupRequest, sink Sink, res *BackupResult) error {
	res.Result = service.ResultSuccess

	sink.phase("prepare")
	if script := e.cfg.Hooks.PreBackup; script != "" {
		if err := e.runHook(ctx, "pre-backup", script, nil, sink); err != nil {
			return fmt.Errorf("pre-backup hook: %w", err)
		}
	}

	sink.phase("backup")
	snaps, err := e.main.Snapshots(ctx)
	if err != nil {
		return fmt.Errorf("list snapshots: %w", err)
	}
	sum, err := e.runBackup(ctx, snaps, req.Origin, req.Note, req.Keep, sink)
	res.Summary = sum
	res.Snapshot = sum.SnapshotID
	switch {
	case errors.Is(err, restic.ErrIncomplete):
		res.warn("%s", unreadableMessage(sum))
	case errors.Is(err, restic.ErrEmptySnapshot):
		// restic stores nothing; the plausibility check treats it like an empty snapshot
		sink.logf("restic did not store a snapshot: the source is empty")
	case err != nil:
		return err
	}

	// plausibility
	own := service.Own(snaps, service.OwnHosts(e.cfg.Host, snaps))
	regular := service.Regular(own)
	if verdict := e.plausibility(sum, regular); verdict != nil {
		return e.afterImplausible(ctx, verdict, res, sink)
	}

	res.Message = fmt.Sprintf("snapshot %s created: %d %s, %s processed", short(sum.SnapshotID),
		sum.TotalFilesProcessed, plural(int(sum.TotalFilesProcessed), "file"), formatBytes(sum.TotalBytesProcessed))

	// forget
	if !res.Suspicious {
		e.forgetPhase(ctx, e.main, "main", e.cfg.AppendOnly, &res.Forget, res, sink)
	}
	// copy
	if e.second != nil {
		e.copyPhase(ctx, res, sink)
	}
	return nil
}

// runBackup takes a snapshot of the data. Snapshots must be the current list
// of the repository (used to find the parent).
func (e *Engine) runBackup(ctx context.Context, snaps []restic.Snapshot, origin service.Origin, note string, keep bool, sink Sink) (restic.BackupSummary, error) {
	if origin == "" {
		origin = service.OriginManual
	}
	tags := []string{service.TagApp, string(origin)}
	if keep {
		tags = append(tags, service.TagKeep)
	}
	if t := service.NoteTag(note); t != "" {
		tags = append(tags, t)
	}
	opts := restic.BackupOptions{
		Host:     e.cfg.Host,
		Tags:     tags,
		Parent:   e.parentSnapshot(snaps),
		Excludes: e.cfg.Excludes,
		Paths:    e.cfg.Paths,
		Extra:    e.cfg.Restic.Backup,
	}
	if e.cfg.PipeMode() {
		opts.Paths = nil
		opts.StdinCommand = []string{e.cfg.Pipe.InScript}
		opts.StdinFilename = e.cfg.Pipe.InFilename
	}
	if opts.Parent != "" {
		sink.logf("using snapshot %s as parent", short(opts.Parent))
	}

	var sum restic.BackupSummary
	err := e.retryLocked(ctx, e.main, e.cfg.AppendOnly, sink, func() error {
		var err error
		sum, err = e.main.Backup(ctx, opts, func(s restic.BackupStatus) {
			p := service.Progress{Percent: s.PercentDone, FilesDone: s.FilesDone, FilesTotal: s.TotalFiles, BytesDone: s.BytesDone, BytesTotal: s.TotalBytes}
			if len(s.CurrentFiles) > 0 {
				p.Current = s.CurrentFiles[0]
			}
			sink.progress(p)
		})
		return err
	})
	if len(sum.CommandOutput) > 0 {
		name := filepath.Base(e.cfg.Pipe.InScript)
		for _, line := range sum.CommandOutput {
			sink.logf("%s: %s", name, line)
		}
	}
	for _, line := range sum.Output {
		sink.logf("restic: %s", line)
	}
	// a file that could not be read is a warning if restic saved the snapshot
	// without it, and the reason why the backup failed if not
	level := "warning"
	if err != nil && !errors.Is(err, restic.ErrIncomplete) {
		level = "error"
	}
	for _, msg := range sum.Errors {
		sink.logf("%s: %s", level, msg)
	}
	if sum.SnapshotID != "" {
		// what restic prints at the end of a backup, for the log
		sink.logf("snapshot %s saved", short(sum.SnapshotID))
		sink.logf("files: %d new, %d changed, %d unmodified; directories: %d new, %d changed, %d unmodified",
			sum.FilesNew, sum.FilesChanged, sum.FilesUnmodified, sum.DirsNew, sum.DirsChanged, sum.DirsUnmodified)
		sink.logf("%s added to the repository (%s stored), %d %s and %s processed in %s",
			formatBytes(sum.DataAdded), formatBytes(sum.DataAddedPacked),
			sum.TotalFilesProcessed, plural(int(sum.TotalFilesProcessed), "file"), formatBytes(sum.TotalBytesProcessed),
			time.Duration(sum.TotalDuration*float64(time.Second)).Round(time.Second))
	}
	return sum, err
}

// snapshotPaths returns the paths a snapshot of the current configuration has.
func (e *Engine) snapshotPaths() []string {
	if e.cfg.PipeMode() {
		return []string{"/" + e.cfg.Pipe.InFilename}
	}
	paths := make([]string, len(e.cfg.Paths))
	for i, p := range e.cfg.Paths {
		if !filepath.IsAbs(p) {
			p = filepath.Join(e.cfg.Root, p)
		}
		paths[i] = filepath.Clean(p)
	}
	return paths
}

// parentSnapshot returns the newest own snapshot with the same paths. restic
// would only find parents of its own host name, which misses the snapshots of
// mcs-backup 1.x and would rescan everything.
func (e *Engine) parentSnapshot(snaps []restic.Snapshot) string {
	want := e.snapshotPaths()
	slices.Sort(want)
	own := service.Own(snaps, service.OwnHosts(e.cfg.Host, snaps))
	for _, s := range slices.Backward(own) {
		got := slices.Clone(s.Paths)
		slices.Sort(got)
		if slices.Equal(got, want) {
			return s.ID
		}
	}
	return ""
}

// implausible is the verdict for a snapshot that must not be trusted.
type implausible struct {
	result  service.Result
	message string
	// remove: the snapshot is not worth keeping (nothing exists to protect).
	remove bool
}

// plausibility checks the new snapshot against the previous regular ones. It
// returns a verdict for implausible snapshots, nil for plausible ones.
func (e *Engine) plausibility(sum restic.BackupSummary, regular []restic.Snapshot) *implausible {
	empty := sum.TotalFilesProcessed == 0
	if e.cfg.PipeMode() {
		empty = sum.TotalBytesProcessed == 0
	}
	if empty && e.cfg.Backup.AllowEmpty && sum.SnapshotID == "" {
		// allowed, but restic refuses to store an empty snapshot
		return &implausible{result: service.ResultSkipped, message: "nothing to back up: the source is empty and restic does not store empty snapshots"}
	}
	if empty && !e.cfg.Backup.AllowEmpty {
		if len(regular) == 0 {
			// first backup of an empty source: nothing to protect, nothing worth keeping
			return &implausible{result: service.ResultSkipped, message: "nothing to back up yet: the source is empty", remove: true}
		}
		return &implausible{
			result:  service.ResultFailure,
			message: fmt.Sprintf("the backup is empty although %d regular snapshots exist", len(regular)),
		}
	}

	if limit := e.cfg.Backup.MaxShrink; limit > 0 && !empty {
		for _, prev := range slices.Backward(regular) {
			if prev.Summary == nil {
				continue // snapshots without statistics (1.x) are no baseline
			}
			base := prev.Summary.TotalBytesProcessed
			if base > 0 && float64(sum.TotalBytesProcessed) < float64(base)*(1-limit/100) {
				return &implausible{
					result: service.ResultSuspicious,
					message: fmt.Sprintf("the backup is %.0f%% smaller than snapshot %s (%s instead of %s)",
						100*(1-float64(sum.TotalBytesProcessed)/float64(base)), short(prev.ID),
						formatBytes(sum.TotalBytesProcessed), formatBytes(base)),
				}
			}
			break
		}
	}
	return nil
}

// afterImplausible handles a snapshot that failed the plausibility check. An
// empty snapshot is tagged suspicious as well, so that "latest" never selects
// it: restoring it would wipe the data.
func (e *Engine) afterImplausible(ctx context.Context, v *implausible, res *BackupResult, sink Sink) error {
	sink.logf("plausibility check: %s", v.message)
	res.Message = v.message

	switch {
	case res.Snapshot == "":
		// restic stored nothing, so there is nothing to remove or tag
	case v.remove && !e.cfg.AppendOnly:
		if rerr := e.main.Remove(ctx, res.Snapshot); rerr != nil {
			res.warn("cannot remove the empty snapshot %s: %v", short(res.Snapshot), rerr)
		} else {
			res.Snapshot = ""
		}
	case !v.remove && !e.cfg.AppendOnly:
		newID, terr := e.addTag(ctx, res.Snapshot, service.TagSuspicious)
		if terr != nil {
			res.warn("cannot tag snapshot %s as suspicious: %v", short(res.Snapshot), terr)
			break
		}
		res.Suspicious = true
		if newID != "" {
			res.Snapshot = newID
		}
	case !v.remove:
		res.Suspicious = true // append-only: reported, but the snapshot cannot be tagged
	}

	res.Result = v.result
	switch v.result {
	case service.ResultSkipped:
		return nil
	case service.ResultFailure:
		// the snapshot is not copied: it is empty
		return errors.New(v.message)
	}
	// suspicious: the data is complete, so keep a second copy of it, but do not forget
	if e.second != nil {
		e.copyPhase(ctx, res, sink)
	}
	return nil
}

func (e *Engine) copyPhase(ctx context.Context, res *BackupResult, sink Sink) {
	sink.phase("copy")
	pr := &PhaseResult{Result: service.ResultSuccess}
	res.Copy = pr
	err := e.retryLocked(ctx, e.second, e.cfg.Copy.AppendOnly, sink, func() error {
		return e.second.Copy(ctx, e.main, restic.CopyOptions{Snapshots: []string{res.Snapshot}}, sink.writer())
	})
	if err != nil {
		pr.Result, pr.Error = classify(ctx, err), err.Error()
		res.warn("copy to the secondary repository failed: %v", err)
		return
	}
	if !res.Suspicious {
		e.forgetPhase(ctx, e.second, "copy", e.cfg.Copy.AppendOnly, &res.CopyForget, res, sink)
	}
}

// postHook runs a post-hook with its own time limit, independent of the
// timeout or cancellation of the operation, and tells it the outcome.
func (e *Engine) postHook(parent context.Context, script, name string, args []string, sink Sink, result service.Result, errMsg, snapshot string) error {
	if script == "" {
		return nil
	}
	sink.phase("wrapup")
	ctx, cancelTimeout := context.WithTimeout(context.WithoutCancel(parent), e.cfg.Timeouts.PostHook)
	defer cancelTimeout()
	ctx, cancel := hardStopContext(parent, ctx)
	defer cancel()
	return e.runHook(ctx, name, script, args, sink,
		"MCS_BACKUP_RESULT="+string(result),
		"MCS_BACKUP_ERROR="+errMsg,
		"MCS_BACKUP_SNAPSHOT="+snapshot,
	)
}

func unreadableMessage(sum restic.BackupSummary) string {
	n := len(sum.Errors)
	if n == 0 {
		return "some source files could not be read"
	}
	more := ""
	if n >= 20 {
		more = " or more"
	}
	return fmt.Sprintf("%d%s source files could not be read, for example: %s", n, more, sum.Errors[0])
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func formatBytes(n int64) string {
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
