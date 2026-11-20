package service

import (
	"context"
	"errors"
	"iter"
	"time"
)

// Where a request comes from. It is logged with every action.
const (
	SourceCLI      = "cli"
	SourceTUI      = "tui"
	SourceWeb      = "web"
	SourceMCP      = "mcp"
	SourceSchedule = "schedule"
)

type sourceKey struct{}

// WithSource records where a request comes from.
func WithSource(ctx context.Context, source string) context.Context {
	return context.WithValue(ctx, sourceKey{}, source)
}

// SourceFrom returns the source recorded in ctx, "unknown" if there is none.
func SourceFrom(ctx context.Context) string {
	if s, ok := ctx.Value(sourceKey{}).(string); ok && s != "" {
		return s
	}
	return "unknown"
}

// Words that confirm destructive actions. A restore is confirmed with the
// snapshot argument itself.
const (
	ConfirmForget      = "forget"
	ConfirmRemoveAll   = "remove-all"
	ConfirmAcknowledge = "acknowledge"
)

// Errors that clients have to present to the user.
var (
	// ErrBusy: another operation is running (BusyError carries it).
	ErrBusy = errors.New("another operation is running")
	// ErrConfirm: a destructive action was not confirmed.
	ErrConfirm = errors.New("confirmation missing or wrong")
	// ErrUnavailable: the function is not available in this configuration.
	ErrUnavailable = errors.New("not available")
	// ErrAppendOnly: the repository is append-only.
	ErrAppendOnly = errors.New("not possible: the repository is append-only")
	// ErrNotFound: no such snapshot.
	ErrNotFound = errors.New("no matching snapshot")
)

// BusyError is returned when an operation was requested while another one runs.
type BusyError struct {
	Current Operation
}

func (e *BusyError) Error() string {
	return "another operation is running: " + string(e.Current.Kind) + " (started " + e.Current.Started.Format(time.RFC3339) + ")"
}

// Is makes errors.Is(err, ErrBusy) work.
func (e *BusyError) Is(target error) bool { return target == ErrBusy }

// Duration is a time.Duration that appears in JSON as a string such as
// "1h30m0s". Decoding accepts everything time.ParseDuration does, so that a
// request can say "2h".
type Duration time.Duration

// Std returns d as a time.Duration.
func (d Duration) Std() time.Duration { return time.Duration(d) }

func (d Duration) String() string { return time.Duration(d).String() }

// MarshalText implements encoding.TextMarshaler.
func (d Duration) MarshalText() ([]byte, error) { return []byte(d.String()), nil }

