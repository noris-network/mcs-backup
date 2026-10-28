package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/noris-network/mcs-backup/v2/internal/restic"
	"github.com/noris-network/mcs-backup/v2/internal/service"
)

// Verify modes.
const (
	VerifySample = "sample"
	VerifyFull   = "full"
)

// VerifyRepoResult is the outcome of the restore test on one repository.
type VerifyRepoResult struct {
	Repo     string
	Result   service.Result
	Snapshot string
	Files    int
	Bytes    int64
	Message  string
	Error    string
}

// VerifyResult is the outcome of a restore test.
type VerifyResult struct {
	Result   service.Result
	Message  string
	Error    string
	Repos    []VerifyRepoResult
	Duration time.Duration
}

// VerifyConfigured reports whether a restore test can run: in pipe mode it
// needs VERIFY_SCRIPT, because only the application knows what valid data is.
func (e *Engine) VerifyConfigured() bool {
	return !e.cfg.PipeMode() || e.cfg.Verify.Script != ""
}

// Verify tests that the latest regular snapshot can be restored and the data
// is intact: restic restores it (or a random sample of its files) into a
// sandbox and compares the result with the repository. A VERIFY_SCRIPT can
// check the restored data further. With VERIFY_COPY the secondary repository
// is tested too.
func (e *Engine) Verify(ctx context.Context, req service.VerifyRequest, sink Sink) VerifyResult {
	start := e.now()
	ctx, cancel := withTimeout(ctx, e.cfg.Timeouts.Verify)
	defer cancel()
	sink.phase("verify")

	mode := req.Mode
	if mode == "" {
		mode = e.cfg.Verify.Mode
	}
	repos := []struct {
		name string
		repo *restic.Repo
	}{{"main", e.main}}
	if e.cfg.Verify.Copy && e.second != nil {
		repos = append(repos, struct {
			name string
			repo *restic.Repo
		}{"copy", e.second})
	}

	res := VerifyResult{Result: service.ResultSkipped}
	var errs []error
	for _, r := range repos {
		rr := e.verifyRepo(ctx, r.repo, r.name, mode, sink)
		if rr.Error != "" {
			errs = append(errs, fmt.Errorf("%s repository: %s", r.name, rr.Error))
		}
		res.Repos = append(res.Repos, rr)
		res.Result = res.Result.Worse(rr.Result)
		if res.Message == "" || !rr.Result.OK() {
			res.Message = rr.Message
		}
	}
	if err := errors.Join(errs...); err != nil {
		res.Error = err.Error()
	}
	res.Duration = e.now().Sub(start)
	return res
}

func (e *Engine) verifyRepo(ctx context.Context, repo *restic.Repo, name, mode string, sink Sink) VerifyRepoResult {
	rr := VerifyRepoResult{Repo: name, Result: service.ResultSuccess}
	fail := func(err error, format string, args ...any) VerifyRepoResult {
		rr.Result, rr.Error = classify(ctx, err), err.Error()
		rr.Message = fmt.Sprintf(format, args...)
		return rr
	}
	skip := func(format string, args ...any) VerifyRepoResult {
		rr.Result, rr.Message = service.ResultSkipped, fmt.Sprintf(format, args...)
		sink.logf("verify %s: %s", name, rr.Message)
		return rr
	}

	if !e.VerifyConfigured() {
		return skip("restore test not configured: set VERIFY_SCRIPT to test restores in pipe mode")
	}
	snaps, err := repo.Snapshots(ctx)
	if err != nil {
		return fail(err, "cannot list the snapshots")
	}
	snap, err := e.resolve(snaps, "latest")
	if err != nil {
		return skip("there is no snapshot to test yet")
	}
	rr.Snapshot = snap.ID
	sink.logf("verify %s: testing snapshot %s", name, short(snap.ID))

	if e.cfg.PipeMode() {
		return e.verifyPipe(ctx, repo, snap, rr, sink)
	}
	return e.verifyFiles(ctx, repo, snap, mode, rr, sink)
}

// verifyPipe streams the snapshot into VERIFY_SCRIPT.
func (e *Engine) verifyPipe(ctx context.Context, repo *restic.Repo, snap restic.Snapshot, rr VerifyRepoResult, sink Sink) VerifyRepoResult {
	file := "/" + e.cfg.Pipe.InFilename
	if len(snap.Paths) == 1 {
		file = snap.Paths[0]
	}
	r := *e.hooks
	r.Output = func(_, line string) { sink.logf("verify-script: %s", line) }
	err := r.Pipe(ctx, "verify-script", e.cfg.Verify.Script, nil, func(w io.Writer) error {
		return repo.Dump(ctx, snap.ID, file, w)
	})
	if err != nil {
		rr.Result, rr.Error = classify(ctx, err), err.Error()
		rr.Message = "the restore test failed"
		return rr
	}
	rr.Message = fmt.Sprintf("snapshot %s was streamed to the verify script and accepted", short(snap.ID))
	return rr
}

