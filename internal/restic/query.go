package restic

import (
	"bufio"
	"context"
	"encoding/json/v2"
	"fmt"
	"io"
	"iter"
	"regexp"
	"strconv"
	"strings"
)

// Version is a restic release number.
type Version struct {
	Major, Minor, Patch int
}

func (v Version) String() string { return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch) }

// AtLeast reports whether v is at least major.minor.
func (v Version) AtLeast(major, minor int) bool {
	return v.Major > major || v.Major == major && v.Minor >= minor
}

var versionRe = regexp.MustCompile(`restic (\d+)\.(\d+)\.(\d+)`)

// ParseVersion parses the output of `restic version`.
func ParseVersion(s string) (Version, error) {
	m := versionRe.FindStringSubmatch(s)
	if m == nil {
		return Version{}, fmt.Errorf("cannot parse restic version from %q", strings.TrimSpace(s))
	}
	n := func(s string) int { v, _ := strconv.Atoi(s); return v }
	return Version{n(m[1]), n(m[2]), n(m[3])}, nil
}

// Version returns the version of the restic binary.
func (r *Repo) Version(ctx context.Context) (Version, error) {
	out, err := r.run(ctx, []string{"version"}, runOpts{})
	if err != nil {
		return Version{}, err
	}
	return ParseVersion(string(out))
}

// Config reads the repository config. It is the cheapest way to check that
// the repository exists and the password is right.
func (r *Repo) Config(ctx context.Context) (RepoConfig, error) {
	var c RepoConfig
	return c, r.query(ctx, []string{"cat", "config", "--no-lock"}, &c)
}

// Snapshots lists all snapshots of the repository.
func (r *Repo) Snapshots(ctx context.Context) ([]Snapshot, error) {
	var s []Snapshot
	return s, r.query(ctx, []string{"snapshots", "--json", "--no-lock"}, &s)
}

// Stats returns statistics for mode (StatsRestoreSize or StatsRawData) over
// the given snapshots, or over the whole repository if none are given.
func (r *Repo) Stats(ctx context.Context, mode string, snapshots ...string) (Stats, error) {
	var s Stats
	args := append([]string{"stats", "--json", "--no-lock", "--mode", mode}, snapshots...)
	return s, r.query(ctx, args, &s)
}

// query runs a command that prints one JSON document and decodes it into v.
func (r *Repo) query(ctx context.Context, args []string, v any) error {
	out, err := r.run(ctx, args, runOpts{})
	if err != nil {
		return err
	}
	if err := json.Unmarshal(out, v); err != nil {
		return fmt.Errorf("restic %s: unexpected output: %w", args[0], err)
	}
	return nil
}

// Locks lists the locks in the repository. Locks that vanish while being read
// are skipped.
func (r *Repo) Locks(ctx context.Context) ([]Lock, error) {
	out, err := r.run(ctx, []string{"list", "locks", "--no-lock"}, runOpts{})
	if err != nil {
		return nil, err
	}
	var locks []Lock
	for id := range strings.FieldsSeq(string(out)) {
		var l Lock
		if err := r.query(ctx, []string{"cat", "lock", id, "--no-lock"}, &l); err != nil {
			if ctx.Err() != nil {
				return nil, err
			}
			r.log.Debug("skip lock", "id", id, "error", err)
			continue
		}
		l.ID = id
		locks = append(locks, l)
	}
	return locks, nil
}

// Ls streams the nodes of a snapshot, optionally restricted to paths. The
// restic process ends when the loop stops early.
func (r *Repo) Ls(ctx context.Context, snapshot string, paths ...string) iter.Seq2[Node, error] {
	return func(yield func(Node, error) bool) {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()

		args := append([]string{"ls", "--json", "--no-lock", snapshot}, paths...)
		cmd := r.cmd(ctx, args)
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			yield(Node{}, err)
			return
		}
		tail := &tailBuffer{max: stderrTail}
		cmd.Stderr = tail
		if err := cmd.Start(); err != nil {
			yield(Node{}, fmt.Errorf("restic ls: %w", err))
			return
		}

		stopped := false
		rd := bufio.NewReader(stdout)
		for !stopped {
			line, readErr := rd.ReadBytes('\n')
			if node, ok := parseNode(line); ok && !yield(node, nil) {
				stopped = true
			}
			if readErr != nil {
				break
			}
		}
		if stopped {
			cancel()
		}
		_, _ = io.Copy(io.Discard, stdout)
		if err := cmd.Wait(); err != nil && !stopped {
			yield(Node{}, r.wrapError(ctx, "ls", tail.String(), err))
		}
	}
}

// parseNode decodes one line of `restic ls --json`. The first line describes
// the snapshot and is skipped. restic 0.17 marks line types with struct_type,
// newer versions with message_type.
func parseNode(line []byte) (Node, bool) {
	var n struct {
		Node
		MessageType string `json:"message_type"`
		StructType  string `json:"struct_type"`
	}
	if len(strings.TrimSpace(string(line))) == 0 || json.Unmarshal(line, &n) != nil {
		return Node{}, false
	}
	if n.MessageType == "snapshot" || n.StructType == "snapshot" || n.Path == "" {
		return Node{}, false
	}
	return n.Node, true
}
