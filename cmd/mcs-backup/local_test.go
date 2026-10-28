package main

import (
	"context"
	"encoding/json/v2"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/noris-network/mcs-backup/v2/internal/resticfake"
	"github.com/noris-network/mcs-backup/v2/internal/service"
)

// shortTemp returns a directory with a path short enough for a Unix socket.
func shortTemp(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "mcsb")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// localEnv is a valid environment around a fake restic.
func localEnv(t *testing.T, fake *resticfake.Fake, extra ...string) (env []string, root string) {
	t.Helper()
	root = t.TempDir()
	tmp := shortTemp(t)
	return append([]string{
		"PATH=" + os.Getenv("PATH"), "TZ=UTC", "RESTIC_REPOSITORY=/repo", "RESTIC_PASSWORD=pw-secret", "AWS_SECRET_ACCESS_KEY=aws-secret",
		`RETENTION_POLICY={"last":3}`, "BACKUP_ROOT=" + root, "MCS_BACKUP_RESTIC=" + fake.Bin,
		"MCS_BACKUP_SOCKET=" + filepath.Join(tmp, "s.sock"), "MCS_BACKUP_STATE_DIR=" + filepath.Join(tmp, "state"),
	}, extra...), root
}

const resticVersion019 = "restic 0.19.1 compiled with go1.26 on linux/amd64"

