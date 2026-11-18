package restic

import (
	"slices"
	"time"
)

// Snapshot is a restic snapshot as printed by `restic snapshots --json`.
type Snapshot struct {
	ID       string    `json:"id"`
	ShortID  string    `json:"short_id"`
	Time     time.Time `json:"time"`
	Parent   string    `json:"parent"`
	Tree     string    `json:"tree"`
	Paths    []string  `json:"paths"`
	Hostname string    `json:"hostname"`
	Username string    `json:"username"`
	Tags     []string  `json:"tags"`
	// Original is the ID of the snapshot this one was copied from.
	Original string `json:"original"`
	// Summary is nil for snapshots created by restic before 0.17.
	Summary *SnapshotSummary `json:"summary"`
}

// HasTag reports whether the snapshot carries tag.
func (s Snapshot) HasTag(tag string) bool { return slices.Contains(s.Tags, tag) }

// SnapshotSummary holds the statistics restic stores with a snapshot.
type SnapshotSummary struct {
	BackupStart         time.Time `json:"backup_start"`
	BackupEnd           time.Time `json:"backup_end"`
	FilesNew            int64     `json:"files_new"`
	FilesChanged        int64     `json:"files_changed"`
	FilesUnmodified     int64     `json:"files_unmodified"`
	DirsNew             int64     `json:"dirs_new"`
	DirsChanged         int64     `json:"dirs_changed"`
	DirsUnmodified      int64     `json:"dirs_unmodified"`
	DataBlobs           int64     `json:"data_blobs"`
	TreeBlobs           int64     `json:"tree_blobs"`
	DataAdded           int64     `json:"data_added"`
	DataAddedPacked     int64     `json:"data_added_packed"`
	TotalFilesProcessed int64     `json:"total_files_processed"`
	TotalBytesProcessed int64     `json:"total_bytes_processed"`
}

// RepoConfig is the repository's config object.
type RepoConfig struct {
	Version           int    `json:"version"`
	ID                string `json:"id"`
	ChunkerPolynomial string `json:"chunker_polynomial"`
}

// Lock is a lock object in the repository.
type Lock struct {
	ID        string    `json:"-"`
	Time      time.Time `json:"time"`
	Exclusive bool      `json:"exclusive"`
	Hostname  string    `json:"hostname"`
	Username  string    `json:"username"`
	PID       int       `json:"pid"`
	UID       int       `json:"uid"`
	GID       int       `json:"gid"`
}

// Node is a file or directory in a snapshot.
type Node struct {
	Name  string    `json:"name"`
	Type  string    `json:"type"` // "file", "dir", "symlink", ...
	Path  string    `json:"path"`
	Size  int64     `json:"size"`
	Mtime time.Time `json:"mtime"`
}

// Stats is the output of `restic stats`.
type Stats struct {
	TotalSize             int64   `json:"total_size"`
	TotalFileCount        int64   `json:"total_file_count"`
	TotalBlobCount        int64   `json:"total_blob_count"`
	TotalUncompressedSize int64   `json:"total_uncompressed_size"`
	CompressionRatio      float64 `json:"compression_ratio"`
	SnapshotsCount        int     `json:"snapshots_count"`
}

// Stats modes.
const (
	StatsRestoreSize = "restore-size"
	StatsRawData     = "raw-data"
)

// ForgetGroup is one group of snapshots in the output of `restic forget --json`.
type ForgetGroup struct {
	Host   string     `json:"host"`
	Paths  []string   `json:"paths"`
	Tags   []string   `json:"tags"`
	Keep   []Snapshot `json:"keep"`
	Remove []Snapshot `json:"remove"`
}

// BackupStatus is a progress report of a running backup.
type BackupStatus struct {
	SecondsElapsed   int      `json:"seconds_elapsed"`
	SecondsRemaining int      `json:"seconds_remaining"`
	PercentDone      float64  `json:"percent_done"`
	TotalFiles       int64    `json:"total_files"`
	FilesDone        int64    `json:"files_done"`
	TotalBytes       int64    `json:"total_bytes"`
	BytesDone        int64    `json:"bytes_done"`
	ErrorCount       int64    `json:"error_count"`
	CurrentFiles     []string `json:"current_files"`
}

// BackupSummary is the final report of a backup.
type BackupSummary struct {
	SnapshotID          string    `json:"snapshot_id"`
	FilesNew            int64     `json:"files_new"`
	FilesChanged        int64     `json:"files_changed"`
	FilesUnmodified     int64     `json:"files_unmodified"`
	DirsNew             int64     `json:"dirs_new"`
	DirsChanged         int64     `json:"dirs_changed"`
	DirsUnmodified      int64     `json:"dirs_unmodified"`
	DataBlobs           int64     `json:"data_blobs"`
	TreeBlobs           int64     `json:"tree_blobs"`
	DataAdded           int64     `json:"data_added"`
	DataAddedPacked     int64     `json:"data_added_packed"`
	TotalFilesProcessed int64     `json:"total_files_processed"`
	TotalBytesProcessed int64     `json:"total_bytes_processed"`
	TotalDuration       float64   `json:"total_duration"`
	BackupStart         time.Time `json:"backup_start"`
	BackupEnd           time.Time `json:"backup_end"`
	// Errors lists files that could not be read (at most maxErrors).
	Errors []string `json:"-"`
	// CommandOutput holds the last lines the stdin command wrote to stderr
	// (at most maxOutput), without the prefix restic puts in front of them.
	CommandOutput []string `json:"-"`
	// Output holds the last other lines restic wrote to stderr as text
	// rather than JSON, such as notes about retries (at most maxOutput).
	Output []string `json:"-"`
}

// RestoreStatus is a progress report of a running restore.
type RestoreStatus struct {
	PercentDone   float64 `json:"percent_done"`
	TotalFiles    int64   `json:"total_files"`
	FilesRestored int64   `json:"files_restored"`
	TotalBytes    int64   `json:"total_bytes"`
	BytesRestored int64   `json:"bytes_restored"`
}

// RestoreSummary is the final report of a restore.
type RestoreSummary struct {
	TotalFiles    int64 `json:"total_files"`
	FilesRestored int64 `json:"files_restored"`
	FilesSkipped  int64 `json:"files_skipped"`
	TotalBytes    int64 `json:"total_bytes"`
	BytesRestored int64 `json:"bytes_restored"`
}
