//go:build integration

package integration

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/noris-network/mcs-backup/v2/internal/config"
	"github.com/noris-network/mcs-backup/v2/internal/daemon"
	"github.com/noris-network/mcs-backup/v2/internal/engine"
	"github.com/noris-network/mcs-backup/v2/internal/notify"
	"github.com/noris-network/mcs-backup/v2/internal/service"
)

var (
	resticBin     = os.Getenv("RESTIC_BIN")
	restServerBin = os.Getenv("REST_SERVER_BIN")
	mcsBin        string // the compiled mcs-backup, built once by TestMain
)

func TestMain(m *testing.M) {
	if resticBin == "" {
		println("RESTIC_BIN is not set: skipping the integration tests")
		os.Exit(0)
	}
	dir, err := os.MkdirTemp("", "mcs-backup-it")
	if err != nil {
		panic(err)
	}
	mcsBin = filepath.Join(dir, "mcs-backup")
	build := exec.Command("go", "build", "-o", mcsBin, "github.com/noris-network/mcs-backup/v2/cmd/mcs-backup")
	build.Stdout, build.Stderr = os.Stdout, os.Stderr
	if err := build.Run(); err != nil {
		panic(err)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// syncBuf is a bytes.Buffer that can be written and read concurrently.
type syncBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// inst is one mcs-backup instance with a real repository and a real restic.
type inst struct {
	t    *testing.T
	root string // BACKUP_ROOT
	repo string // the repository (path or URL)
	home string // scratch space of the test
	env  []string

	cfg *config.Config
	eng *engine.Engine
	d   *daemon.Daemon
	log *syncBuf

	cancel context.CancelFunc
	done   chan error
	notif  *notify.Notifier
}

// newInst prepares an instance. extra is KEY=VALUE pairs that add to or
// replace the defaults.
func newInst(t *testing.T, extra ...string) *inst {
	t.Helper()
	t.Parallel() // opening a repository costs restic about half a second of key derivation
	home := t.TempDir()
	i := &inst{t: t, root: filepath.Join(home, "data"), repo: filepath.Join(home, "repo"), home: home, log: &syncBuf{}}
	if err := os.MkdirAll(i.root, 0o755); err != nil {
		t.Fatal(err)
	}
	i.env = append([]string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + home, "TZ=UTC",
		"RESTIC_REPOSITORY=" + i.repo, "RESTIC_PASSWORD=it-password", "RESTIC_CACHE_DIR=" + filepath.Join(home, "cache"),
		"BACKUP_ROOT=" + i.root, "MCS_BACKUP_RESTIC=" + resticBin,
		`RETENTION_POLICY={"last":10}`,
		"MCS_BACKUP_STATE_DIR=" + filepath.Join(home, "state"), "MCS_BACKUP_SOCKET=" + filepath.Join(home, "s.sock"),
		"PRUNE_SCHEDULE=", "CHECK_SCHEDULE=", "VERIFY_SCHEDULE=",
		"VERIFY_DIR=" + filepath.Join(home, "verify"),
	}, extra...)
	return i
}

// set adds or replaces variables; call before start.
func (i *inst) set(kv ...string) { i.env = append(i.env, kv...) }

// start loads the configuration and runs the daemon until the test ends.
func (i *inst) start() {
	i.t.Helper()
	cfg, err := config.Load(i.env)
	if err != nil {
		i.t.Fatalf("config: %v", err)
	}
	i.cfg = cfg
	log := slog.New(slog.NewTextHandler(i.log, &slog.HandlerOptions{Level: slog.LevelDebug}))
	i.eng = engine.NewFromConfig(cfg, log)
	var n daemon.Notifier
	if cfg.Notify.Command != "" {
		i.notif = notify.New(notify.Config{Command: cfg.Notify.Command, On: cfg.Notify.On, Timeout: cfg.Notify.Timeout, Host: cfg.Host, Env: cfg.Environ(), Log: log})
		n = i.notif
	}
	i.d = daemon.New(daemon.Options{
		Config: cfg, Engine: i.eng, Notifier: n, Logger: log, Version: "it", HTTPAddr: "127.0.0.1:0",
		Env:        func(reveal bool) (service.EnvInfo, error) { return engine.EnvInfo(cfg, reveal), nil },
		MinBackoff: 100 * time.Millisecond, MaxBackoff: time.Second,
	})
	ctx, cancel := context.WithCancel(context.Background())
	i.cancel = cancel
	i.done = make(chan error, 1)
	go func() { i.done <- i.d.Run(ctx) }()
	i.t.Cleanup(func() {
		i.stop()
		if os.Getenv("IT_LOG") != "" {
			i.t.Logf("service log:\n%s", i.log)
		}
	})
	i.waitFor("the service to get ready", 30*time.Second, func() bool {
		st, err := i.d.Status(ctx)
		return err == nil && st.Ready
	})
}

// stop shuts the daemon down and waits for Run to return.
func (i *inst) stop() {
	if i.cancel == nil {
		return
	}
	i.cancel()
	select {
	case err := <-i.done:
		if err != nil && !errors.Is(err, context.Canceled) {
			i.t.Errorf("Run: %v\n%s", err, i.log)
		}
	case <-time.After(60 * time.Second):
		i.t.Errorf("the daemon did not stop\n%s", i.log)
	}
	i.cancel = nil
}

func (i *inst) ctx() context.Context { return service.WithSource(i.t.Context(), "it") }

func (i *inst) waitFor(what string, d time.Duration, cond func() bool) {
	i.t.Helper()
	deadline := time.Now().Add(d)
	for !cond() {
		if time.Now().After(deadline) {
			i.t.Fatalf("timed out waiting for %s\n%s", what, i.log)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// finish waits for the operation op to end and returns its result.
func (i *inst) finish(op service.Operation) service.RunResult {
	i.t.Helper()
	var res service.RunResult
	i.waitFor("the "+string(op.Kind)+" to finish", 120*time.Second, func() bool {
		st, err := i.d.Status(i.ctx())
		if err != nil || st.Operation != nil {
			return false
		}
		res = st.LastRuns[op.Kind]
		return res.ID == op.ID
	})
	return res
}

// backup runs a manual backup and returns its result.
func (i *inst) backup(note string, keep bool) service.RunResult {
	i.t.Helper()
	op, err := i.d.StartBackup(i.ctx(), service.BackupRequest{Note: note, Keep: keep})
	if err != nil {
		i.t.Fatalf("StartBackup: %v", err)
	}
	return i.finish(op)
}

// mustBackup runs a backup that has to succeed.
func (i *inst) mustBackup(note string, keep bool) service.RunResult {
	i.t.Helper()
	res := i.backup(note, keep)
	if res.Result != service.ResultSuccess {
		i.t.Fatalf("backup: %s: %s %s\n%s", res.Result, res.Message, res.Error, i.log)
	}
	return res
}

// restore starts a restore and returns its result.
func (i *inst) restore(req service.RestoreRequest) service.RunResult {
	i.t.Helper()
	op, err := i.d.StartRestore(i.ctx(), req)
	if err != nil {
		i.t.Fatalf("StartRestore: %v", err)
	}
	return i.finish(op)
}

func (i *inst) snapshots() []service.Snapshot {
	i.t.Helper()
	s, err := i.d.Snapshots(i.ctx())
	if err != nil {
		i.t.Fatalf("Snapshots: %v", err)
	}
	return s
}

func (i *inst) status() service.Status {
	i.t.Helper()
	st, err := i.d.Status(i.ctx())
	if err != nil {
		i.t.Fatalf("Status: %v", err)
	}
	return st
}

// restic runs the real restic against the repository of this instance.
func (i *inst) restic(args ...string) string {
	i.t.Helper()
	out, err := i.resticErr(args...)
	if err != nil {
		i.t.Fatalf("restic %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

func (i *inst) resticErr(args ...string) (string, error) {
	cmd := exec.CommandContext(i.t.Context(), resticBin, args...)
	cmd.Env = append(os.Environ(), "RESTIC_REPOSITORY="+i.repo, "RESTIC_PASSWORD=it-password", "RESTIC_CACHE_DIR="+filepath.Join(i.home, "cache"))
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.String(), err
}

// ---- files ----

func (i *inst) write(rel, content string) {
	i.t.Helper()
	path := filepath.Join(i.root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		i.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil { //nolint:gosec // test data
		i.t.Fatal(err)
	}
}

func (i *inst) read(rel string) string {
	i.t.Helper()
	b, err := os.ReadFile(filepath.Join(i.root, rel)) //nolint:gosec // test data
	if err != nil {
		i.t.Fatal(err)
	}
	return string(b)
}

func (i *inst) exists(rel string) bool {
	_, err := os.Stat(filepath.Join(i.root, rel))
	return err == nil
}

func (i *inst) remove(rel string) {
	i.t.Helper()
	if err := os.RemoveAll(filepath.Join(i.root, rel)); err != nil {
		i.t.Fatal(err)
	}
}

// script writes an executable shell script into the scratch space and
// returns its path.
func (i *inst) script(name, body string) string {
	i.t.Helper()
	path := filepath.Join(i.home, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil { //nolint:gosec // test data
		i.t.Fatal(err)
	}
	return path
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil { //nolint:gosec // test data
		t.Fatal(err)
	}
}

func fileContent(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // test data
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func tags(s service.Snapshot) string { return strings.Join(s.Tags, ",") }

// latestRegular returns the newest snapshot that is neither suspicious nor a safety snapshot.
func latestRegular(snaps []service.Snapshot) service.Snapshot {
	var best service.Snapshot
	for _, s := range snaps {
		if !s.Suspicious && !s.PreRestore && s.Time.After(best.Time) {
			best = s
		}
	}
	return best
}

// holdLock starts a restic backup that blocks on its input and waits until it
// holds its lock in the repository. The returned function freezes the process
// (SIGSTOP): it stays alive, as restic's own stale check for a dead process
// on this host would otherwise discard the lock, but it never renews the lock.
func (i *inst) holdLock() (freeze func()) {
	i.t.Helper()
	cmd := exec.Command(resticBin, "backup", "--stdin-from-command", "--stdin-filename", "blocked", "--", "sleep", "300") //nolint:gosec,noctx // killed in the cleanup
	cmd.Env = append(os.Environ(), "RESTIC_REPOSITORY="+i.repo, "RESTIC_PASSWORD=it-password", "RESTIC_CACHE_DIR="+filepath.Join(i.home, "cache"))
	if err := cmd.Start(); err != nil {
		i.t.Fatal(err)
	}
	i.t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGCONT)
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	i.waitFor("restic to take its lock", 30*time.Second, func() bool {
		out, err := i.resticErr("list", "locks", "--no-lock")
		return err == nil && strings.TrimSpace(out) != ""
	})
	return func() {
		if err := cmd.Process.Signal(syscall.SIGSTOP); err != nil {
			i.t.Fatal(err)
		}
	}
}