// verifyFiles restores the snapshot, or a sample of it, into a sandbox.
func (e *Engine) verifyFiles(ctx context.Context, repo *restic.Repo, snap restic.Snapshot, mode string, rr VerifyRepoResult, sink Sink) VerifyRepoResult {
	fail := func(err error, msg string) VerifyRepoResult {
		rr.Result, rr.Error, rr.Message = classify(ctx, err), err.Error(), msg
		return rr
	}

	base := e.cfg.Verify.Dir
	if base == "" {
		base = os.TempDir()
	}
	if err := os.MkdirAll(base, 0o700); err != nil {
		return fail(err, "cannot create the sandbox directory")
	}
	sandbox, err := os.MkdirTemp(base, "mcs-backup-verify-")
	if err != nil {
		return fail(err, "cannot create the sandbox")
	}
	// The sandbox must never be inside the live data: it is deleted afterwards.
	if within(e.cfg.Root, sandbox) {
		_ = os.Remove(sandbox)
		return fail(fmt.Errorf("sandbox %s is inside BACKUP_ROOT", sandbox), "refusing to run the restore test")
	}
	defer forceRemove(sandbox)

	opts := restic.RestoreOptions{Snapshot: snap.ID, Target: sandbox, Verify: true, Extra: e.cfg.Restic.Restore}
	var need int64
	var sampled int
	switch mode {
	case VerifyFull:
		st, err := repo.Stats(ctx, restic.StatsRestoreSize, snap.ID)
		if err != nil {
			return fail(err, "cannot determine the size of the snapshot")
		}
		need = st.TotalSize
	default:
		files, err := e.sampleFiles(ctx, repo, snap)
		if err != nil {
			return fail(err, "cannot read the file list of the snapshot")
		}
		if len(files) == 0 {
			rr.Result, rr.Message = service.ResultSkipped, "the snapshot contains no files to test"
			return rr
		}
		list, err := os.CreateTemp(base, "mcs-backup-verify-list-")
		if err != nil {
			return fail(err, "cannot write the file list")
		}
		defer os.Remove(list.Name())
		for _, f := range files {
			need += f.Size
			fmt.Fprintln(list, escapePattern(f.Path))
		}
		if err := list.Close(); err != nil {
			return fail(err, "cannot write the file list")
		}
		opts.IncludeFile = list.Name()
		sampled = len(files)
		rr.Files, rr.Bytes = len(files), need
	}

	if free, err := freeBytes(sandbox); err == nil && int64(float64(need)*spaceReserve) > free {
		rr.Result = service.ResultSkipped
		rr.Message = fmt.Sprintf("not enough space for the restore test: %s needed, %s free in %s", formatBytes(need), formatBytes(free), base)
		sink.logf("verify: %s", rr.Message)
		return rr
	}

	sink.logf("restoring into %s and verifying", sandbox)
	var sum restic.RestoreSummary
	err = e.retryLocked(ctx, repo, e.cfg.AppendOnly, sink, func() error {
		var err error
		sum, err = repo.Restore(ctx, opts, func(s restic.RestoreStatus) {
			sink.progress(service.Progress{Percent: s.PercentDone, FilesDone: s.FilesRestored, FilesTotal: s.TotalFiles, BytesDone: s.BytesRestored, BytesTotal: s.TotalBytes})
		})
		return err
	})
	if err != nil {
		return fail(err, "the restore test failed")
	}
	if sampled > 0 && sum.FilesRestored < int64(sampled) {
		return fail(fmt.Errorf("restored %d entries for %d sampled files", sum.FilesRestored, sampled), "the restore test failed")
	}
	if mode == VerifyFull {
		rr.Files, rr.Bytes = int(sum.FilesRestored), sum.BytesRestored
	}

	if script := e.cfg.Verify.Script; script != "" {
		r := *e.hooks
		r.Dir = sandbox
		r.Output = func(_, line string) { sink.logf("verify-script: %s", line) }
		if err := r.Run(ctx, "verify-script", script, nil, "MCS_BACKUP_VERIFY_DIR="+sandbox); err != nil {
			return fail(err, "the verify script rejected the restored data")
		}
	}
	rr.Message = fmt.Sprintf("snapshot %s: %d files restored and verified", short(snap.ID), rr.Files)
	return rr
}

// sampleFiles picks random files of a snapshot: at most VERIFY_SAMPLE_FILES
// files and about VERIFY_SAMPLE_BYTES bytes. Reservoir sampling gives every
// file the same chance without holding the whole file list in memory.
func (e *Engine) sampleFiles(ctx context.Context, repo *restic.Repo, snap restic.Snapshot) ([]restic.Node, error) {
	k := e.cfg.Verify.SampleFiles
	reservoir := make([]restic.Node, 0, k)
	seen := 0
	for n, err := range repo.Ls(ctx, snap.ID) {
		if err != nil {
			return nil, err
		}
		if n.Type != "file" {
			continue
		}
		seen++
		if len(reservoir) < k {
			reservoir = append(reservoir, n)
		} else if j := e.rnd.IntN(seen); j < k {
			reservoir[j] = n
		}
	}

	e.rnd.Shuffle(len(reservoir), func(i, j int) { reservoir[i], reservoir[j] = reservoir[j], reservoir[i] })
	var total int64
	for i, n := range reservoir {
		if i > 0 && total+n.Size > e.cfg.Verify.SampleBytes {
			return reservoir[:i], nil
		}
		total += n.Size
	}
	return reservoir, nil
}

// escapePattern escapes the characters that restic's include patterns treat
// specially, so that a file name is matched literally.
func escapePattern(p string) string {
	var b strings.Builder
	for _, r := range p {
		if strings.ContainsRune(`\*?[`, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// within reports whether path is dir itself or lies below it.
func within(dir, path string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// forceRemove deletes a directory tree even if restic restored read-only
// directories.
func forceRemove(dir string) {
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			_ = os.Chmod(p, 0o700) //nolint:gosec // directories need the owner bits to be deleted
		}
		return nil
	})
	_ = os.RemoveAll(dir)
}
