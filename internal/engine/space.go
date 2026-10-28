package engine

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
)

// dirSize returns the total size of the regular files below root. Entries that
// cannot be read are skipped: the result is an estimate.
func dirSize(ctx context.Context, root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	if os.IsNotExist(err) {
		return 0, nil
	}
	return total, err
}

// ignoredEntries are directory entries that do not make a directory "not empty":
// file systems and Finder create them on their own.
func ignoredEntries() []string { return []string{"lost+found", ".DS_Store"} }

// isEmptyDir reports whether path has no entries besides ignoredEntries. A
// path that does not exist is empty.
func isEmptyDir(path string) (bool, error) {
	entries, err := os.ReadDir(path)
	if os.IsNotExist(err) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		if !slices.Contains(ignoredEntries(), e.Name()) {
			return false, nil
		}
	}
	return true, nil
}
