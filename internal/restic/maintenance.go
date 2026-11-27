package restic

import (
	"context"
	"encoding/json/v2"
	"errors"
	"io"
	"slices"
	"strings"
)

// ForgetOptions describes one forget run.
type ForgetOptions struct {
	// Hosts restricts forgetting to snapshots of these hosts.
	Hosts []string
	// KeepTags are tags whose snapshots are always kept.
	KeepTags []string
	// Policy are the --keep-* flags.
	Policy []string
	// DryRun only reports what would be removed and does not lock the repository.
	DryRun bool
	Extra  []string
}

func (o ForgetOptions) args() ([]string, error) {
	if err := checkTags(o.KeepTags...); err != nil {
		return nil, err
	}
	args := []string{"forget", "--json", "--group-by", "paths"}
	if o.DryRun {
		args = append(args, "--dry-run", "--no-lock")
	}
	for _, h := range o.Hosts {
		args = append(args, "--host", h)
	}
	for _, t := range o.KeepTags {
		args = append(args, "--keep-tag", t)
	}
	args = append(args, o.Policy...)
	return append(args, o.Extra...), nil
}

// Forget removes snapshots according to the policy, without pruning data. It
// returns the groups with the snapshots kept and removed.
func (r *Repo) Forget(ctx context.Context, o ForgetOptions) ([]ForgetGroup, error) {
	args, err := o.args()
	if err != nil {
		return nil, err
	}
	var groups []ForgetGroup
	return groups, r.query(ctx, args, &groups)
}

// Remove forgets the given snapshots without pruning their data.
func (r *Repo) Remove(ctx context.Context, ids ...string) error {
	_, err := r.run(ctx, positional([]string{"forget"}, ids...), runOpts{})
	return err
}

// Prune removes unreferenced data. Output goes to out.
func (r *Repo) Prune(ctx context.Context, extra []string, out io.Writer) error {
	_, err := r.run(ctx, append([]string{"prune"}, extra...), runOpts{combined: out})
	return err
}

// Check verifies the repository structure. Output goes to out.
func (r *Repo) Check(ctx context.Context, extra []string, out io.Writer) error {
	_, err := r.run(ctx, append([]string{"check"}, extra...), runOpts{combined: out})
	return err
}

// Unlock removes stale locks, or all locks if all is set.
func (r *Repo) Unlock(ctx context.Context, all bool) error {
	args := []string{"unlock"}
	if all {
		args = append(args, "--remove-all")
	}
	_, err := r.run(ctx, args, runOpts{})
	return err
}

// Tag adds and removes tags on a snapshot. restic rewrites the snapshot, which
// gives it a new ID: the new ID is returned if restic reports it (0.18 and
// later), otherwise it is empty and the caller has to look it up.
func (r *Repo) Tag(ctx context.Context, snapshot string, add, remove []string) (newID string, err error) {
	if err := checkTags(slices.Concat(add, remove)...); err != nil {
		return "", err
	}
	args := []string{"tag", "--json"}
	for _, t := range add {
		args = append(args, "--add", t)
	}
	for _, t := range remove {
		args = append(args, "--remove", t)
	}
	_, err = r.run(ctx, positional(args, snapshot), runOpts{stdoutLine: func(line []byte) {
		var m struct {
			Type string `json:"message_type"`
			New  string `json:"new_snapshot_id"`
		}
		if json.Unmarshal(line, &m) == nil && m.Type == "changed" {
			newID = m.New
		}
	}})
	return newID, err
}

// Init creates the repository. With from set, the new repository uses the same
// chunker parameters as from so that copying between them deduplicates.
// It returns the ID of the new repository.
func (r *Repo) Init(ctx context.Context, from *Repo) (string, error) {
	args := []string{"init", "--json"}
	var env []string
	if from != nil {
		args = append(args, "--copy-chunker-params")
		env = from.fromEnv()
	}
	var res struct {
		ID string `json:"id"`
	}
	_, err := r.run(ctx, args, runOpts{env: env, stdoutLine: func(line []byte) {
		_ = json.Unmarshal(line, &res)
	}})
	return res.ID, err
}

// CopyOptions describes one copy run.
type CopyOptions struct {
	// Snapshots to copy; all snapshots missing in the destination if empty.
	Snapshots []string
	// Hosts restricts copying to snapshots of these hosts. It is ignored when
	// Snapshots are given: restic refuses both together.
	Hosts []string
}

// Copy copies snapshots from the repository from into this one. Output goes to out.
func (r *Repo) Copy(ctx context.Context, from *Repo, o CopyOptions, out io.Writer) error {
	if from == nil {
		return errors.New("restic copy: no source repository")
	}
	args := append([]string{"copy"}, hostFilter(o.Hosts, o.Snapshots)...)
	_, err := r.run(ctx, positional(args, o.Snapshots...), runOpts{env: from.fromEnv(), combined: out})
	return err
}

// hostFilter is the --host options for hosts, or none when explicit snapshots
// are given: since 0.20 restic refuses a filter next to snapshot IDs
// ("explicit snapshot ids are given"), and the caller chose them already.
func hostFilter(hosts, snapshots []string) []string {
	if len(snapshots) > 0 {
		return nil
	}
	var args []string
	for _, h := range hosts {
		args = append(args, "--host", h)
	}
	return args
}

// fromEnv maps the credentials of this repository to the RESTIC_FROM_*
// variables that restic reads for the source of init and copy.
func (r *Repo) fromEnv() []string {
	var out []string
	for _, kv := range r.env {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		switch k {
		case "RESTIC_REPOSITORY", "RESTIC_REPOSITORY_FILE",
			"RESTIC_PASSWORD", "RESTIC_PASSWORD_FILE", "RESTIC_PASSWORD_COMMAND":
			out = append(out, "RESTIC_FROM_"+k[len("RESTIC_"):]+"="+v)
		}
	}
	return out
}
