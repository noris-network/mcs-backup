package restic

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strings"
)

// maxErrors limits how many unreadable files a BackupSummary lists.
const maxErrors = 20

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
	// Unreadable files are reported on stderr.
	var errs []string
	stderrLine := func(line []byte) {
		var m struct {
			Type  string `json:"message_type"`
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(line, &m) == nil && m.Type == "error" && len(errs) < maxErrors {
			errs = append(errs, m.Error.Message)
		}
	}

	_, err = r.run(ctx, args, runOpts{stdoutLine: stdoutLine, stderrLine: stderrLine})
	sum.Errors = errs
	if err != nil {
		return sum, err // for ErrIncomplete the snapshot exists and sum is valid
	}
	if !gotResult {
		return sum, errors.New("restic backup: no summary in output")
	}
	return sum, nil
}
