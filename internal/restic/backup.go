package restic

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// maxErrors limits how many unreadable files a BackupSummary lists.
const maxErrors = 20

// maxOutput limits how many lines of text output a BackupSummary keeps.
const maxOutput = 20

// BackupOptions describes one backup run.
type BackupOptions struct {
	Host     string
	Tags     []string
	Parent   string // snapshot to use as parent, empty for restic's default
	Excludes []string
	// Paths are backed up relative to the working directory.
	Paths []string
	// StdinCommand, if set, backs up the output of this command instead of
	// files, stored as StdinFilename.
	StdinCommand  []string
	StdinFilename string
	Extra         []string
}

func (o BackupOptions) args() ([]string, error) {
	if err := checkTags(o.Tags...); err != nil {
		return nil, err
	}
	args := []string{"backup", "--json", "--host", o.Host}
	for _, t := range o.Tags {
		args = append(args, "--tag", t)
	}
	if o.Parent != "" {
		args = append(args, "--parent", o.Parent)
	}
	for _, e := range o.Excludes {
		args = append(args, "--exclude", e)
	}
	args = append(args, o.Extra...)
	if len(o.StdinCommand) > 0 {
		args = append(args, "--stdin-from-command", "--stdin-filename", o.StdinFilename)
		return positional(args, o.StdinCommand...), nil
	}
	return positional(args, o.Paths...), nil
}

// checkTags rejects tags restic would split at commas.
func checkTags(tags ...string) error {
	for _, t := range tags {
		if t == "" || strings.Contains(t, ",") {
			return fmt.Errorf("invalid tag %q: must not be empty or contain a comma", t)
		}
	}
	return nil
}

// Backup runs a backup and reports progress to onStatus (which may be nil).
// If restic created the snapshot but could not read some files, the summary
// is valid and the error satisfies errors.Is(err, ErrIncomplete).
func (r *Repo) Backup(ctx context.Context, o BackupOptions, onStatus func(BackupStatus)) (BackupSummary, error) {
	args, err := o.args()
	if err != nil {
		return BackupSummary{}, err
	}
	var (
		sum       BackupSummary
		gotResult bool
	)
	stdoutLine := func(line []byte) {
		var m struct {
			Type string `json:"message_type"`
		}
		if json.Unmarshal(line, &m) != nil {
			return
		}
		switch m.Type {
		case "status":
			var st BackupStatus
			if onStatus != nil && json.Unmarshal(line, &st) == nil {
				onStatus(st)
			}
		case "summary":
			if json.Unmarshal(line, &sum) == nil {
				gotResult = true
			}
		}
	}
	// Unreadable files are reported on stderr, and so is what the stdin
	// command writes there, as text: "subprocess <command>: <line>".
	var errs, cmdOut, out []string
	var prefix string
	if len(o.StdinCommand) > 0 {
		prefix = "subprocess " + o.StdinCommand[0] + ": "
	}
	stderrLine := func(line []byte) {
		var m struct {
			Type  string `json:"message_type"`
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		switch {
		case json.Unmarshal(line, &m) == nil:
			if m.Type == "error" && len(errs) < maxErrors {
				errs = append(errs, m.Error.Message)
			}
		case prefix != "" && strings.HasPrefix(string(line), prefix):
			cmdOut = keepLast(cmdOut, strings.TrimPrefix(string(line), prefix))
		default:
			out = keepLast(out, string(line))
		}
	}

	_, err = r.run(ctx, args, runOpts{stdoutLine: stdoutLine, stderrLine: stderrLine})
	sum.Errors, sum.CommandOutput, sum.Output = errs, cmdOut, out
	if err != nil && !errors.Is(err, ErrIncomplete) && len(cmdOut) > 0 {
		// the reason of a failed command is in its own words, not in restic's
		err = fmt.Errorf("%w; %s said: %s", err, filepath.Base(o.StdinCommand[0]), cmdOut[len(cmdOut)-1])
	}
	if err != nil {
		return sum, err // for ErrIncomplete the snapshot exists and sum is valid
	}
	if !gotResult {
		return sum, errors.New("restic backup: no summary in output")
	}
	return sum, nil
}

// keepLast appends line and drops the oldest lines beyond maxOutput.
func keepLast(lines []string, line string) []string {
	lines = append(lines, line)
	if len(lines) > maxOutput {
		lines = lines[len(lines)-maxOutput:]
	}
	return lines
}
