package notify

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestCategoryAndMatches(t *testing.T) {
	tests := []struct {
		name, category string
	}{
		{"backup.failed", "failure"}, {"verify.failed", "failure"}, {"backup.suspicious", "failure"},
		{"repository.down", "failure"}, {"copy.warning", "warning"}, {"retention.held", "warning"},
		{"backup.recovered", "recovered"}, {"repository.up", "recovered"}, {"prune.succeeded", "success"},
		{"test", ""}, {"unknown.thing", ""},
	}
	for _, tt := range tests {
		if got := Category(tt.name); got != tt.category {
			t.Errorf("Category(%q) = %q, want %q", tt.name, got, tt.category)
		}
	}
	def := []string{"failure", "warning", "recovered"}
	for name, want := range map[string]bool{
		"backup.failed": true, "retention.held": true, "backup.recovered": true,
		"backup.succeeded": false, "test": false,
	} {
		if got := Matches(def, name); got != want {
			t.Errorf("Matches(default, %q) = %v, want %v", name, got, want)
		}
	}
	if !Matches([]string{"all"}, "backup.succeeded") || Matches([]string{"all"}, "test") {
		t.Error("all selects every real event but not the test event")
	}
}

func TestStripSecrets(t *testing.T) {
	in := []string{
		"PATH=/bin", "HOME=/root", "NTFY_TOKEN=keep", "RESTIC_PASSWORD=x", "RESTIC_REPOSITORY=y",
		"AWS_SECRET_ACCESS_KEY=z", "AZURE_ACCOUNT_KEY=a", "B2_ACCOUNT_KEY=b", "GOOGLE_APPLICATION_CREDENTIALS=c",
		"COPY_PASSWORD=d", "MCS_BACKUP_WEB_TOKEN=e", "MCS_BACKUP_SOCKET=/tmp/s",
	}
	want := []string{"PATH=/bin", "HOME=/root", "NTFY_TOKEN=keep", "MCS_BACKUP_SOCKET=/tmp/s"}
	if got := StripSecrets(in); !slices.Equal(got, want) {
		t.Errorf("StripSecrets() = %q, want %q", got, want)
	}
}

func newNotifier(t *testing.T, command string, mod ...func(*Config)) *Notifier {
	t.Helper()
	cfg := Config{Command: command, On: []string{"all"}, Timeout: 5 * time.Second, Host: "db1", Env: os.Environ()}
	for _, m := range mod {
		m(&cfg)
	}
	return New(cfg)
}

func TestSend(t *testing.T) {
	dir := t.TempDir()
	// only what the test needs: the environment of the test run may hold
	// anything, the CI sets MCS_TEST_S3_SECRET_KEY for example
	n := newNotifier(t, `env > `+dir+`/env; cat > `+dir+`/stdin`, func(c *Config) {
		c.Env = []string{"PATH=" + os.Getenv("PATH"), "RESTIC_PASSWORD=secret", "AWS_SECRET_ACCESS_KEY=secret2", "COPY_PASSWORD=secret3"}
	})
	ts := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	err := n.Send(t.Context(), Event{
		Name: "backup.failed", Kind: "backup", Result: "failure", Message: "backup failed",
		Error: "disk full; `rm -rf /` $(whoami)", Snapshot: "abc", Time: ts,
	})
	if err != nil {
		t.Fatal(err)
	}

	envData, _ := os.ReadFile(filepath.Join(dir, "env"))
	env := string(envData)
	for _, want := range []string{
		"MCS_BACKUP_EVENT=backup.failed", "MCS_BACKUP_KIND=backup", "MCS_BACKUP_RESULT=failure",
		"MCS_BACKUP_MESSAGE=backup failed", "MCS_BACKUP_ERROR=disk full; `rm -rf /` $(whoami)",
		"MCS_BACKUP_SNAPSHOT=abc", "MCS_BACKUP_TIME=2026-03-04T05:06:07Z", "MCS_BACKUP_HOST=db1",
	} {
		if !strings.Contains(env, want+"\n") {
			t.Errorf("environment lacks %q", want)
		}
	}
	for _, secret := range []string{"secret", "RESTIC_PASSWORD", "AWS_SECRET", "COPY_PASSWORD"} {
		if strings.Contains(env, secret) {
			t.Errorf("environment leaks %q", secret)
		}
	}

	stdin, _ := os.ReadFile(filepath.Join(dir, "stdin"))
	var got Event
	if err := json.Unmarshal(stdin, &got); err != nil {
		t.Fatalf("stdin is not JSON: %v\n%s", err, stdin)
	}
	if got.Name != "backup.failed" || got.Error != "disk full; `rm -rf /` $(whoami)" || got.Snapshot != "abc" || !got.Time.Equal(ts) {
		t.Errorf("stdin event = %+v", got)
	}
}

