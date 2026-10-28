package engine

import (
	"context"
	"fmt"
	"slices"

	"github.com/noris-network/mcs-backup/v2/internal/restic"
	"github.com/noris-network/mcs-backup/v2/internal/service"
)

// resolve finds the snapshot ref refers to: "latest" (the newest regular
// snapshot of this instance), a full ID or a unique ID prefix.
func (e *Engine) resolve(snaps []restic.Snapshot, ref string) (restic.Snapshot, error) {
	if ref == "latest" {
		own := service.Own(snaps, service.OwnHosts(e.cfg.Host, snaps))
		if s, ok := service.Latest(service.Regular(own)); ok {
			return s, nil
		}
		return restic.Snapshot{}, fmt.Errorf("%w: there is no regular snapshot yet", ErrNoSnapshot)
	}
	var found []restic.Snapshot
	for _, s := range snaps {
		if ref != "" && len(ref) <= len(s.ID) && s.ID[:len(ref)] == ref {
			if len(ref) == len(s.ID) {
				return s, nil
			}
			found = append(found, s)
		}
	}
	switch len(found) {
	case 0:
		return restic.Snapshot{}, fmt.Errorf("%w: %q", ErrNoSnapshot, ref)
	case 1:
		return found[0], nil
	}
	return restic.Snapshot{}, fmt.Errorf("%w: %q is ambiguous", ErrNoSnapshot, ref)
}

// rewriteTags changes the tags of a snapshot. restic rewrites the snapshot,
// which gives it a new ID; this is returned (empty if it cannot be determined).
func (e *Engine) rewriteTags(ctx context.Context, repo *restic.Repo, appendOnly bool, id string, add, remove []string) (string, error) {
	before, err := repo.Snapshots(ctx)
	if err != nil {
		return "", err
	}
	old, err := e.resolve(before, id)
	if err != nil {
		return "", err
	}
	var newID string
	err = e.retryLocked(ctx, repo, appendOnly, Sink{}, func() error {
		var err error
		newID, err = repo.Tag(ctx, old.ID, add, remove)
		return err
	})
	if err != nil || newID != "" {
		return newID, err
	}
	// restic 0.17 does not report the new ID: find the rewritten snapshot
	after, err := repo.Snapshots(ctx)
	if err != nil {
		return "", nil //nolint:nilerr // the tag change succeeded, only the new ID is unknown
	}
	for _, s := range after {
		if s.ID != old.ID && s.Tree == old.Tree && s.Time.Equal(old.Time) && s.Hostname == old.Hostname && slices.Equal(s.Paths, old.Paths) {
			return s.ID, nil
		}
	}
	return "", nil
}

// addTag adds tags to a snapshot of the main repository.
func (e *Engine) addTag(ctx context.Context, id string, tags ...string) (string, error) {
	return e.rewriteTags(ctx, e.main, e.cfg.AppendOnly, id, tags, nil)
}

// Keep protects a snapshot from forget.
func (e *Engine) Keep(ctx context.Context, ref string) (service.TagResult, error) {
	return e.setTags(ctx, ref, []string{service.TagKeep}, nil)
}

// Unkeep removes the protection.
func (e *Engine) Unkeep(ctx context.Context, ref string) (service.TagResult, error) {
	return e.setTags(ctx, ref, nil, []string{service.TagKeep})
}

// Acknowledge accepts a snapshot that was tagged suspicious: it becomes a
// regular snapshot and the baseline for later comparisons.
func (e *Engine) Acknowledge(ctx context.Context, ref string) (service.TagResult, error) {
	return e.setTags(ctx, ref, nil, []string{service.TagSuspicious})
}

// setTags changes tags on the main repository and mirrors the change to the
// secondary one first. A change that is only made on the main repository would
// make the next copy duplicate the snapshot there.
func (e *Engine) setTags(ctx context.Context, ref string, add, remove []string) (service.TagResult, error) {
	var res service.TagResult
	if e.cfg.AppendOnly {
		return res, ErrAppendOnly
	}
	snaps, err := e.main.Snapshots(ctx)
	if err != nil {
		return res, err
	}
	snap, err := e.resolve(snaps, ref)
	if err != nil {
		return res, err
	}

	if e.second != nil {
		if e.cfg.Copy.AppendOnly {
			res.Warnings = append(res.Warnings, "the secondary repository is append-only: the tag change is not mirrored there")
		} else if err := e.mirrorTags(ctx, snap, add, remove); err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("the tag change could not be mirrored to the secondary repository: %v", err))
		}
	}
	res.ID, err = e.rewriteTags(ctx, e.main, false, snap.ID, add, remove)
	return res, err
}

// mirrorTags applies a tag change to the copy of snap in the secondary
// repository, if there is one.
func (e *Engine) mirrorTags(ctx context.Context, snap restic.Snapshot, add, remove []string) error {
	others, err := e.second.Snapshots(ctx)
	if err != nil {
		return err
	}
	// a copy refers to the snapshot it was copied from, or to that one's origin
	for _, o := range others {
		if o.ID == snap.ID || o.Original != "" && (o.Original == snap.ID || o.Original == snap.Original) {
			_, err := e.rewriteTags(ctx, e.second, false, o.ID, add, remove)
			return err
		}
	}
	return nil // not copied (yet)
}
