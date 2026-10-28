// Package notify passes events to an external command (NOTIFY_COMMAND), which
// can forward them to a desktop notification, a webhook or a chat.
package notify

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// Event is something worth telling the operator about.
type Event struct {
	// Name is "<kind>.succeeded|warning|failed|recovered", "backup.suspicious",
	// "retention.held", "repository.down", "repository.up" or "test".
	Name string `json:"event"`
	// Kind is the operation: backup, copy, prune, check, verify, restore.
	Kind     string `json:"kind,omitempty"`
	Result   string `json:"result,omitempty"`
	Message  string `json:"message,omitempty"`
	Error    string `json:"error,omitempty"`
	Snapshot string `json:"snapshot,omitempty"`
	// Repository is "main" or "copy" for repository events.
	Repository string    `json:"repository,omitempty"`
	Host       string    `json:"host,omitempty"`
	Time       time.Time `json:"time"`
}

// Category returns the NOTIFY_ON category of an event name, or "" for events
// that belong to none (such as "test").
func Category(name string) string {
	switch {
	case strings.HasSuffix(name, ".failed"), name == "backup.suspicious", name == "repository.down":
		return "failure"
	case strings.HasSuffix(name, ".warning"), name == "retention.held":
		return "warning"
	case strings.HasSuffix(name, ".recovered"), name == "repository.up":
		return "recovered"
	case strings.HasSuffix(name, ".succeeded"):
		return "success"
	}
	return ""
}

// Matches reports whether an event name is selected by NOTIFY_ON.
func Matches(on []string, name string) bool {
	cat := Category(name)
	return cat != "" && (slices.Contains(on, "all") || slices.Contains(on, cat))
}

// Config configures a Notifier.
type Config struct {
	// Command is run with /bin/sh -c; empty disables notifications.
	Command string
	// On is the NOTIFY_ON filter.
	On      []string
	Timeout time.Duration
	// Host is the MCS_BACKUP_HOST of this instance.
	Host string
	// Env is the environment of the command; restic and backend secrets are
	// removed from it.
	Env []string
	// QueueSize bounds the number of waiting events, 32 if zero.
	QueueSize int
	Log       *slog.Logger
}

// Notifier delivers events asynchronously and one at a time. A failing
// command never affects operations: it is only counted and logged.
type Notifier struct {
	cfg  Config
	log  *slog.Logger
	env  []string
	ch   chan Event
	done chan struct{}

	mu      sync.Mutex
	closed  bool
	lastErr string
	errors  atomic.Int64
}

// New creates a Notifier. Call Start to begin delivering.
func New(cfg Config) *Notifier {
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = 32
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	n := &Notifier{
		cfg:  cfg,
		log:  cfg.Log,
		env:  StripSecrets(cfg.Env),
		ch:   make(chan Event, cfg.QueueSize),
		done: make(chan struct{}),
	}
	if n.log == nil {
		n.log = slog.New(slog.DiscardHandler)
	}
	return n
}

// Enabled reports whether a command is configured.
func (n *Notifier) Enabled() bool { return n.cfg.Command != "" }

// Start launches the delivery goroutine. It ends when Stop is called or ctx is done.
func (n *Notifier) Start(ctx context.Context) {
	go func() {
		defer close(n.done)
		for ev := range n.ch {
			if err := n.Send(ctx, ev); err != nil {
				n.fail(err)
			}
		}
	}()
}

// Stop stops accepting events and waits until the queue is empty or ctx ends.
func (n *Notifier) Stop(ctx context.Context) {
	n.mu.Lock()
	if !n.closed {
		n.closed = true
		close(n.ch)
	}
	n.mu.Unlock()
	select {
	case <-n.done:
	case <-ctx.Done():
	}
}

// Notify queues ev if it passes the NOTIFY_ON filter. It never blocks.
func (n *Notifier) Notify(ev Event) {
	if !n.Enabled() || !Matches(n.cfg.On, ev.Name) {
		return
	}
	if ev.Time.IsZero() {
		ev.Time = time.Now()
	}
	ev.Host = n.cfg.Host
	if !n.enqueue(ev) {
		n.fail(fmt.Errorf("notification queue full, dropped %s", ev.Name))
	}
}

// enqueue adds ev to the queue. It reports false if the queue is full; events
// after Stop are silently ignored.
func (n *Notifier) enqueue(ev Event) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.closed {
		return true
	}
	select {
	case n.ch <- ev:
		return true
	default:
		return false
	}
}

func (n *Notifier) fail(err error) {
	n.errors.Add(1)
	n.mu.Lock()
	n.lastErr = err.Error()
	n.mu.Unlock()
	n.log.Warn("notification failed", "error", err)
}

// Errors returns how many notifications failed since start.
func (n *Notifier) Errors() int64 { return n.errors.Load() }

// LastError returns the message of the latest failure, empty if none.
func (n *Notifier) LastError() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.lastErr
}

// Send runs the command for ev now, regardless of the filter, and returns its
// error. It is used for the notification test.
func (n *Notifier) Send(ctx context.Context, ev Event) error {
	if !n.Enabled() {
		return errors.New("NOTIFY_COMMAND is not configured")
	}
	if ev.Time.IsZero() {
		ev.Time = time.Now()
	}
	if ev.Host == "" {
		ev.Host = n.cfg.Host
	}
	payload, err := json.Marshal(ev)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, n.cfg.Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", n.cfg.Command)
	cmd.Env = append(slices.Clone(n.env),
		"MCS_BACKUP_EVENT="+ev.Name,
		"MCS_BACKUP_KIND="+ev.Kind,
		"MCS_BACKUP_RESULT="+ev.Result,
		"MCS_BACKUP_MESSAGE="+ev.Message,
		"MCS_BACKUP_ERROR="+ev.Error,
		"MCS_BACKUP_SNAPSHOT="+ev.Snapshot,
		"MCS_BACKUP_TIME="+ev.Time.Format(time.RFC3339),
		"MCS_BACKUP_HOST="+ev.Host,
	)
	cmd.Stdin = bytes.NewReader(payload)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 2 * time.Second

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(out.String())
		if len(msg) > 500 {
			msg = msg[len(msg)-500:]
		}
		if ctx.Err() != nil {
			err = fmt.Errorf("timed out after %v", n.cfg.Timeout)
		}
		if msg != "" {
			return fmt.Errorf("NOTIFY_COMMAND: %w: %s", err, msg)
		}
		return fmt.Errorf("NOTIFY_COMMAND: %w", err)
	}
	n.log.Debug("notification sent", "event", ev.Name)
	return nil
}

// StripSecrets removes restic and storage backend credentials from env, so
// that the notification command, which is user code, does not receive them.
func StripSecrets(env []string) []string {
	prefixes := []string{
		"RESTIC_", "AWS_", "AZURE_", "B2_", "GOOGLE_", "OS_", "ST_", "RCLONE_", "COPY_",
		"MCS_BACKUP_WEB_TOKEN",
	}
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if !slices.ContainsFunc(prefixes, func(p string) bool { return strings.HasPrefix(kv, p) }) {
			out = append(out, kv)
		}
	}
	return out
}