func TestSendFailures(t *testing.T) {
	if err := newNotifier(t, "").Send(t.Context(), Event{Name: "test"}); err == nil {
		t.Error("Send without a command must fail")
	}

	err := newNotifier(t, `echo "webhook returned 500" >&2; exit 22`).Send(t.Context(), Event{Name: "test"})
	if err == nil || !strings.Contains(err.Error(), "webhook returned 500") {
		t.Errorf("err = %v, want the command's output", err)
	}

	n := newNotifier(t, `sleep 30`, func(c *Config) { c.Timeout = 200 * time.Millisecond })
	start := time.Now()
	err = n.Send(t.Context(), Event{Name: "test"})
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Errorf("err = %v, want a timeout", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("took %v", d)
	}

	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(200*time.Millisecond, cancel)
	err = newNotifier(t, `sleep 30`).Send(ctx, Event{Name: "test"})
	if err == nil || strings.Contains(err.Error(), "timed out") || !strings.Contains(err.Error(), "canceled") {
		t.Errorf("err = %v, want the cancellation and no timeout", err)
	}
}

func TestNotifyAsync(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "log")
	n := newNotifier(t, `echo "$MCS_BACKUP_EVENT" >> `+log, func(c *Config) { c.On = []string{"failure", "recovered"} })
	n.Start(t.Context())

	n.Notify(Event{Name: "backup.failed"})
	n.Notify(Event{Name: "backup.succeeded"}) // filtered
	n.Notify(Event{Name: "backup.recovered"})
	n.Notify(Event{Name: "repository.down"})

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	n.Stop(ctx)

	data, _ := os.ReadFile(log)
	if got, want := strings.Fields(string(data)), []string{"backup.failed", "backup.recovered", "repository.down"}; !slices.Equal(got, want) {
		t.Errorf("delivered %q, want %q in order", got, want)
	}
	if n.Errors() != 0 {
		t.Errorf("errors = %d", n.Errors())
	}
	n.Notify(Event{Name: "backup.failed"}) // after Stop: ignored, no panic
}

func TestStopAbortsTheRestWhenItsContextEnds(t *testing.T) {
	var log bytes.Buffer
	n := newNotifier(t, `sleep 30`, func(c *Config) { c.Log = slog.New(slog.NewTextHandler(&log, nil)) })
	n.Start(t.Context())
	for range 3 {
		n.Notify(Event{Name: "backup.failed"})
	}
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	n.Stop(ctx)

	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("Stop took %v", d)
	}
	if n.Errors() != 3 || !strings.Contains(n.LastError(), "stopped at shutdown") {
		t.Errorf("errors = %d, last = %q: every lost event must be counted with the reason", n.Errors(), n.LastError())
	}
	if out := log.String(); !strings.Contains(out, "NOTIFY_COMMAND: stopped at shutdown") || strings.Contains(out, "timed out") {
		t.Errorf("the aborted delivery is logged as\n%s", out)
	}
}

func TestNotifyFailureIsCounted(t *testing.T) {
	n := newNotifier(t, `exit 1`)
	n.Start(t.Context())
	n.Notify(Event{Name: "backup.failed"})
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	n.Stop(ctx)

	if n.Errors() != 1 || !strings.Contains(n.LastError(), "NOTIFY_COMMAND") {
		t.Errorf("errors = %d, last = %q", n.Errors(), n.LastError())
	}
}

func TestNotifyQueueFull(t *testing.T) {
	n := newNotifier(t, `sleep 0.5`, func(c *Config) { c.QueueSize = 1 })
	n.Start(t.Context())
	for range 6 {
		n.Notify(Event{Name: "backup.failed"})
	}
	if n.Errors() < 3 || !strings.Contains(n.LastError(), "queue full") {
		t.Errorf("errors = %d, last = %q: surplus events must be dropped and counted", n.Errors(), n.LastError())
	}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	n.Stop(ctx) // returns when the context ends, even though events are still queued
}

func TestDisabled(t *testing.T) {
	n := New(Config{})
	if n.Enabled() {
		t.Error("Enabled() without a command")
	}
	n.Start(t.Context())
	n.Notify(Event{Name: "backup.failed"})
	n.Stop(t.Context())
	if n.Errors() != 0 {
		t.Errorf("errors = %d", n.Errors())
	}
}
