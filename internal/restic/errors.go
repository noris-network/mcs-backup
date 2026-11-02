package restic

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Errors that identify a class of failure by restic's documented exit codes.
// Use errors.Is on errors returned by Repo methods.
var (
	// ErrIncomplete: the snapshot was created but some source files could not be read (exit code 3).
	ErrIncomplete = errors.New("some source files could not be read")
	// ErrRepoMissing: the repository does not exist (exit code 10).
	ErrRepoMissing = errors.New("repository does not exist")
	// ErrStorageMissing: the bucket or container of the repository does not
	// exist. It counts as a missing repository, because init creates the bucket.
	ErrStorageMissing = fmt.Errorf("the bucket or container of the repository does not exist: %w", ErrRepoMissing)
	// ErrLocked: the repository is locked by another process (exit code 11).
	ErrLocked = errors.New("repository is locked")
	// ErrWrongPassword: the password is wrong or no key was found (exit code 12).
	ErrWrongPassword = errors.New("wrong password or no key found")
	// ErrInterrupted: restic was interrupted by a signal (exit code 130).
	ErrInterrupted = errors.New("interrupted")
	// ErrEmptySnapshot: restic refuses to store a snapshot without any content.
	// It has no exit code of its own, so this is recognized by its message.
	ErrEmptySnapshot = errors.New("snapshot is empty")
)

// ExitError is a failed restic invocation.
type ExitError struct {
	Command string // restic subcommand, e.g. "backup"
	Code    int    // process exit code, -1 if killed
	Message string // restic's error message
}

func (e *ExitError) Error() string {
	msg := fmt.Sprintf("restic %s: exit code %d", e.Command, e.Code)
	if e.Message != "" {
		msg += ": " + e.Message
	}
	return msg
}

// Is maps restic's exit codes to the Err* sentinels.
func (e *ExitError) Is(target error) bool {
	switch target {
	case ErrIncomplete:
		return e.Code == 3
	case ErrRepoMissing:
		return e.Code == 10
	case ErrLocked:
		return e.Code == 11
	case ErrWrongPassword:
		return e.Code == 12
	case ErrInterrupted:
		return e.Code == 130
	case ErrEmptySnapshot:
		return e.Code == 1 && strings.Contains(e.Message, "snapshot is empty")
	}
	return false
}

// errorMessage extracts the most useful message from restic's stderr: the
// exit_error line when run with --json, otherwise the whole text.
func errorMessage(stderr string) string {
	stderr = strings.TrimSpace(stderr)
	for line := range strings.SplitSeq(stderr, "\n") {
		var m struct {
			Type    string `json:"message_type"`
			Message string `json:"message"`
		}
		if json.Unmarshal([]byte(line), &m) == nil && m.Type == "exit_error" && m.Message != "" {
			return m.Message
		}
	}
	return stderr
}

// storageMissingRe matches what restic says while it retries for a bucket or a
// container that does not exist: S3 (and compatible servers), Azure and Google
// Cloud Storage.
var storageMissingRe = regexp.MustCompile(`(?i)specified bucket does not exist|nosuchbucket|containernotfound|specified container does not exist|bucket doesn't exist`)

// storageMissing reports whether a line of restic's output says that the
// bucket or container of the repository does not exist. restic keeps retrying
// on that for minutes, as it would for a network error, before it gives up.
func storageMissing(line []byte) bool { return storageMissingRe.Match(line) }
