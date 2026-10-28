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
	// HookArgs are passed to the pre- and post-restore hooks.
	HookArgs []string `json:"hook_args,omitempty"`
	// Confirm must equal Snapshot for an in-place restore.
	Confirm string `json:"confirm,omitempty"`
}

// RestorePreflight describes what a restore would do.
type RestorePreflight struct {
	Snapshot Snapshot `json:"snapshot"`
	Target   string   `json:"target,omitempty"`
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

	// StartBackup, StartRestore, StartVerify and ApproveForget start an
	// operation in the background and return at once. ErrBusy is returned
	// while another operation runs.
	StartBackup(ctx context.Context, req BackupRequest) (Operation, error)
	StartRestore(ctx context.Context, req RestoreRequest) (Operation, error)
	StartVerify(ctx context.Context, req VerifyRequest) (Operation, error)
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
