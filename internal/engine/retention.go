package engine

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/noris-network/mcs-backup/v2/internal/restic"
	"github.com/noris-network/mcs-backup/v2/internal/service"
)

// ForgetOutcome describes what forget did, or would do.
type ForgetOutcome struct {
	// Ran is set if snapshots were forgotten.
	Ran bool `json:"ran"`
	// Held is set if forget was held back by the retention guard.
	Held bool `json:"held"`
	// Total is the number of snapshots of this instance.
	Total int `json:"total"`
	// Removed is the number of snapshots forgotten, or, if Held, that would be.
	Removed int    `json:"removed"`
	Error   string `json:"error,omitempty"`
}

// guardTrips reports whether forgetting remove of total snapshots is so
// drastic that it needs approval: more than two snapshots and more than
// maxPercent of all.
func guardTrips(total, remove int, maxPercent float64) bool {
	return remove > 2 && float64(remove)*100 > maxPercent*float64(total)
}

func countGroups(groups []restic.ForgetGroup) (total, remove int) {
	for _, g := range groups {
		total += len(g.Keep) + len(g.Remove)
		remove += len(g.Remove)
	}
	return total, remove
}

func (e *Engine) forgetOptions(snaps []restic.Snapshot) restic.ForgetOptions {
	return restic.ForgetOptions{
		Hosts:    service.OwnHosts(e.cfg.Host, snaps),
		KeepTags: []string{service.TagKeep},
		Policy:   e.cfg.Retention.Args(),
		Extra:    e.cfg.Restic.Forget,
	}
}

// forget applies the retention policy to the snapshots of this instance. Unless
// force is set, it first asks restic what it would delete and holds back
// drastic deletions.
func (e *Engine) forget(ctx context.Context, repo *restic.Repo, appendOnly bool, force bool) (ForgetOutcome, error) {
	var out ForgetOutcome
	if appendOnly {
		return out, ErrAppendOnly
	}
	snaps, err := repo.Snapshots(ctx)
	if err != nil {
		return out, fmt.Errorf("list snapshots: %w", err)
	}
	opts := e.forgetOptions(snaps)

	if !force {
		dry := opts
		dry.DryRun = true
		groups, err := repo.Forget(ctx, dry)
		if err != nil {
			return out, err
		}
		out.Total, out.Removed = countGroups(groups)
		if guardTrips(out.Total, out.Removed, e.cfg.Backup.ForgetMaxDelete) {
			out.Held = true
			return out, nil
		}
		if out.Removed == 0 {
			return out, nil
		}
	}

	var groups []restic.ForgetGroup
	err = e.retryLocked(ctx, repo, appendOnly, Sink{}, func() error {
		var err error
		groups, err = repo.Forget(ctx, opts)
		return err
	})
	if err != nil {
		return out, err
	}
	out.Total, out.Removed = countGroups(groups)
	out.Ran = true
	return out, nil
}

// forgetPhase is the forget step of a backup. A failure only degrades the
// result to a warning: the backup itself is safe.
func (e *Engine) forgetPhase(ctx context.Context, repo *restic.Repo, name string, appendOnly bool, out *ForgetOutcome, res *BackupResult, sink Sink) {
	if appendOnly {
		return
	}
	sink.phase("forget")
	o, err := e.forget(ctx, repo, appendOnly, false)
	*out = o
	switch {
	case err != nil:
		out.Error = err.Error()
		res.warn("forget on the %s repository failed: %v", name, err)
	case o.Held:
		res.warn("forget on the %s repository is held back: it would remove %d of %d snapshots, approve it if that is intended",
			name, o.Removed, o.Total)
		sink.logf("retention guard: holding back forget on the %s repository (%d of %d snapshots)", name, o.Removed, o.Total)
	case o.Ran:
		sink.logf("forget on the %s repository removed %d snapshots", name, o.Removed)
	}
}

// ApproveResult is the outcome of an approved forget.
type ApproveResult struct {
	Result service.Result
	Main   ForgetOutcome
	Second *ForgetOutcome
	Error  string
}

// ApproveForget applies the retention policy without the retention guard.
func (e *Engine) ApproveForget(ctx context.Context, sink Sink) ApproveResult {
	sink.phase("forget")
	var res ApproveResult
	if e.cfg.AppendOnly {
		res.Result, res.Error = service.ResultFailure, ErrAppendOnly.Error()
		return res
	}
	var errs []error
	var err error
	if res.Main, err = e.forget(ctx, e.main, e.cfg.AppendOnly, true); err != nil {
		res.Main.Error = err.Error()
		errs = append(errs, fmt.Errorf("main repository: %w", err))
	}
	if e.second != nil && !e.cfg.Copy.AppendOnly {
		var o ForgetOutcome
		if o, err = e.forget(ctx, e.second, false, true); err != nil {
			o.Error = err.Error()
			errs = append(errs, fmt.Errorf("secondary repository: %w", err))
		}
		res.Second = &o
	}
	res.Result = classify(ctx, errors.Join(errs...))
	if len(errs) > 0 {
		res.Error = errors.Join(errs...).Error()
	}
	return res
}

// RetentionInfo tells what the retention policy would do right now.
type RetentionInfo struct {
	// Policy describes the policy in plain words.
	Policy string
	// Hosts are the restic host names whose snapshots the policy applies to.
	Hosts []string
	// Foreign is the number of snapshots of other hosts, which are never touched.
	Foreign int
	// Total is the number of snapshots of this instance.
	Total int
	// Remove lists the snapshots the policy would forget.
	Remove []service.Snapshot
	// Held is set if the retention guard would hold back this forget.
	Held bool
	// AppendOnly: nothing is ever forgotten.
	AppendOnly bool
}

// Retention previews the retention policy on the main repository.
func (e *Engine) Retention(ctx context.Context) (RetentionInfo, error) {
	info := RetentionInfo{Policy: e.cfg.Retention.Describe(), AppendOnly: e.cfg.AppendOnly}
	snaps, err := e.main.Snapshots(ctx)
	if err != nil {
		return info, err
	}
	info.Hosts = service.OwnHosts(e.cfg.Host, snaps)
	info.Foreign = len(snaps) - len(service.Own(snaps, info.Hosts))
	if e.cfg.AppendOnly {
		info.Total = len(service.Own(snaps, info.Hosts))
		return info, nil
	}

	opts := e.forgetOptions(snaps)
	opts.DryRun = true
	groups, err := e.main.Forget(ctx, opts)
	if err != nil {
		return info, err
	}
	var remove int
	info.Total, remove = countGroups(groups)
	for _, g := range groups {
		for _, s := range g.Remove {
			info.Remove = append(info.Remove, service.FromRestic(s))
		}
	}
	slices.SortFunc(info.Remove, func(a, b service.Snapshot) int { return a.Time.Compare(b.Time) })
	info.Held = guardTrips(info.Total, remove, e.cfg.Backup.ForgetMaxDelete)
	return info, nil
}
