// Package service defines the vocabulary shared by the daemon, its clients
// and the user interfaces.
package service

import "strings"

// Kind is the type of an operation, used in metrics, logs and notifications.
type Kind string

const (
	KindBackup  Kind = "backup"
	KindCopy    Kind = "copy"
	KindPrune   Kind = "prune"
	KindCheck   Kind = "check"
	KindVerify  Kind = "verify"
	KindRestore Kind = "restore"
)

// Result is the outcome of an operation.
type Result string

const (
	ResultSuccess Result = "success"
	// ResultWarning: the operation did its job but something needs attention,
	// e.g. the copy failed or forget was held back.
	ResultWarning Result = "warning"
	// ResultSkipped: nothing had to be done.
	ResultSkipped Result = "skipped"
	ResultFailure Result = "failure"
	// ResultSuspicious: the backup completed but its content looks wrong.
	ResultSuspicious Result = "suspicious"
	ResultTimeout    Result = "timeout"
	ResultCancelled  Result = "cancelled"
)

func (r Result) severity() int {
	switch r {
	case ResultSkipped:
		return 0
	case ResultSuccess:
		return 1
	case ResultWarning:
		return 2
	case ResultSuspicious:
		return 3
	case ResultCancelled:
		return 4
	case ResultTimeout:
		return 5
	case ResultFailure:
		return 6
	}
	return 1
}

// Worse returns the more severe of r and other.
func (r Result) Worse(other Result) Result {
	if other.severity() > r.severity() {
		return other
	}
	return r
}

// OK reports whether the operation did what it should: success, skipped or
// a warning.
func (r Result) OK() bool { return r.severity() <= ResultWarning.severity() }

// Origin says why a backup was made. It is stored as a snapshot tag.
type Origin string

const (
	OriginScheduled  Origin = "scheduled"
	OriginManual     Origin = "manual"
	OriginCatchUp    Origin = "catch-up"
	OriginPreRestore Origin = "pre-restore"
)

// Tags used on snapshots.
const (
	TagApp        = "mcs-backup"
	TagKeep       = "keep"
	TagSuspicious = "suspicious"
	TagPreRestore = "pre-restore"
	NotePrefix    = "note:"
)

// LegacyHost is the restic host name that mcs-backup 1.x used for all snapshots.
const LegacyHost = "dummy"

// maxNote is the longest note, in characters.
const maxNote = 200

// NoteTag turns free text into a snapshot tag, or returns "" for an empty
// note. Commas would split the tag in restic and control characters make it
// unreadable, so both are replaced.
func NoteTag(note string) string {
	note = strings.Map(func(r rune) rune {
		switch {
		case r == ',':
			return ';'
		case r < ' ' || r == 0x7f:
			return ' '
		}
		return r
	}, note)
	note = strings.Join(strings.Fields(note), " ")
	if r := []rune(note); len(r) > maxNote {
		note = string(r[:maxNote])
	}
	if note == "" {
		return ""
	}
	return NotePrefix + note
}

// NoteOf returns the note stored in tags.
func NoteOf(tags []string) string {
	for _, t := range tags {
		if note, ok := strings.CutPrefix(t, NotePrefix); ok {
			return note
		}
	}
	return ""
}

// OriginOf returns the origin stored in tags, empty if there is none.
func OriginOf(tags []string) Origin {
	for _, t := range tags {
		switch o := Origin(t); o {
		case OriginScheduled, OriginManual, OriginCatchUp, OriginPreRestore:
			return o
		}
	}
	return ""
}

// IsRegular reports whether a snapshot with these tags is a regular snapshot:
// neither suspicious nor a safety snapshot taken before a restore. "Latest"
// always means the newest regular snapshot.
func IsRegular(tags []string) bool {
	for _, t := range tags {
		if t == TagSuspicious || t == TagPreRestore {
			return false
		}
	}
	return true
}
