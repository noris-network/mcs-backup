package engine

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/noris-network/mcs-backup/v2/internal/restic"
	"github.com/noris-network/mcs-backup/v2/internal/service"
)

const (
	// maxFiles is how many entries of a directory are listed.
	maxFiles = 5000
	// maxVersions is how many file versions a search returns.
	maxVersions = 1000
)

// Files lists a directory (or a single file) in a snapshot, the top of the
// snapshot without a path.
//
// restic stores what was backed up relative to BACKUP_ROOT below "/", so
// BACKUP_ROOT/docs is "/docs" in the snapshot, and paths outside of the root
// under their full name. A relative path therefore counts from the top, and a
// path inside BACKUP_ROOT that the snapshot does not have is tried without the
// root in front: both ways of naming a file work.
func (e *Engine) Files(ctx context.Context, req service.FilesRequest) (service.FileList, error) {
	var list service.FileList
	// a browser goes on with the full ID: restic describes the snapshot when
	// it lists it, which saves asking for all snapshots every time
	snap := restic.Snapshot{ID: req.Snapshot}
	var described func(restic.Snapshot)
	if fullID(req.Snapshot) {
		described = func(s restic.Snapshot) { list.Snapshot = service.FromRestic(s) }
	} else {
		snaps, err := e.main.Snapshots(ctx)
		if err != nil {
			return list, fmt.Errorf("list snapshots: %w", err)
		}
		if snap, err = e.resolve(snaps, cmp.Or(req.Snapshot, "latest")); err != nil {
			return list, err
		}
	}
	list.Snapshot = service.FromRestic(snap)

	candidates := e.pathsInSnapshot(req.Path)
	for _, p := range candidates {
		found, err := e.listDir(ctx, snap.ID, p, &list, described)
		if err != nil {
			return list, err
		}
		if found || p == "/" {
			list.Path = p
			return list, nil
		}
	}
	return list, fmt.Errorf("snapshot %s has no %s", short(snap.ID), candidates[0])
}

// ReadFile returns the start of a file in a snapshot: at most
// service.MaxFileText bytes, and no text for a binary file.
func (e *Engine) ReadFile(ctx context.Context, req service.FileRequest) (service.FileContent, error) {
	var c service.FileContent
	if req.Path == "" {
		return c, errors.New("which file? the path is empty")
	}
	snap := restic.Snapshot{ID: req.Snapshot}
	if !fullID(req.Snapshot) {
		snaps, err := e.main.Snapshots(ctx)
		if err != nil {
			return c, fmt.Errorf("list snapshots: %w", err)
		}
		if snap, err = e.resolve(snaps, cmp.Or(req.Snapshot, "latest")); err != nil {
			return c, err
		}
	}
	c.Snapshot, c.Path = snap.ID, path.Join("/", filepath.ToSlash(req.Path))

	// restic stops when the writer is full: the rest of the file is not needed
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	w := &capWriter{max: service.MaxFileText, full: cancel}
	if err := e.main.Peek(ctx, snap.ID, c.Path, w); err != nil && !w.truncated {
		return c, err
	}
	data := w.buf
	c.Truncated = w.truncated
	text := data
	if c.Truncated { // the cut may have split a character
		for i := 0; i < utf8.UTFMax && len(text) > 0 && !utf8.Valid(text); i++ {
			text = text[:len(text)-1]
		}
	}
	if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(text) {
		c.Binary = true
		return c, nil
	}
	c.Text = string(text)
	return c, nil
}

// fullID reports whether ref is the full ID of a snapshot, which needs no
// lookup in the list of snapshots.
func fullID(ref string) bool {
	if len(ref) != 64 {
		return false
	}
	return strings.Trim(ref, "0123456789abcdef") == ""
}

// capWriter keeps the first max bytes written to it and calls full when
// more come.
type capWriter struct {
	max       int
	buf       []byte
	truncated bool
	full      func()
}

func (w *capWriter) Write(p []byte) (int, error) {
	if room := w.max - len(w.buf); len(p) > room {
		w.buf = append(w.buf, p[:room]...)
		w.truncated = true
		w.full()
		return room, errFull
	}
	w.buf = append(w.buf, p...)
	return len(p), nil
}

var errFull = errors.New("enough of the file was read")

