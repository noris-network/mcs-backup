package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// testEnv returns a minimal valid environment with a fresh backup root.
func testEnv(t *testing.T, extra ...string) []string {
	t.Helper()
	return append([]string{
		"RESTIC_REPOSITORY=/tmp/repo",
		"RESTIC_PASSWORD=secret",
		"RETENTION_POLICY={\"daily\":7}",
		"BACKUP_ROOT=" + t.TempDir(),
		"TZ=UTC",
	}, extra...)
}

func writeScript(t *testing.T, name string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func mustLoad(t *testing.T, environ []string) *Config {
	t.Helper()
	c, err := Load(environ)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	return c
}

func TestLoadDefaults(t *testing.T) {
	c := mustLoad(t, testEnv(t))

	if c.Repository != "/tmp/repo" || c.Host != "mcs-backup" {
		t.Errorf("repository/host = %q/%q", c.Repository, c.Host)
	}
	if !slices.Equal(c.Paths, []string{"."}) || c.Excludes != nil {
		t.Errorf("paths/excludes = %q/%q", c.Paths, c.Excludes)
	}
	if c.Retention != (Retention{Daily: 7}) {
		t.Errorf("retention = %+v", c.Retention)
	}
	wantSched := Schedule{
		Prune: "0 3 * * 0", Check: "0 4 1 * *", Verify: "0 5 * * 6", CatchUp: true, Location: time.UTC,
	}
	if c.Schedule.Prune != wantSched.Prune || c.Schedule.Check != wantSched.Check ||
		c.Schedule.Verify != wantSched.Verify || c.Schedule.CatchUp != wantSched.CatchUp ||
		c.Schedule.Backup != "" || c.Schedule.Location.String() != "UTC" {
		t.Errorf("schedule = %+v", c.Schedule)
	}
	if want := (Timeouts{
		Backup: 12 * time.Hour, Prune: 12 * time.Hour, Check: 12 * time.Hour, Verify: 12 * time.Hour,
		PostHook: 5 * time.Minute, Shutdown: 25 * time.Second,
	}); c.Timeouts != want {
		t.Errorf("timeouts = %+v", c.Timeouts)
	}
	if want := (BackupPolicy{Retries: 2, RetryDelay: 5 * time.Minute, MaxShrink: 50, ForgetMaxDelete: 25}); c.Backup != want {
		t.Errorf("backup policy = %+v", c.Backup)
	}
	if c.Verify.Mode != "sample" || c.Verify.SampleFiles != 100 || c.Verify.SampleBytes != 256<<20 {
		t.Errorf("verify = %+v", c.Verify)
	}
	if c.Server.HTTPPort != 9000 || c.Server.Socket != filepath.Join(os.TempDir(), "mcs-backup.sock") ||
		c.Server.WebAddr != "" {
		t.Errorf("server = %+v", c.Server)
	}
	if !slices.Equal(c.Notify.On, []string{"failure", "warning", "recovered"}) || c.Notify.Timeout != 30*time.Second {
		t.Errorf("notify = %+v", c.Notify)
	}
	if c.Pipe.InFilename != "stdin" || c.PipeMode() || c.Copy != nil || c.AppendOnly {
		t.Errorf("pipe/copy/append-only = %+v / %v / %v", c.Pipe, c.Copy, c.AppendOnly)
	}
	if c.LogFormat != "json" || c.Debug || c.AutoUnlockAfter != 0 {
		t.Errorf("log/unlock = %q %v %v", c.LogFormat, c.Debug, c.AutoUnlockAfter)
	}
	if len(c.Warnings) != 0 {
		t.Errorf("unexpected warnings: %q", c.Warnings)
	}
}

func TestLoadValues(t *testing.T) {
	pre := writeScript(t, "pre", 0o755)
	pipeIn := writeScript(t, "in", 0o755)
	pipeOut := writeScript(t, "out", 0o755)
	c := mustLoad(t, testEnv(t,
		"BACKUP_PATHS=data:/etc/conf",
		"EXCLUDE_PATHS=*.tmp::cache",
		"MCS_BACKUP_HOST=db1",
		"PRE_BACKUP_SCRIPT="+pre,
		"PIPE_IN_SCRIPT="+pipeIn,
		"PIPE_OUT_SCRIPT="+pipeOut,
		"PIPE_IN_FILENAME=dump.sql",
		"CRON_SCHEDULE=5 */3 * * *",
		"PRUNE_SCHEDULE=",
		"BACKUP_RETRIES=0",
		"BACKUP_MAX_SHRINK=30%",
		"FORGET_MAX_DELETE=10",
		"AUTO_UNLOCK_AFTER=2h",
		"RESTORE_TIMEOUT=1h",
		"NOTIFY_ON=Failure, success",
		"NOTIFY_COMMAND=curl -d x https://example.com",
		"BACKUP_HTTP_PORT=9100",
		"MCS_BACKUP_WEB_ADDR=127.0.0.1:8080",
		"MCS_BACKUP_LOG_FORMAT=text",
		"MCS_BACKUP_DEBUG=true",
		"S3_USE_V1=true",
		`RESTIC_BACKUP_ARGS=--one-file-system --exclude "my dir"`,
		"METRICS_LABELS={team: storage, replicas: 3}",
		"APPEND_ONLY=true",
	))
	if !slices.Equal(c.Paths, []string{"data", "/etc/conf"}) || !slices.Equal(c.Excludes, []string{"*.tmp", "cache"}) {
		t.Errorf("paths/excludes = %q/%q", c.Paths, c.Excludes)
	}
	if c.Host != "db1" || c.Hooks.PreBackup != pre || !c.PipeMode() || c.Pipe.InFilename != "dump.sql" {
		t.Errorf("host/hooks/pipe = %q %+v %+v", c.Host, c.Hooks, c.Pipe)
	}
	if c.Schedule.Backup != "5 */3 * * *" || c.Schedule.Prune != "" {
		t.Errorf("schedule = %+v", c.Schedule)
	}
	if c.Backup.Retries != 0 || c.Backup.MaxShrink != 30 || c.Backup.ForgetMaxDelete != 10 {
		t.Errorf("backup policy = %+v", c.Backup)
	}
	if c.AutoUnlockAfter != 2*time.Hour || c.Timeouts.Restore != time.Hour {
		t.Errorf("unlock/restore timeout = %v/%v", c.AutoUnlockAfter, c.Timeouts.Restore)
	}
	if !slices.Equal(c.Notify.On, []string{"failure", "success"}) || c.Notify.Command == "" {
		t.Errorf("notify = %+v", c.Notify)
	}
	if c.Server.HTTPPort != 9100 || c.Server.WebAddr != "127.0.0.1:8080" {
		t.Errorf("server = %+v", c.Server)
	}
	if c.LogFormat != "text" || !c.Debug || !c.AppendOnly {
		t.Errorf("log/debug/append-only = %q %v %v", c.LogFormat, c.Debug, c.AppendOnly)
	}
	if !slices.Equal(c.Restic.Global, []string{"-o", "s3.list-objects-v1=true"}) ||
		!slices.Equal(c.Restic.Backup, []string{"--one-file-system", "--exclude", "my dir"}) {
		t.Errorf("restic args = %+v", c.Restic)
	}
	if c.MetricsLabels["team"] != "storage" || c.MetricsLabels["replicas"] != "3" {
		t.Errorf("labels = %v", c.MetricsLabels)
	}
}

func TestLoadRepositoryAndPassword(t *testing.T) {
	root := t.TempDir()
	base := []string{"BACKUP_ROOT=" + root, `RETENTION_POLICY={"last":3}`, "TZ=UTC"}

	t.Run("composed from base and path", func(t *testing.T) {
		c := mustLoad(t, append(slices.Clone(base),
			"RESTIC_REPOSITORY_BASE=s3:https://s3.example.com/bucket/", "RESTIC_REPOSITORY_PATH=/db", "RESTIC_PASSWORD_FILE=/run/pw"))
		if c.Repository != "s3:https://s3.example.com/bucket/db" {
			t.Errorf("repository = %q", c.Repository)
		}
		if !slices.Contains(c.Environ(), "RESTIC_REPOSITORY=s3:https://s3.example.com/bucket/db") {
			t.Errorf("composed repository missing from environ")
		}
	})
	t.Run("repository file", func(t *testing.T) {
		c := mustLoad(t, append(slices.Clone(base), "RESTIC_REPOSITORY_FILE=/run/repo", "RESTIC_PASSWORD_COMMAND=pass show x"))
		if c.Repository != "" {
			t.Errorf("repository = %q", c.Repository)
		}
	})
	t.Run("missing repository", func(t *testing.T) {
		_, err := Load(append(slices.Clone(base), "RESTIC_PASSWORD=x"))
		wantErr(t, err, "RESTIC_REPOSITORY")
	})
	t.Run("missing password", func(t *testing.T) {
		_, err := Load(append(slices.Clone(base), "RESTIC_REPOSITORY=/r"))
		wantErr(t, err, "RESTIC_PASSWORD")
	})
}

func wantErr(t *testing.T, err error, substrings ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("Load() succeeded, want error mentioning %q", substrings)
	}
	for _, s := range substrings {
		if !strings.Contains(err.Error(), s) {
			t.Errorf("error %q does not mention %q", err, s)
		}
	}
}

