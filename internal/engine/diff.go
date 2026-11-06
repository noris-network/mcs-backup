package engine

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/noris-network/mcs-backup/v2/internal/restic"
	"github.com/noris-network/mcs-backup/v2/internal/service"
)

const (
	// diffPaths is how many added and removed paths a comparison lists.
	diffPaths = 50
	// diffCollect bounds the paths that are kept while restic reports them.
	diffCollect = 20000
	// diffChanged is how many changed files a comparison lists.
	diffChanged = 50
)

// Diff tells what changed from one snapshot to another. Without a request it
// compares the newest snapshot, also a suspicious one, with the newest regular
// snapshot before it: what a backup lost that was flagged as much smaller.
func (e *Engine) Diff(ctx context.Context, req service.DiffRequest) (service.DiffResult, error) {
	var res service.DiffResult
	snaps, err := e.main.Snapshots(ctx)
	if err != nil {
		return res, fmt.Errorf("list snapshots: %w", err)
	}
	from, to, err := e.diffPair(snaps, req)
	if err != nil {
		return res, err
	}
	res.From, res.To = service.FromRestic(from), service.FromRestic(to)

	added, removed := &diffSide{}, &diffSide{}
	stats, err := e.main.Diff(ctx, from.ID, to.ID, func(path, modifier string) {
		switch modifier {
		case "+":
			added.add(path)
		case "-":
			removed.add(path)
		case "M", "T":
			if !strings.HasSuffix(path, "/") && len(res.ChangedPaths) < diffChanged {
				res.ChangedPaths = append(res.ChangedPaths, path)
			}
		}
	})
	if err != nil {
		return res, err
	}
	res.Changed = stats.ChangedFiles
	res.Added = added.result(stats.Added)
	res.Removed = removed.result(stats.Removed)
	return res, nil
}

// diffPair picks the two snapshots to compare.
func (e *Engine) diffPair(snaps []restic.Snapshot, req service.DiffRequest) (from, to restic.Snapshot, err error) {
	own := service.Own(snaps, service.OwnHosts(e.cfg.Host, snaps))
	if req.To != "" {
		to, err = e.resolve(snaps, req.To)
	} else {
		backups := slices.DeleteFunc(slices.Clone(own), func(s restic.Snapshot) bool { return s.HasTag(service.TagPreRestore) })
		var ok bool
		if to, ok = service.Latest(backups); !ok {
			err = fmt.Errorf("%w: there is no snapshot yet", ErrNoSnapshot)
		}
	}
	if err != nil {
		return from, to, err
	}
	if req.From != "" {
		from, err = e.resolve(snaps, req.From)
		return from, to, err
	}
	before := slices.DeleteFunc(service.Regular(own), func(s restic.Snapshot) bool { return !s.Time.Before(to.Time) })
	from, ok := service.Latest(before)
	if !ok {
		return from, to, errors.New("there is no regular snapshot before " + short(to.ID) + " to compare it with")
	}
	return from, to, nil
}

// diffSide collects the paths that only one snapshot has. restic reports a
// directory before its content, so everything below the last directory that
// was reported belongs to it.
type diffSide struct {
	paths   []service.DiffPath
	dropped int
}

func (d *diffSide) add(path string) {
	if n := len(d.paths); n > 0 && strings.HasSuffix(d.paths[n-1].Path, "/") && strings.HasPrefix(path, d.paths[n-1].Path) {
		d.paths[n-1].Entries++
		return
	}
	if len(d.paths) == diffCollect {
		d.dropped++
		return
	}
	d.paths = append(d.paths, service.DiffPath{Path: path, Entries: 1})
}

func (d *diffSide) result(c restic.DiffCount) service.DiffSide {
	side := service.DiffSide{Files: c.Files, Dirs: c.Dirs, Bytes: c.Bytes, Paths: d.paths, More: d.dropped}
	slices.SortStableFunc(side.Paths, func(a, b service.DiffPath) int {
		return cmp.Or(cmp.Compare(b.Entries, a.Entries), cmp.Compare(a.Path, b.Path))
	})
	if len(side.Paths) > diffPaths {
		side.More += len(side.Paths) - diffPaths
		side.Paths = side.Paths[:diffPaths]
	}
	return side
}
