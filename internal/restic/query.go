package restic

import (
	"bufio"
	"context"
	"encoding/json/v2"
	"errors"
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

// LockCheck takes and releases a lock on the repository. It shows early
// whether the credentials may create (and, for append-only storage, delete)
// lock objects.
func (r *Repo) LockCheck(ctx context.Context) error {
	_, err := r.run(ctx, []string{"snapshots", "--json", "--latest", "1"}, runOpts{})
	return err
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
	args := positional([]string{"stats", "--json", "--no-lock", "--mode", mode}, snapshots...)
	return s, r.query(ctx, args, &s)
}

// query runs a command that prints one JSON document and decodes it into v.
func (r *Repo) query(ctx context.Context, args []string, v any) error {
	out, err := r.runQuery(ctx, args)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(out, v); err != nil {
		return fmt.Errorf("restic %s: unexpected output: %w", args[0], err)
	}
	return nil
}

// runQuery is run for a command that reads the repository. A bucket or container
// that does not exist ends it at once with ErrStorageMissing, which is an
// ErrRepoMissing: restic itself would retry for minutes, as it does for a network
// error. Creating the repository with init creates the bucket as well.
func (r *Repo) runQuery(ctx context.Context, args []string) ([]byte, error) {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	out, err := r.run(ctx, args, runOpts{stderrLine: func(line []byte) {
		if storageMissing(line) {
			cancel(ErrStorageMissing)
		}
	}})
	if errors.Is(context.Cause(ctx), ErrStorageMissing) {
		return nil, ErrStorageMissing
	}
	return out, err
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
			r.log.DebugContext(ctx, "skip lock", "id", id, "error", err)
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

		args := append(positional([]string{"ls", "--json", "--no-lock"}, snapshot), paths...)
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

// findOutputMax bounds the output of `restic find` that is read: restic prints
// all matches at once, and a pattern such as "*" matches every file of every
// snapshot.
const findOutputMax = 16 << 20

// ErrTooManyMatches is returned by Find when the pattern matches more than
// can be handled.
var ErrTooManyMatches = errors.New("too many matches")

// FindOptions restricts a search.
type FindOptions struct {
	// Hosts restricts the search to snapshots of these hosts.
	Hosts []string
	// Snapshots restricts the search to these snapshots.
	Snapshots []string
	// IgnoreCase matches the pattern regardless of case.
	IgnoreCase bool
}

// FindGroup is the matches of a search in one snapshot.
type FindGroup struct {
	Snapshot string `json:"snapshot"`
	Matches  []Node `json:"matches"`
}

// Find searches the snapshots for files and directories whose name or path
// matches the glob pattern. The groups come newest snapshot first.
func (r *Repo) Find(ctx context.Context, pattern string, o FindOptions) ([]FindGroup, error) {
	args := []string{"find", "--json", "--no-lock"}
	if o.IgnoreCase {
		args = append(args, "--ignore-case")
	}
	for _, h := range o.Hosts {
		args = append(args, "--host", h)
	}
	for _, s := range o.Snapshots {
		args = append(args, "--snapshot", s)
	}
	out := &capWriter{max: findOutputMax}
	_, err := r.run(ctx, positional(args, pattern), runOpts{stdout: out})
	if out.full {
		return nil, ErrTooManyMatches
	}
	if err != nil {
		return nil, err
	}
	var groups []FindGroup
	if err := json.Unmarshal(out.buf, &groups); err != nil {
		return nil, fmt.Errorf("restic find: unexpected output: %w", err)
	}
	return groups, nil
}

// capWriter collects what is written to it and fails once it is more than max.
type capWriter struct {
	max  int
	buf  []byte
	full bool
}

func (w *capWriter) Write(p []byte) (int, error) {
	if w.full || len(w.buf)+len(p) > w.max {
		w.full, w.buf = true, nil
		return 0, ErrTooManyMatches
	}
	w.buf = append(w.buf, p...)
	return len(p), nil
}

// DiffCount is one side of the statistics of `restic diff`.
type DiffCount struct {
	Files int `json:"files"`
	Dirs  int `json:"dirs"`
	// Bytes is the size of the data in the repository, not of the files.
	Bytes int64 `json:"bytes"`
}

// DiffStats is the summary of `restic diff`.
type DiffStats struct {
	ChangedFiles int       `json:"changed_files"`
	Added        DiffCount `json:"added"`
	Removed      DiffCount `json:"removed"`
}

// Diff compares two snapshots. onChange is called for every path that differs,
// in the order of the paths: a directory (its path ends with a slash) comes
// before what is in it. The modifier is "+" (added), "-" (removed), "M"
// (content changed), "T" (type changed) or "U" (only metadata changed).
func (r *Repo) Diff(ctx context.Context, from, to string, onChange func(path, modifier string)) (DiffStats, error) {
	var stats DiffStats
	args := positional([]string{"diff", "--json", "--no-lock"}, from, to)
	_, err := r.run(ctx, args, runOpts{stdoutLine: func(line []byte) {
		var m struct {
			Type     string `json:"message_type"`
			Path     string `json:"path"`
			Modifier string `json:"modifier"`
		}
		if json.Unmarshal(line, &m) != nil {
			return
		}
		switch m.Type {
		case "change":
			onChange(m.Path, m.Modifier)
		case "statistics":
			_ = json.Unmarshal(line, &stats)
		}
	}})
	return stats, err
}