func TestLoadErrors(t *testing.T) {
	notExec := writeScript(t, "noexec", 0o644)
	tests := []struct {
		name string
		env  []string
		want []string
	}{
		{"no root", []string{"BACKUP_ROOT="}, []string{"BACKUP_ROOT"}},
		{"root missing", []string{"BACKUP_ROOT=/does/not/exist"}, []string{"BACKUP_ROOT"}},
		{"retention missing", []string{"RETENTION_POLICY="}, []string{"RETENTION_POLICY"}},
		{"retention empty policy", []string{`RETENTION_POLICY={"last":0}`}, []string{"RETENTION_POLICY"}},
		{"hook missing", []string{"PRE_BACKUP_SCRIPT=/does/not/exist"}, []string{"PRE_BACKUP_SCRIPT", "/does/not/exist"}},
		{"hook not executable", []string{"POST_RESTORE_SCRIPT=" + notExec}, []string{"POST_RESTORE_SCRIPT", "not executable"}},
		{"bad cron", []string{"CRON_SCHEDULE=every day"}, []string{"CRON_SCHEDULE"}},
		{"bad prune cron", []string{"PRUNE_SCHEDULE=* * *"}, []string{"PRUNE_SCHEDULE"}},
		{"bad duration", []string{"BACKUP_TIMEOUT=soon"}, []string{"BACKUP_TIMEOUT"}},
		{"negative duration", []string{"BACKUP_RETRY_DELAY=-5m"}, []string{"BACKUP_RETRY_DELAY"}},
		{"bad percent", []string{"BACKUP_MAX_SHRINK=150"}, []string{"BACKUP_MAX_SHRINK"}},
		{"bad bool", []string{"APPEND_ONLY=maybe"}, []string{"APPEND_ONLY"}},
		{"bad retries", []string{"BACKUP_RETRIES=-1"}, []string{"BACKUP_RETRIES"}},
		{"bad tz", []string{"TZ=Mars/Olympus"}, []string{"TZ"}},
		{"bad verify mode", []string{"VERIFY_MODE=partial"}, []string{"VERIFY_MODE"}},
		{"bad sample bytes", []string{"VERIFY_SAMPLE_BYTES=lots"}, []string{"VERIFY_SAMPLE_BYTES"}},
		{"verify copy without copy repo", []string{"VERIFY_COPY=true"}, []string{"VERIFY_COPY"}},
		{"bad notify on", []string{"NOTIFY_ON=failure,loud"}, []string{"NOTIFY_ON", "loud"}},
		{"bad port", []string{"BACKUP_HTTP_PORT=70000"}, []string{"BACKUP_HTTP_PORT"}},
		{"bad web addr", []string{"MCS_BACKUP_WEB_ADDR=localhost"}, []string{"MCS_BACKUP_WEB_ADDR"}},
		{"bad log format", []string{"MCS_BACKUP_LOG_FORMAT=xml"}, []string{"MCS_BACKUP_LOG_FORMAT"}},
		{"rejected restic arg", []string{"RESTIC_BACKUP_ARGS=--host other"}, []string{"RESTIC_BACKUP_ARGS", "--host"}},
		{"unterminated quote", []string{`RESTIC_CHECK_ARGS=--x "y`}, []string{"RESTIC_CHECK_ARGS"}},
		{"reserved label", []string{"METRICS_LABELS={version: x}"}, []string{"METRICS_LABELS", "reserved"}},
		{"bad label name", []string{`METRICS_LABELS={"a-b": x}`}, []string{"METRICS_LABELS"}},
		{"copy without password", []string{"COPY_REPOSITORY=/copy"}, []string{"COPY_PASSWORD"}},
		{"copy two passwords", []string{"COPY_REPOSITORY=/copy", "COPY_PASSWORD=a", "COPY_PASSWORD_FILE=/x"}, []string{"not both"}},
		{"copy equals main", []string{"COPY_REPOSITORY=/tmp/repo", "COPY_PASSWORD=a"}, []string{"COPY_REPOSITORY"}},
		{"cron both", []string{"CRON_SCHEDULE=0 * * * *", "CRON_SCHEDULE_FILE=/x"}, []string{"CRON_SCHEDULE_FILE", "not both"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(append(testEnv(t), tt.env...))
			wantErr(t, err, tt.want...)
		})
	}

	t.Run("all problems reported together", func(t *testing.T) {
		_, err := Load(append(testEnv(t), "BACKUP_TIMEOUT=x", "APPEND_ONLY=x", "PRUNE_SCHEDULE=x"))
		wantErr(t, err, "BACKUP_TIMEOUT", "APPEND_ONLY", "PRUNE_SCHEDULE")
	})
}

