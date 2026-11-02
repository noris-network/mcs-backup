package present

import (
	"regexp"
	"strings"

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
	}
	if phase == "" {
		return ""
	}
	return strings.ToUpper(phase[:1]) + phase[1:]
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