// UnmarshalText implements encoding.TextUnmarshaler.
func (d *Duration) UnmarshalText(text []byte) error {
	v, err := time.ParseDuration(string(text))
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

// Progress is the progress of a running backup or restore.
type Progress struct {
	Percent    float64 `json:"percent"` // 0..1
	FilesDone  int64   `json:"files_done,omitzero"`
	FilesTotal int64   `json:"files_total,omitzero"`
	BytesDone  int64   `json:"bytes_done,omitzero"`
	BytesTotal int64   `json:"bytes_total,omitzero"`
	Current    string  `json:"current,omitempty"`
}

// Operation is the operation that currently holds the operation slot.
type Operation struct {
	ID      string    `json:"id"`
	Kind    Kind      `json:"kind"`
	Source  string    `json:"source"`
	Origin  Origin    `json:"origin,omitempty"`
	Detail  string    `json:"detail,omitempty"`
	Started time.Time `json:"started"`
	// Phase is the current step, e.g. "backup" or "forget".
	Phase    string    `json:"phase,omitempty"`
	Progress *Progress `json:"progress,omitempty"`
	// Attempt counts retries of a failed scheduled backup, starting at 1.
	Attempt int `json:"attempt,omitzero"`
}

// RunResult is the outcome of an operation.
type RunResult struct {
	ID       string    `json:"id"`
	Kind     Kind      `json:"kind"`
	Result   Result    `json:"result"`
	Message  string    `json:"message,omitempty"`
	Error    string    `json:"error,omitempty"`
	Snapshot string    `json:"snapshot,omitempty"`
	Warnings []string  `json:"warnings,omitempty"`
	Source   string    `json:"source,omitempty"`
	Started  time.Time `json:"started"`
	Duration Duration  `json:"duration"`
	// Attempt is the number of the attempt (1 for the first).
	Attempt int `json:"attempt,omitzero"`
	// WillRetry: the operation failed and will be tried again.
	WillRetry bool `json:"will_retry,omitzero"`
}

// EventType tells what an Event reports.
type EventType string

const (
	EventStarted  EventType = "started"  // an operation started
	EventPhase    EventType = "phase"    // an operation entered a new phase
	EventProgress EventType = "progress" // an operation made progress
	EventLog      EventType = "log"      // a line of the run log
	EventFinished EventType = "finished" // an operation finished
	EventStatus   EventType = "status"   // something in the status changed
)

// Event is a live report of what the service does.
type Event struct {
	Type      EventType  `json:"type"`
	Time      time.Time  `json:"time"`
	Operation *Operation `json:"operation,omitempty"`
	Line      string     `json:"line,omitempty"`
	Result    *RunResult `json:"result,omitempty"`
}

// Log is the log of the latest run of one kind.
type Log struct {
	Kind  Kind      `json:"kind"`
	Lines []string  `json:"lines"`
	Start time.Time `json:"start,omitzero"`
}

// BackupRequest asks for a backup.
type BackupRequest struct {
	// Note is stored with the snapshot.
	Note string `json:"note,omitempty"`
	// Keep protects the snapshot from forget.
	Keep bool `json:"keep,omitzero"`
	// Origin says why the backup is made; manual if empty.
	Origin Origin `json:"origin,omitempty"`
}

// RestoreMode says what happens to files in the target that are not in the snapshot.
type RestoreMode string

const (
	// ModeExact removes them, so that the target equals the snapshot.
	ModeExact RestoreMode = "exact"
	// ModeMerge leaves them alone.
	ModeMerge RestoreMode = "merge"
)

// RestoreRequest asks for a restore.
type RestoreRequest struct {
	// Snapshot is "latest" (the newest regular snapshot) or an ID.
	Snapshot string `json:"snapshot"`
	// Target is the directory to restore to; empty restores in place into BACKUP_ROOT.
	Target string `json:"target,omitempty"`
	// Includes restricts the restore to these paths of the snapshot.
	Includes []string `json:"include,omitempty"`
	// Mode applies to in-place restores, other targets always merge. Exact if empty.
	Mode RestoreMode `json:"mode,omitempty"`
	// NoSafetySnapshot skips the snapshot of the current data that is taken
	// before an in-place restore.
	NoSafetySnapshot bool `json:"no_safety_snapshot,omitzero"`
	// IgnoreSpaceWarning restores although the free space looks too small.
	IgnoreSpaceWarning bool `json:"ignore_space_warning,omitzero"`
	// Preview makes the preflight work out what the restore would change in
	// the target. That reads the files there and can take a while.
	Preview bool `json:"preview,omitzero"`
	// HookArgs are passed to the pre- and post-restore hooks.
	HookArgs []string `json:"hook_args,omitempty"`
	// Confirm must equal Snapshot for an in-place restore.
	Confirm string `json:"confirm,omitempty"`
}

// RestorePreflight describes what a restore would do.
type RestorePreflight struct {
	Snapshot Snapshot `json:"snapshot"`
	Target   string   `json:"target,omitempty"`
	// Includes are the paths of the snapshot to restore, empty for all.
	Includes []string `json:"include,omitempty"`
	// InPlace: the restore replaces the live data (BACKUP_ROOT or the database).
	InPlace bool `json:"in_place"`
	// PipeMode: the snapshot is streamed to PIPE_OUT_SCRIPT.
	PipeMode bool        `json:"pipe_mode"`
	Mode     RestoreMode `json:"mode"`
	// TargetEmpty reports whether the target directory is empty (ignoring lost+found).
	TargetEmpty bool `json:"target_empty"`
	// SafetySnapshot: a snapshot of the current data is taken first.
	SafetySnapshot bool `json:"safety_snapshot"`
	// NeedBytes is the additional space the restore needs, FreeBytes what is available.
	NeedBytes    int64 `json:"need_bytes"`
	FreeBytes    int64 `json:"free_bytes"`
	SpaceWarning bool  `json:"space_warning"`
	// Hooks lists the hooks that will run.
	Hooks    []string `json:"hooks,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
	// Changes is what the restore would do to the target, if a preview was
	// asked for.
	Changes *RestoreChanges `json:"changes,omitempty"`
}

// RestoreChanges counts what a restore would do to the files and directories
// of its target.
type RestoreChanges struct {
	Created     int `json:"created"`
	Overwritten int `json:"overwritten"`
	Deleted     int `json:"deleted"`
	Unchanged   int `json:"unchanged"`
	// OverwrittenPaths and DeletedPaths are the first of them.
	OverwrittenPaths []string `json:"overwritten_paths,omitempty"`
	DeletedPaths     []string `json:"deleted_paths,omitempty"`
}

// VerifyRequest asks for a restore test.
type VerifyRequest struct {
	// Mode is "sample" or "full"; the configured mode if empty.
	Mode string `json:"mode,omitempty"`
}

// TagResult is the outcome of a tag change.
type TagResult struct {
	// ID is the new ID of the snapshot (restic rewrites snapshots when tags change).
	ID       string   `json:"id,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// RetentionInfo tells what the retention policy would do right now.
type RetentionInfo struct {
	// Policy describes the policy in plain words.
	Policy string `json:"policy"`
	// Hosts are the restic host names whose snapshots the policy applies to.
	Hosts []string `json:"hosts"`
	// Foreign is the number of snapshots of other hosts, which are never touched.
	Foreign int `json:"foreign"`
	// Total is the number of snapshots of this instance.
	Total int `json:"total"`
	// Remove lists the snapshots the policy would forget.
	Remove []Snapshot `json:"remove,omitempty"`
	// Held is set if the retention guard would hold back this forget.
	Held bool `json:"held"`
	// AppendOnly: nothing is ever forgotten.
	AppendOnly bool `json:"append_only"`
}

// Lock is a lock in the repository.
type Lock struct {
	ID        string    `json:"id"`
	Time      time.Time `json:"time"`
	Age       Duration  `json:"age"`
	Exclusive bool      `json:"exclusive"`
	Hostname  string    `json:"hostname"`
	Username  string    `json:"username"`
	PID       int       `json:"pid"`
	// Stale: restic would remove this lock with a plain unlock.
	Stale bool `json:"stale"`
}

// File is a file or directory in a snapshot.
type File struct {
	Path string `json:"path"`
	// Type is "file", "dir", "symlink" or another type restic knows.
	Type  string    `json:"type"`
	Size  int64     `json:"size"`
	Mtime time.Time `json:"mtime"`
}

// FilesRequest asks for the content of a directory in a snapshot.
type FilesRequest struct {
	// Snapshot is an ID, a prefix of one or "latest" (the default).
	Snapshot string `json:"snapshot,omitempty"`
	// Path is the directory or file to list, as it is in the snapshot: what
	// lies in BACKUP_ROOT is found below "/" there ("docs" or "/docs" for
	// BACKUP_ROOT/docs). The path in the file system works as well. Empty
	// lists the top of the snapshot.
	Path string `json:"path,omitempty"`
}

// FileList is the content of a directory in a snapshot.
type FileList struct {
	Snapshot Snapshot `json:"snapshot"`
	// Path is the directory that was listed, as it is in the snapshot.
	Path  string `json:"path"`
	Files []File `json:"files"`
	// Truncated: the directory has more entries than are shown.
	Truncated bool `json:"truncated,omitzero"`
}

// MaxFileText is how much of a file ReadFile returns.
const MaxFileText = 256 << 10

// FileRequest asks for the content of a file in a snapshot.
type FileRequest struct {
	// Snapshot is an ID, a prefix of one or "latest" (the default).
	Snapshot string `json:"snapshot,omitempty"`
	// Path is the file as it is in the snapshot.
	Path string `json:"path"`
}

// FileContent is the start of a file in a snapshot, to look at it.
type FileContent struct {
	// Snapshot is the ID of the snapshot the file was read from.
	Snapshot string `json:"snapshot"`
	Path     string `json:"path"`
	// Text is the content, at most MaxFileText bytes; empty for a binary file.
	Text string `json:"text,omitempty"`
	// Binary: the file is not text (it has NUL bytes or is not UTF-8).
	Binary bool `json:"binary,omitzero"`
	// Truncated: the file is longer than MaxFileText.
	Truncated bool `json:"truncated,omitzero"`
}

// FindRequest searches the snapshots of this instance for files.
type FindRequest struct {
	// Pattern is a glob that is matched against the name and the whole path,
	// such as "invoice.pdf", "*.sql" or "/docs/*".
	Pattern string `json:"pattern"`
	// Snapshot restricts the search to one snapshot; all if empty.
	Snapshot   string `json:"snapshot,omitempty"`
	IgnoreCase bool   `json:"ignore_case,omitzero"`
}

// FileVersion is one state of a file and the snapshots that have it.
type FileVersion struct {
	File
	// Newest and Oldest are the newest and the oldest snapshot with this
	// version, the same if there is only one. Snapshots counts them.
	Newest    SnapshotRef `json:"newest"`
	Oldest    SnapshotRef `json:"oldest"`
	Snapshots int         `json:"snapshots"`
}

// SnapshotRef names a snapshot.
type SnapshotRef struct {
	ID   string    `json:"id"`
	Time time.Time `json:"time"`
}

// FindResult is what a search found: the versions of every matching path,
// sorted by path, newest version first.
type FindResult struct {
	Versions []FileVersion `json:"versions"`
	// Searched is the number of snapshots that were searched.
	Searched int `json:"searched"`
	// Truncated: there are more matches than are shown.
	Truncated bool `json:"truncated,omitzero"`
}

// DiffRequest asks what changed between two snapshots.
type DiffRequest struct {
	// To is the newer snapshot. Empty is the newest snapshot of this
	// instance, also if it is marked suspicious.
	To string `json:"to,omitempty"`
	// From is the older snapshot. Empty is the newest regular snapshot
	// before To.
	From string `json:"from,omitempty"`
}

// DiffSide counts what one snapshot has and the other has not.
type DiffSide struct {
	Files int `json:"files"`
	Dirs  int `json:"dirs"`
	// Bytes is the size of the data in the repository, not of the files.
	Bytes int64 `json:"bytes"`
	// Paths are the files and directories that were added or removed as a
	// whole: a directory stands for everything in it. The largest come first.
	Paths []DiffPath `json:"paths,omitempty"`
	// More is how many further paths are not listed.
	More int `json:"more,omitzero"`
}

// DiffPath is a file or a directory (its path ends with a slash) that exists
// in only one of two snapshots.
type DiffPath struct {
	Path string `json:"path"`
	// Entries is the number of files and directories it stands for.
	Entries int `json:"entries"`
}

// DiffResult is what changed from one snapshot to another.
type DiffResult struct {
	From    Snapshot `json:"from"`
	To      Snapshot `json:"to"`
	Added   DiffSide `json:"added"`
	Removed DiffSide `json:"removed"`
	// Changed is the number of files whose content changed, ChangedPaths
	// the first of them.
	Changed      int      `json:"changed"`
	ChangedPaths []string `json:"changed_paths,omitempty"`
}

// ScheduleAction is what a ScheduleRequest does.
type ScheduleAction string

const (
	// ScheduleEnable switches scheduled backups on and ends a maintenance window.
	ScheduleEnable ScheduleAction = "enable"
	// ScheduleDisable switches scheduled backups off until enabled again or
	// until the service restarts.
	ScheduleDisable ScheduleAction = "disable"
	// ScheduleMaintenance pauses scheduled backups for a while. The window
	// survives restarts.
	ScheduleMaintenance ScheduleAction = "maintenance"
)

// ScheduleRequest controls scheduled backups.
type ScheduleRequest struct {
	Action ScheduleAction `json:"action"`
	// For is the length of a maintenance window.
	For Duration `json:"for,omitzero"`
}

// EnvVar is one variable of the restic environment.
type EnvVar struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Secret bool   `json:"secret,omitzero"`
}

// EnvInfo is what is needed to run restic against the repository elsewhere.
type EnvInfo struct {
	Vars []EnvVar `json:"vars"`
	// Args are restic options that have no environment variable and have to
	// be passed every time.
	Args []string `json:"args,omitempty"`
	// Notes are hints, e.g. about file based credentials.
	Notes []string `json:"notes,omitempty"`
	// MinVersion is the oldest restic that works with mcs-backup.
	MinVersion string `json:"min_version"`
}

// WebUIStatus is the state of the optional web UI.
type WebUIStatus struct {
	Enabled bool   `json:"enabled"`
	Addr    string `json:"addr,omitempty"`
	// Until is when the web UI switches itself off, zero if it stays on.
	Until time.Time `json:"until,omitzero"`
}

// WebUIRequest switches the web UI on or off.
type WebUIRequest struct {
	Enable bool `json:"enable"`
	// Addr is the listen address; the configured one if empty.
	Addr string `json:"addr,omitempty"`
	// For limits how long the web UI stays on.
	For Duration `json:"for,omitzero"`
}

// Service is everything a user interface can ask of mcs-backup. The daemon
// implements it; the API client, the TUI, the web UI and the MCP server use it.
type Service interface {
	// Status summarizes the state of the service.
	Status(ctx context.Context) (Status, error)
	// Snapshots lists the snapshots of this instance.
	Snapshots(ctx context.Context) ([]Snapshot, error)
	// Retention previews what the retention policy would delete.
	Retention(ctx context.Context) (RetentionInfo, error)
	// Locks lists the locks in the repository.
	Locks(ctx context.Context) ([]Lock, error)
	// Files lists a directory in a snapshot, Find searches the snapshots for
	// files, to see what a restore would bring back and from which snapshot.
	Files(ctx context.Context, req FilesRequest) (FileList, error)
	Find(ctx context.Context, req FindRequest) (FindResult, error)
	// ReadFile returns the start of a file in a snapshot, to look at it
	// without restoring it.
	ReadFile(ctx context.Context, req FileRequest) (FileContent, error)
	// Diff tells what changed between two snapshots, to judge a backup that
	// is suspiciously small.
	Diff(ctx context.Context, req DiffRequest) (DiffResult, error)
	// RestorePreflight checks a restore without performing it.
	RestorePreflight(ctx context.Context, req RestoreRequest) (RestorePreflight, error)
	// Operation returns the running operation, nil if none.
	Operation(ctx context.Context) (*Operation, error)
	// Log returns the log of the latest run of a kind.
	Log(ctx context.Context, kind Kind) (Log, error)
	// Events streams what the service does until ctx ends. The first events
	// describe the running operation, if any.
	Events(ctx context.Context) iter.Seq2[Event, error]
	// Env returns the restic environment; secrets are masked unless reveal is set.
	Env(ctx context.Context, reveal bool) (EnvInfo, error)

	// StartBackup, StartRestore, StartVerify, StartPrune, StartCheck and
	// ApproveForget start an operation in the background and return at once.
	// ErrBusy is returned while another operation runs.
	StartBackup(ctx context.Context, req BackupRequest) (Operation, error)
	StartRestore(ctx context.Context, req RestoreRequest) (Operation, error)
	StartVerify(ctx context.Context, req VerifyRequest) (Operation, error)
	// StartPrune removes the data that no snapshot needs any more, from every
	// repository that is not append-only (ErrAppendOnly if none is).
	StartPrune(ctx context.Context) (Operation, error)
	// StartCheck checks the structure of the repositories.
	StartCheck(ctx context.Context) (Operation, error)
	// ApproveForget runs the forget that the retention guard held back.
	ApproveForget(ctx context.Context, confirm string) (Operation, error)
	// Cancel stops the running operation and discards a pending retry.
	Cancel(ctx context.Context) error

	// Keep, Unkeep and Acknowledge change the tags of a snapshot.
	Keep(ctx context.Context, ref string) (TagResult, error)
	Unkeep(ctx context.Context, ref string) (TagResult, error)
	Acknowledge(ctx context.Context, ref, confirm string) (TagResult, error)
	// Unlock removes stale locks, or all locks with all (confirm "remove-all").
	Unlock(ctx context.Context, all bool, confirm string) error

	// SetSchedule enables, disables or pauses scheduled backups.
	SetSchedule(ctx context.Context, req ScheduleRequest) (Status, error)
	// NotifyTest sends a test notification.
	NotifyTest(ctx context.Context) error

	// WebUI returns the state of the web UI, SetWebUI switches it on or off.
	WebUI(ctx context.Context) (WebUIStatus, error)
	SetWebUI(ctx context.Context, req WebUIRequest) (WebUIStatus, error)
	// LoginLink returns a one-time URL that logs a browser into the web UI.
	LoginLink(ctx context.Context) (string, error)
}
