package restic

import (
	"context"
	"encoding/json/v2"
	"errors"
	"io"
)

// RestoreOptions describes one restore run.
type RestoreOptions struct {
	Snapshot string
	Target   string
	// Includes restricts the restore to these paths.
	Includes []string
	// IncludeFile names a file that lists the paths to restore, one per line.
	IncludeFile string
	// Delete removes files from the target that are not in the snapshot
	// (within the included paths).
	Delete bool
	// Verify reads the restored files back and compares them.
	Verify bool
	Extra  []string
}

func (o RestoreOptions) args() []string {
	args := []string{"restore", o.Snapshot, "--json", "--target", o.Target}
	if o.Delete {
		args = append(args, "--delete")
	}
	if o.Verify {
		args = append(args, "--verify")
	}
	for _, p := range o.Includes {
		args = append(args, "--include", p)
	}
	if o.IncludeFile != "" {
		args = append(args, "--include-file", o.IncludeFile)
	}
	return append(args, o.Extra...)
}

// Restore restores a snapshot and reports progress to onStatus (which may be nil).
func (r *Repo) Restore(ctx context.Context, o RestoreOptions, onStatus func(RestoreStatus)) (RestoreSummary, error) {
	var (
		sum       RestoreSummary
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
			var st RestoreStatus
			if onStatus != nil && json.Unmarshal(line, &st) == nil {
				onStatus(st)
			}
		case "summary":
			if json.Unmarshal(line, &sum) == nil {
				gotResult = true
			}
		}
	}
	if _, err := r.run(ctx, o.args(), runOpts{stdoutLine: stdoutLine}); err != nil {
		return sum, err
	}
	if !gotResult {
		return sum, errors.New("restic restore: no summary in output")
	}
	return sum, nil
}

// Dump writes a file of a snapshot to w.
func (r *Repo) Dump(ctx context.Context, snapshot, file string, w io.Writer) error {
	_, err := r.run(ctx, []string{"dump", snapshot, file}, runOpts{stdout: w})
	return err
}