func TestConfigCheck(t *testing.T) {
	fake := resticfake.New(t)
	c := newCLI(t)
	c.env, _ = localEnv(t, fake, "CRON_SCHEDULE=0 2 * * *", "LOKI_URL=http://loki")

	if code := c.run("config", "check"); code != 0 {
		t.Fatalf("exit %d: %s", code, c.err())
	}
	for _, want := range []string{"RESTIC_REPOSITORY", "/repo", "keeps the last 3 snapshots", "0 2 * * *", "PRUNE_SCHEDULE", "0 3 * * 0",
		"warning: LOKI_URL is no longer supported", "The configuration is valid", "--connect"} {
		if !strings.Contains(c.out(), want) {
			t.Errorf("output lacks %q:\n%s", want, c.out())
		}
	}
	for _, secret := range []string{"pw-secret", "aws-secret"} {
		if strings.Contains(c.out(), secret) {
			t.Errorf("the output leaks %q", secret)
		}
	}
	if len(fake.Calls(t)) != 0 {
		t.Error("restic was run without --connect")
	}

	c.run("--json", "config", "check")
	var out struct {
		Settings []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"settings"`
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal(c.stdout.Bytes(), &out); err != nil || len(out.Settings) < 20 || len(out.Warnings) != 1 {
		t.Errorf("JSON: %v\n%s", err, c.out())
	}
}

func TestConfigCheckReportsEveryProblem(t *testing.T) {
	c := newCLI(t)
	c.env = []string{"PATH=/bin", "BACKUP_ROOT=/does/not/exist", "CRON_SCHEDULE=every day", "BACKUP_TIMEOUT=soon"}
	if code := c.run("config", "check"); code != exitCodeUsage {
		t.Errorf("exit %d, want %d", code, exitCodeUsage)
	}
	for _, want := range []string{"invalid configuration", "RESTIC_REPOSITORY", "RESTIC_PASSWORD", "RETENTION_POLICY", "BACKUP_ROOT", "CRON_SCHEDULE", "BACKUP_TIMEOUT"} {
		if !strings.Contains(c.err(), want) {
			t.Errorf("stderr lacks %q:\n%s", want, c.err())
		}
	}
}

func TestConfigCheckConnect(t *testing.T) {
	setup := func(t *testing.T) (*cli, *resticfake.Fake) {
		fake := resticfake.New(t)
		fake.On(t, "version", resticfake.Response{Stdout: resticVersion019})
		fake.On(t, "cat config", resticfake.Response{Stdout: `{"version":2,"id":"0123456789abcdef","chunker_polynomial":"x"}`})
		fake.On(t, "snapshots", resticfake.Response{Stdout: "[]"})
		c := newCLI(t)
		c.env, _ = localEnv(t, fake)
		return c, fake
	}

	t.Run("ok", func(t *testing.T) {
		c, _ := setup(t)
		if code := c.run("config", "check", "--connect"); code != 0 {
			t.Fatalf("exit %d: %s%s", code, c.out(), c.err())
		}
		for _, want := range []string{"restic 0.19.1", "main repository: ok (id 01234567)", "a lock can be taken and released"} {
			if !strings.Contains(c.out(), want) {
				t.Errorf("output lacks %q:\n%s", want, c.out())
			}
		}
	})

	t.Run("info does the same", func(t *testing.T) {
		c, fake := setup(t)
		if code := c.run("info"); code != 0 || !strings.Contains(c.out(), "main repository: ok") || len(fake.CallsTo(t, "cat")) != 1 {
			t.Errorf("exit %d\n%s", code, c.out())
		}
	})

	t.Run("json", func(t *testing.T) {
		c, _ := setup(t)
		if code := c.run("--json", "info"); code != 0 {
			t.Fatal(code, c.err())
		}
		var out struct {
			Connect struct {
				ResticVersion string `json:"restic_version"`
				Repositories  []struct {
					Repository string `json:"repository"`
					ID         string `json:"id"`
				} `json:"repositories"`
			} `json:"connect"`
		}
		if err := json.Unmarshal(c.stdout.Bytes(), &out); err != nil || out.Connect.ResticVersion != "0.19.1" || out.Connect.Repositories[0].ID != "0123456789abcdef" {
			t.Errorf("JSON: %v\n%s", err, c.out())
		}
	})

	t.Run("repository does not exist yet", func(t *testing.T) {
		c, fake := setup(t)
		fake.On(t, "cat config", resticfake.Response{Stderr: "Fatal: repository does not exist\n", Exit: 10})
		if code := c.run("info"); code != 0 || !strings.Contains(c.out(), "does not exist yet") {
			t.Errorf("exit %d\n%s", code, c.out())
		}
	})

	t.Run("wrong password", func(t *testing.T) {
		c, fake := setup(t)
		fake.On(t, "cat config", resticfake.Response{Stderr: "Fatal: wrong password or no key found\n", Exit: 12})
		if code := c.run("info"); code != exitCodeError || !strings.Contains(c.out(), "NOT USABLE") || !strings.Contains(c.out(), "wrong password") {
			t.Errorf("exit %d\n%s", code, c.out())
		}
	})

	t.Run("a lock cannot be released", func(t *testing.T) {
		c, fake := setup(t)
		fake.On(t, "snapshots", resticfake.Response{Stderr: "Fatal: 403 forbidden\n", Exit: 1})
		if code := c.run("info"); code != exitCodeError || !strings.Contains(c.out(), "deleting under locks/") {
			t.Errorf("exit %d\n%s", code, c.out())
		}
	})

	t.Run("restic too old", func(t *testing.T) {
		c, fake := setup(t)
		fake.On(t, "version", resticfake.Response{Stdout: "restic 0.16.5 compiled with go1.22 on linux/amd64"})
		if code := c.run("info"); code != exitCodeError || !strings.Contains(c.err(), "too old") {
			t.Errorf("exit %d\n%s", code, c.err())
		}
	})
}

func TestResticPassthrough(t *testing.T) {
	fake := resticfake.New(t)
	fake.On(t, "snapshots", resticfake.Response{Stdout: "[]\n"})
	fake.On(t, "forget", resticfake.Response{Stderr: "Fatal: boom\n", Exit: 3})
	c := newCLI(t)
	var root string
	c.env, root = localEnv(t, fake, "S3_USE_V1=true")

	if code := c.run("restic", "snapshots", "--json", "-q", "--host", "x"); code != 0 {
		t.Fatalf("exit %d: %s", code, c.err())
	}
	if c.out() != "[]\n" {
		t.Errorf("stdout = %q", c.out())
	}
	call := fake.CallsTo(t, "snapshots")[0]
	if want := []string{"-o", "s3.list-objects-v1=true", "snapshots", "--json", "-q", "--host", "x"}; !slices.Equal(call, want) {
		t.Errorf("args = %q, want %q: the flags are restic's, not ours", call, want)
	}
	env := fake.LastEnv(t)
	resolved, _ := filepath.EvalSymlinks(root)
	if env["RESTIC_REPOSITORY"] != "/repo" || env["RESTIC_PASSWORD"] != "pw-secret" || fake.LastDir(t) != resolved {
		t.Errorf("env %v, dir %q (want %q)", env["RESTIC_REPOSITORY"], fake.LastDir(t), resolved)
	}

	if code := c.run("restic", "forget"); code != 3 {
		t.Errorf("the exit code of restic must be passed on: %d", code)
	}
	if !strings.Contains(c.err(), "Fatal: boom") || strings.Contains(c.err(), "mcs-backup:") {
		t.Errorf("stderr = %q", c.err())
	}
}

func snapshotsJSON(hostname string, tags ...string) string {
	b, _ := json.Marshal([]map[string]any{{
		"id": "abcdef0123456789", "short_id": "abcdef01", "time": "2026-03-01T10:00:00Z", "hostname": hostname,
		"paths": []string{"/data"}, "tags": tags,
	}})
	return string(b)
}

func TestRestoreIfEmpty(t *testing.T) {
	fake := resticfake.New(t)
	fake.On(t, "snapshots", resticfake.Response{Stdout: snapshotsJSON("mcs-backup", "mcs-backup")})
	fake.On(t, "stats", resticfake.Response{Stdout: `{"total_size":14,"total_file_count":2,"snapshots_count":1}`})
	fake.On(t, "restore", resticfake.Response{Stdout: `{"message_type":"summary","total_files":2,"files_restored":2,"total_bytes":14,"bytes_restored":14}` + "\n"})
	newCLIWith := func(extra ...string) (*cli, string) {
		c := newCLI(t)
		var root string
		c.env, root = localEnv(t, fake, extra...)
		return c, root
	}

	t.Run("data present: the repository is not even contacted", func(t *testing.T) {
		before := len(fake.Calls(t))
		c, root := newCLIWith()
		if err := os.WriteFile(filepath.Join(root, "data.db"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if code := c.run("restore-if-empty"); code != 0 || !strings.Contains(c.out(), "is not empty: nothing to restore") {
			t.Errorf("exit %d\n%s%s", code, c.out(), c.err())
		}
		if len(fake.Calls(t)) != before {
			t.Error("restic was run")
		}
	})

	t.Run("empty: restores", func(t *testing.T) {
		c, root := newCLIWith()
		if err := os.MkdirAll(filepath.Join(root, "lost+found"), 0o700); err != nil {
			t.Fatal(err)
		}
		if code := c.run("restore-if-empty"); code != 0 || !strings.Contains(c.out(), "restored the latest snapshot into "+root) {
			t.Fatalf("exit %d\n%s%s", code, c.out(), c.err())
		}
		call := fake.CallsTo(t, "restore")[0]
		if !slices.Contains(call, "abcdef0123456789") || !slices.Contains(call, root) {
			t.Errorf("restore args = %q", call)
		}
	})

	t.Run("no repository yet", func(t *testing.T) {
		fake.On(t, "snapshots", resticfake.Response{Stderr: "Fatal: repository does not exist\n", Exit: 10})
		c, _ := newCLIWith()
		if code := c.run("restore-if-empty"); code != 0 || !strings.Contains(c.out(), "fresh installation") {
			t.Errorf("exit %d\n%s", code, c.out())
		}
		if len(fake.CallsTo(t, "init")) != 0 {
			t.Error("restore-if-empty must never create a repository")
		}
	})

	t.Run("repository unreachable", func(t *testing.T) {
		fake.On(t, "snapshots", resticfake.Response{Stderr: "Fatal: connection refused\n", Exit: 1})
		c, _ := newCLIWith()
		if code := c.run("restore-if-empty"); code != exitCodeError || !strings.Contains(c.err(), "connection refused") {
			t.Errorf("exit %d: %s", code, c.err())
		}
	})

	t.Run("pipe mode", func(t *testing.T) {
		script := filepath.Join(shortTemp(t), "s.sh")
		if err := os.WriteFile(script, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		c, _ := newCLIWith("PIPE_IN_SCRIPT="+script, "PIPE_OUT_SCRIPT="+script)
		if code := c.run("restore-if-empty"); code != exitCodeUsage || !strings.Contains(c.err(), "pipe mode") {
			t.Errorf("exit %d: %s", code, c.err())
		}
	})
}

// freePort returns a TCP port nobody listens on.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// runServe starts `serve` and returns the app, a stop function and the exit code channel.
func runServe(t *testing.T, env []string) (stop func() int, out *syncBuffer) {
	t.Helper()
	out = &syncBuffer{}
	a := newApp()
	a.stdout, a.stderr, a.stdin, a.interactive = out, out, strings.NewReader(""), false
	a.environ = func() []string { return env }
	a.getenv = func(k string) string {
		for _, kv := range env {
			if v, ok := strings.CutPrefix(kv, k+"="); ok {
				return v
			}
		}
		return ""
	}
	ctx, cancel := context.WithCancel(t.Context())
	code := make(chan int, 1)
	go func() { code <- a.run(ctx, []string{"serve"}) }()
	return func() int {
		cancel()
		select {
		case c := <-code:
			return c
		case <-time.After(30 * time.Second):
			t.Fatal("serve did not stop")
			return -1
		}
	}, out
}

type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestServe(t *testing.T) {
	fake := resticfake.New(t)
	fake.On(t, "version", resticfake.Response{Stdout: resticVersion019})
	fake.On(t, "cat config", resticfake.Response{Stdout: `{"version":2,"id":"0123456789abcdef","chunker_polynomial":"x"}`})
	fake.On(t, "snapshots", resticfake.Response{Stdout: snapshotsJSON("mcs-backup", "mcs-backup")})
	fake.On(t, "stats", resticfake.Response{Stdout: `{"total_size":4096,"total_file_count":2,"snapshots_count":1}`})
	fake.On(t, "list locks", resticfake.Response{})
	env, _ := localEnv(t, fake, "BACKUP_HTTP_PORT="+strconv.Itoa(freePort(t)), "PRUNE_SCHEDULE=", "CHECK_SCHEDULE=", "VERIFY_SCHEDULE=")
	socket := ""
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "MCS_BACKUP_SOCKET="); ok {
			socket = v
		}
	}

	stop, out := runServe(t, env)
	client := newApp()
	client.getenv = func(string) string { return socket }
	client.stdout, client.stderr = &strings.Builder{}, &strings.Builder{}
	svc := client.dial(socket)

	// the service comes up and reads the repository
	var st service.Status
	waitFor(t, 20*time.Second, func() bool {
		var err error
		st, err = svc.Status(t.Context())
		return err == nil && st.Ready
	})
	if !st.Repository.Up || st.Repository.ID != "0123456789abcdef" || st.Repository.Snapshots != 1 || st.Host != "mcs-backup" {
		t.Errorf("status = %+v", st)
	}
	snaps, err := svc.Snapshots(t.Context())
	if err != nil || len(snaps) != 1 || snaps[0].ShortID != "abcdef01" {
		t.Errorf("Snapshots() = %+v, %v", snaps, err)
	}

	// the CLI talks to it over the socket
	a := newApp()
	var stdout strings.Builder
	a.stdout, a.stderr, a.getenv = &stdout, &stdout, func(string) string { return socket }
	if code := a.run(t.Context(), []string{"snapshots"}); code != 0 || !strings.Contains(stdout.String(), "abcdef01") {
		t.Errorf("snapshots over the socket: exit %d\n%s", code, stdout.String())
	}

	// the same socket cannot be taken twice
	second := newApp()
	var secondOut strings.Builder
	second.stdout, second.stderr = &secondOut, &secondOut
	second.environ = func() []string { return env }
	second.getenv = func(string) string { return "" }
	if code := second.run(t.Context(), []string{"serve"}); code != exitCodeError || !strings.Contains(secondOut.String(), "instance already running") {
		t.Errorf("second serve: exit %d\n%s", code, secondOut.String())
	}
	if st, err := svc.Status(t.Context()); err != nil || !st.Ready {
		t.Errorf("the first instance was disturbed: %v", err)
	}

	if code := stop(); code != 0 {
		t.Errorf("serve exited with %d\n%s", code, out.String())
	}
	if _, err := os.Stat(socket); !os.IsNotExist(err) {
		t.Errorf("the socket was left behind: %v", err)
	}
	if !strings.Contains(out.String(), `"msg":"mcs-backup starting"`) {
		t.Errorf("the service logs JSON to stdout:\n%s", out.String())
	}
	if strings.Contains(out.String(), "pw-secret") || strings.Contains(out.String(), "aws-secret") {
		t.Errorf("the log leaks a secret:\n%s", out.String())
	}
}

func TestServeRefusesAnInvalidConfiguration(t *testing.T) {
	out := &syncBuffer{}
	a := newApp()
	a.stdout, a.stderr = out, out
	a.environ = func() []string { return []string{"PATH=/bin"} }
	a.getenv = func(string) string { return "" }
	if code := a.run(t.Context(), []string{"serve"}); code != exitCodeUsage || !strings.Contains(out.String(), "invalid configuration") {
		t.Errorf("exit %d\n%s", code, out.String())
	}
}

func TestServeWithAnUnsupportedRestic(t *testing.T) {
	fake := resticfake.New(t)
	fake.On(t, "version", resticfake.Response{Stdout: "restic 0.15.0 compiled with go1.20 on linux/amd64"})
	env, _ := localEnv(t, fake, "BACKUP_HTTP_PORT="+strconv.Itoa(freePort(t)))
	stop, out := runServe(t, env)
	waitFor(t, 20*time.Second, func() bool { return strings.Contains(out.String(), "too old") })
	if code := stop(); code != exitCodeError {
		t.Errorf("exit %d, want %d\n%s", code, exitCodeError, out.String())
	}
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

func TestEnv(t *testing.T) {
	fake := resticfake.New(t)
	c := newCLI(t)
	c.env, _ = localEnv(t, fake, "RESTIC_REPOSITORY=s3:s3.example.com/bucket", "S3_USE_V1=true", "CRON_SCHEDULE=0 2 * * *")

	if code := c.run("env"); code != 0 {
		t.Fatalf("exit %d: %s", code, c.err())
	}
	for _, want := range []string{"export RESTIC_REPOSITORY='s3:s3.example.com/bucket'", "export RESTIC_PASSWORD='pw-secret'", "export AWS_SECRET_ACCESS_KEY='aws-secret'", "-o s3.list-objects-v1=true"} {
		if !strings.Contains(c.out(), want) {
			t.Errorf("output lacks %q:\n%s", want, c.out())
		}
	}
	for _, unwanted := range []string{"BACKUP_ROOT", "CRON_SCHEDULE", "MCS_BACKUP"} {
		if strings.Contains(c.out(), unwanted) {
			t.Errorf("output contains %q, which is no business of restic:\n%s", unwanted, c.out())
		}
	}

	if code := c.run("env", "--format", "docker"); code != 0 || !strings.Contains(c.out(), "docker run") || !strings.Contains(c.out(), "restic/restic -o s3.list-objects-v1=true") {
		t.Errorf("docker format: exit %d\n%s%s", code, c.out(), c.err())
	}

	if code := c.run("env", "--json"); code != 0 || !strings.Contains(c.out(), `"min_version": "0.17"`) {
		t.Errorf("json: exit %d\n%s%s", code, c.out(), c.err())
	}

	if code := c.run("env", "--format", "yaml"); code != exitCodeUsage || !strings.Contains(c.err(), "unknown format") {
		t.Errorf("bad format: exit %d\n%s", code, c.err())
	}
}