// listDir reads the entries of dir into list. It reports whether the snapshot
// has dir at all. described, if set, gets the snapshot as restic describes it.
func (e *Engine) listDir(ctx context.Context, snapshot, dir string, list *service.FileList, described func(restic.Snapshot)) (found bool, err error) {
	for n, err := range e.main.LsSnapshot(ctx, described, snapshot, dir) {
		if err != nil {
			return found, err
		}
		found = true
		if n.Type == "dir" && n.Path == dir {
			continue // the directory itself
		}
		if len(list.Files) == maxFiles {
			list.Truncated = true
			break
		}
		list.Files = append(list.Files, service.File{Path: n.Path, Type: n.Type, Size: n.Size, Mtime: n.Mtime})
	}
	return found, nil
}

// pathsInSnapshot returns the paths in a snapshot that a path given by a user
// can mean, the likeliest first.
func (e *Engine) pathsInSnapshot(p string) []string {
	p = path.Join("/", filepath.ToSlash(p))
	paths := []string{p}
	if rel, err := filepath.Rel(e.cfg.Root, filepath.FromSlash(p)); err == nil && filepath.IsLocal(rel) && !e.cfg.PipeMode() {
		paths = append(paths, path.Join("/", filepath.ToSlash(rel)))
	}
	return paths
}

// Find searches the snapshots of this instance for files whose name or path
// matches a glob pattern. A file that did not change between snapshots is one
// version: what is needed to pick the snapshot to restore it from.
func (e *Engine) Find(ctx context.Context, req service.FindRequest) (service.FindResult, error) {
	var res service.FindResult
	if req.Pattern == "" {
		return res, errors.New("a search needs a pattern, such as a file name or *.sql")
	}
	snaps, err := e.main.Snapshots(ctx)
	if err != nil {
		return res, fmt.Errorf("list snapshots: %w", err)
	}
	hosts := service.OwnHosts(e.cfg.Host, snaps)
	opts := restic.FindOptions{Hosts: hosts, IgnoreCase: req.IgnoreCase}
	res.Searched = len(service.Own(snaps, hosts))
	if req.Snapshot != "" {
		snap, err := e.resolve(snaps, req.Snapshot)
		if err != nil {
			return res, err
		}
		opts.Snapshots, res.Searched = []string{snap.ID}, 1
	}
	if res.Searched == 0 {
		return res, nil
	}

	groups, err := e.main.Find(ctx, req.Pattern, opts)
	if errors.Is(err, restic.ErrTooManyMatches) {
		return res, errors.New("the pattern matches too many files: narrow it or search a single snapshot")
	}
	if err != nil {
		return res, err
	}
	res.Versions = fileVersions(groups, snaps)
	if len(res.Versions) > maxVersions {
		res.Versions, res.Truncated = res.Versions[:maxVersions], true
	}
	return res, nil
}

// fileVersions merges the matches of all snapshots into the versions of every
// path. Two matches are the same version if type, size and modification time
// agree.
func fileVersions(groups []restic.FindGroup, snaps []restic.Snapshot) []service.FileVersion {
	times := make(map[string]service.SnapshotRef, len(snaps))
	for _, s := range snaps {
		times[s.ID] = service.SnapshotRef{ID: s.ID, Time: s.Time}
	}
	type key struct {
		path, typ string
		size      int64
		mtime     int64
	}
	byKey := map[key]*service.FileVersion{}
	for _, g := range groups {
		ref, ok := times[g.Snapshot]
		if !ok {
			continue // a snapshot that appeared after the list was read
		}
		for _, m := range g.Matches {
			k := key{m.Path, m.Type, m.Size, m.Mtime.UnixNano()}
			v := byKey[k]
			if v == nil {
				v = &service.FileVersion{Path: m.Path, Type: m.Type, Size: m.Size, Mtime: m.Mtime, Newest: ref, Oldest: ref}
				byKey[k] = v
			}
			v.Snapshots++
			if ref.Time.After(v.Newest.Time) {
				v.Newest = ref
			}
			if ref.Time.Before(v.Oldest.Time) {
				v.Oldest = ref
			}
		}
	}
	out := make([]service.FileVersion, 0, len(byKey))
	for _, v := range byKey {
		out = append(out, *v)
	}
	slices.SortFunc(out, func(a, b service.FileVersion) int {
		return cmp.Or(cmp.Compare(a.Path, b.Path), b.Newest.Time.Compare(a.Newest.Time), b.Mtime.Compare(a.Mtime))
	})
	return out
}