func TestLoadAppendOnlyNeedsNoRetention(t *testing.T) {
	env := []string{"RESTIC_REPOSITORY=/r", "RESTIC_PASSWORD=x", "BACKUP_ROOT=" + t.TempDir(), "APPEND_ONLY=true"}
	c := mustLoad(t, env)
	if !c.Retention.IsZero() {
		t.Errorf("retention = %+v", c.Retention)
	}
}

func TestLoadCopy(t *testing.T) {
	c := mustLoad(t, testEnv(t, "COPY_REPOSITORY=s3:host/offsite", "COPY_PASSWORD_FILE=/run/copy", "COPY_APPEND_ONLY=true", "VERIFY_COPY=true"))
	want := Copy{Repository: "s3:host/offsite", PasswordFile: "/run/copy", AppendOnly: true}
	if c.Copy == nil || *c.Copy != want {
		t.Errorf("copy = %+v, want %+v", c.Copy, want)
	}
	if !c.Verify.Copy {
		t.Error("VERIFY_COPY not set")
	}
}

func TestLoadScheduleFile(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good")
	bad := filepath.Join(dir, "bad")
	if err := os.WriteFile(good, []byte("0 2 * * *\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, []byte("nonsense"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := mustLoad(t, testEnv(t, "CRON_SCHEDULE_FILE="+good))
	if c.Schedule.BackupFile != good || c.Schedule.Backup != "" {
		t.Errorf("schedule = %+v", c.Schedule)
	}

	_, err := Load(testEnv(t, "CRON_SCHEDULE_FILE="+bad))
	wantErr(t, err, "CRON_SCHEDULE_FILE")

	c = mustLoad(t, testEnv(t, "CRON_SCHEDULE_FILE="+filepath.Join(dir, "later")))
	if len(c.Warnings) != 1 || !strings.Contains(c.Warnings[0], "not found") {
		t.Errorf("warnings = %q", c.Warnings)
	}
}

func TestLoadVerifyDirInsideRoot(t *testing.T) {
	root := t.TempDir()
	env := []string{"RESTIC_REPOSITORY=/r", "RESTIC_PASSWORD=x", `RETENTION_POLICY={"last":1}`, "BACKUP_ROOT=" + root}

	for _, dir := range []string{root, filepath.Join(root, "sandbox")} {
		_, err := Load(append(slices.Clone(env), "VERIFY_DIR="+dir))
		wantErr(t, err, "VERIFY_DIR", "BACKUP_ROOT")
	}
	// a sibling with the same prefix is not inside the root
	mustLoad(t, append(slices.Clone(env), "VERIFY_DIR="+root+"-sandbox"))
}

func TestLoadWarnings(t *testing.T) {
	c := mustLoad(t, testEnv(t,
		"INFLUXDB_URL=http://x", "LOKI_URL=http://y",
		"AUTO_UNLOCK_AFTER=5m",
		"BACKUP_PRUNE_INTERVAL=120h",
		"COPY_APPEND_ONLY=true",
		"PIPE_IN_SCRIPT="+writeScript(t, "in", 0o755),
	))
	for _, want := range []string{"INFLUXDB_URL", "LOKI_URL", "AUTO_UNLOCK_AFTER", "BACKUP_PRUNE_INTERVAL", "COPY_APPEND_ONLY", "PIPE_IN_SCRIPT"} {
		if !slices.ContainsFunc(c.Warnings, func(w string) bool { return strings.Contains(w, want) }) {
			t.Errorf("no warning mentions %s: %q", want, c.Warnings)
		}
	}
}

func TestLoadPruneInterval(t *testing.T) {
	c := mustLoad(t, testEnv(t, "BACKUP_PRUNE_INTERVAL=0"))
	if c.Schedule.Prune != "" {
		t.Errorf("prune schedule = %q, want disabled", c.Schedule.Prune)
	}
	c = mustLoad(t, testEnv(t, "BACKUP_PRUNE_INTERVAL=0", "PRUNE_SCHEDULE=0 1 * * *"))
	if c.Schedule.Prune != "0 1 * * *" {
		t.Errorf("prune schedule = %q, want PRUNE_SCHEDULE to win", c.Schedule.Prune)
	}
	c = mustLoad(t, testEnv(t, "BACKUP_PRUNE_INTERVAL=junk"))
	if c.Schedule.Prune != "0 3 * * 0" || len(c.Warnings) != 1 {
		t.Errorf("prune schedule = %q, warnings = %q", c.Schedule.Prune, c.Warnings)
	}
}

func TestLoadConfigFile(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(t.TempDir(), "config.yaml")
	yaml := `
RESTIC_REPOSITORY: /from/file
RESTIC_PASSWORD: file-secret
BACKUP_ROOT: ` + root + `
RETENTION_POLICY:
  daily: 7
  last: 3
BACKUP_PATHS: [data, "my files"]
RESTIC_BACKUP_ARGS: ["--exclude", "a b", "--one-file-system"]
BACKUP_RETRIES: 5
APPEND_ONLY: false
METRICS_LABELS:
  team: storage
`
	if err := os.WriteFile(file, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	// the environment wins, even with an empty value
	c := mustLoad(t, []string{"MCS_BACKUP_CONFIG=" + file, "RESTIC_REPOSITORY=/from/env", "BACKUP_RETRIES=", "TZ=UTC"})

	if c.Repository != "/from/env" {
		t.Errorf("repository = %q, env should win", c.Repository)
	}
	if c.Backup.Retries != 2 {
		t.Errorf("retries = %d, an empty env value should win over the file and select the default", c.Backup.Retries)
	}
	if c.Env("RESTIC_PASSWORD") != "file-secret" {
		t.Error("password from file missing")
	}
	if c.Retention != (Retention{Last: 3, Daily: 7}) {
		t.Errorf("retention = %+v", c.Retention)
	}
	if !slices.Equal(c.Paths, []string{"data", "my files"}) {
		t.Errorf("paths = %q", c.Paths)
	}
	if !slices.Equal(c.Restic.Backup, []string{"--exclude", "a b", "--one-file-system"}) {
		t.Errorf("backup args = %q", c.Restic.Backup)
	}
	if c.MetricsLabels["team"] != "storage" {
		t.Errorf("labels = %v", c.MetricsLabels)
	}

	_, err := Load([]string{"MCS_BACKUP_CONFIG=/does/not/exist.yaml"})
	wantErr(t, err, "MCS_BACKUP_CONFIG")
}

func TestEnviron(t *testing.T) {
	c := mustLoad(t, testEnv(t,
		"AWS_ACCESS_KEY_ID=key", "MCS_BACKUP_WEB_TOKEN=token",
		"COPY_REPOSITORY=/copy", "COPY_PASSWORD=copy-secret",
	))
	all := c.Environ()
	if !slices.IsSorted(all) {
		t.Error("Environ() is not sorted")
	}
	for _, want := range []string{"AWS_ACCESS_KEY_ID=key", "MCS_BACKUP_WEB_TOKEN=token", "COPY_PASSWORD=copy-secret"} {
		if !slices.Contains(all, want) {
			t.Errorf("Environ() lacks %s", want)
		}
	}
	for _, e := range c.ResticEnviron() {
		if strings.HasPrefix(e, "MCS_BACKUP_") || strings.HasPrefix(e, "COPY_") {
			t.Errorf("ResticEnviron() leaks %s", strings.SplitN(e, "=", 2)[0])
		}
	}
	if !slices.Contains(c.ResticEnviron(), "AWS_ACCESS_KEY_ID=key") {
		t.Error("ResticEnviron() lacks AWS_ACCESS_KEY_ID")
	}
}
