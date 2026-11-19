package present

import (
	"cmp"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/noris-network/mcs-backup/v2/internal/service"
)

// PhaseTitle says in words what a phase of an operation does.
func PhaseTitle(phase string) string {
	switch phase {
	case "prepare":
		return "Preparing"
	case "backup":
		return "Backing up"
	case "forget":
		return "Applying the retention (forget)"
	case "forget copy":
		return "Applying the retention on the secondary repository"
	case "copy":
		return "Copying to the secondary repository"
	case "wrapup":
		return "Finishing"
	case "safety snapshot":
		return "Taking a safety snapshot of the current data"
	case "restore":
		return "Restoring"
	case "verify":
		return "Testing the restore"
	case "prune":
		return "Pruning the repository"
	case "check":
		return "Checking the repository"
	case "tag":
		return "Changing the tags of a snapshot"
	case "unlock":
		return "Removing locks"
	case "refresh":
		return "Reading the new state of the repository"
	}
	if phase == "" {
		return ""
	}
	return strings.ToUpper(phase[:1]) + phase[1:]
}

// KindName names a kind of operation in words: the kinds are identifiers of
// the API and the command line, which say verify for the restore test.
func KindName(k service.Kind) string {
	switch k {
	case service.KindVerify:
		return "restore test"
	case service.KindTag:
		return "tag change"
	}
	return string(k)
}

// OperationTitle says in words what a running operation does now.
func OperationTitle(op service.Operation) string {
	return cmp.Or(PhaseTitle(op.Phase), PhaseTitle(string(op.Kind)))
}

// OperationMeta says which operation runs, who started it and for how long.
func OperationMeta(now time.Time, op service.Operation) string {
	return fmt.Sprintf("%s, started by %s, running for %s", KindName(op.Kind), op.Source, Duration(now.Sub(op.Started)))
}

var (
	reParent    = regexp.MustCompile(`^using snapshot (\w+) as parent$`)
	reRemoved   = regexp.MustCompile(`^forget on the (\w+) repository removed (\d+) snapshots?$`)
	reSaved     = regexp.MustCompile(`^snapshot (\w+) saved$`)
	reCopying   = regexp.MustCompile(`^snapshot (\w+) of \[.*\] at .* by \S+$`)
	reNoPacks   = regexp.MustCompile(`^\[\d+:\d{2}\]\s+0 packs copied$`)
	reCopyStart = regexp.MustCompile(`^copy started, this may take a while\.\.\.$`)
)

// LogLine turns a line of the log of an operation into the line to show while
// it runs. It drops what is only noise (empty lines, the start message and the
// zero progress of restic copy) and says the rest in plain words. The log
// itself keeps the original lines.
func LogLine(line string) (string, bool) {
	line = strings.TrimSpace(line)
	switch {
	case line == "", reNoPacks.MatchString(line), reCopyStart.MatchString(line):
		return "", false
	}
	if m := reParent.FindStringSubmatch(line); m != nil {
		return "incremental to snapshot " + m[1], true
	}
	if m := reRemoved.FindStringSubmatch(line); m != nil {
		n := "snapshots"
		if m[2] == "1" {
			n = "snapshot"
		}
		repo := m[1]
		if repo == "copy" {
			repo = "secondary"
		}
		return "removed " + m[2] + " " + n + " from the " + repo + " repository", true
	}
	if m := reCopying.FindStringSubmatch(line); m != nil {
		return "copying snapshot " + m[1], true
	}
	if m := reSaved.FindStringSubmatch(line); m != nil {
		return "saved as snapshot " + m[1], true
	}
	return line, true
}

// ResultMark is the symbol in front of the result of an operation.
func ResultMark(r service.Result) string {
	switch r {
	case service.ResultSuccess:
		return "✓"
	case service.ResultWarning:
		return "!"
	case service.ResultSkipped:
		return "-"
	}
	return "✗"
}

// Unavailable says that a function is not available, and why if the service
// gave a reason, such as "NOTIFY_COMMAND is not configured".
func Unavailable(err error) string {
	if _, why, ok := strings.Cut(err.Error(), service.ErrUnavailable.Error()+": "); ok && why != "" {
		return "not available: " + why
	}
	return "not available in this configuration"
}
