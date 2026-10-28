//go:build unix

package engine

import (
	"math"
	"os"
	"path/filepath"
	"syscall"
)

// freeBytes returns the space available to unprivileged users on the file
// system that holds path, or would hold it if it does not exist yet.
func freeBytes(path string) (int64, error) {
	for {
		var st syscall.Statfs_t
		err := syscall.Statfs(path, &st)
		if err == nil {
			// the field types differ between Linux and macOS
			return int64(min(st.Bavail*uint64(st.Bsize), math.MaxInt64)), nil //nolint:gosec // the block size is positive
		}
		parent := filepath.Dir(path)
		if !os.IsNotExist(err) || parent == path {
			return 0, err
		}
		path = parent
	}
}
