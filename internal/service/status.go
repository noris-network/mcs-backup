package service

import "time"

// RepoStatus is the state of a repository.
type RepoStatus struct {
	// Up: the last attempt to reach the repository succeeded.
	Up bool `json:"up"`
	// Error is why the repository is not reachable.
	Error string `json:"error,omitempty"`
	ID    string `json:"id,omitempty"`
	// Snapshots is the number of snapshots of this instance, -1 if unknown.
	Snapshots int `json:"snapshots"`
	// SizeBytes is the raw data size, -1 if unknown.
	SizeBytes int64 `json:"size_bytes"`
	// LastSuccess is the time of the newest regular snapshot.
	LastSuccess time.Time `json:"last_success,omitzero"`
}

// ScheduleStatus is the state of the schedules.
type ScheduleStatus struct {
	// Enabled: scheduled backups are on (unless a maintenance window is open).
	Enabled bool `json:"enabled"`
	// MaintenanceUntil is the end of an open maintenance window, zero if none.
	MaintenanceUntil time.Time `json:"maintenance_until,omitzero"`
	// Backup is the cron expression of the backup schedule, empty if there is none.
	Backup string `json:"backup,omitempty"`
	// Next is when each scheduled kind runs next.
	Next    map[Kind]time.Time `json:"next,omitempty"`
	CatchUp bool               `json:"catch_up"`
}

// RetryInfo describes a pending retry of a failed scheduled backup.
type RetryInfo struct {
	Attempt int       `json:"attempt"`
	Max     int       `json:"max"`
	At      time.Time `json:"at"`
}

// Severity of a warning.
type Severity string

const (
	SeverityInfo    Severity = "info"
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
)

// Warning is something an operator should look at.
type Warning struct {
	// Code is stable and machine readable, e.g. "backup_suspicious".
	Code     string   `json:"code"`
	Severity Severity `json:"severity"`
	Message  string   `json:"message"`
}

// Status summarizes the state of the service.
type Status struct {
	Version   string    `json:"version"`
	Host      string    `json:"host"`
	StartedAt time.Time `json:"started_at"`
	// Ready: the start sequence is through and the repository was reached once.
	Ready bool `json:"ready"`

	Repository RepoStatus  `json:"repository"`
	Copy       *RepoStatus `json:"copy,omitempty"`

	Schedule ScheduleStatus `json:"schedule"`
	// Operation is the running operation, nil if idle.
	Operation *Operation `json:"operation,omitempty"`
	// Retry is a pending retry of a failed scheduled backup.
	Retry *RetryInfo `json:"retry,omitempty"`
	// LastRuns holds the latest result of each kind since the service started.
	LastRuns map[Kind]RunResult `json:"last_runs,omitempty"`

	// Retention describes the policy in plain words.
	Retention  string `json:"retention"`
	AppendOnly bool   `json:"append_only"`
	PipeMode   bool   `json:"pipe_mode"`
	// Suspicious: a snapshot is tagged suspicious and waits for acknowledgement.
	Suspicious bool `json:"suspicious"`
	// ForgetHeld: forget is held back by the retention guard.
	ForgetHeld bool `json:"forget_held"`
	// VerifyConfigured: the restore test can run.
	VerifyConfigured bool `json:"verify_configured"`

	WebUI    WebUIStatus `json:"webui"`
	Warnings []Warning   `json:"warnings,omitempty"`
}
