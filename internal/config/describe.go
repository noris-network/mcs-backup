package config

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Setting is one line of the effective configuration.
type Setting struct {
	// Name is the environment variable that controls it.
	Name  string `json:"name"`
	Value string `json:"value"`
	// Note explains defaults and consequences.
	Note string `json:"note,omitempty"`
}

var urlUserinfo = regexp.MustCompile(`(://[^/@:\s]+:)[^/@\s]+@`)

// MaskURL hides the password in the userinfo of a repository URL.
func MaskURL(s string) string { return urlUserinfo.ReplaceAllString(s, "${1}***@") }

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func orNone(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func dur(d time.Duration) string {
	if d == 0 {
		return "off"
	}
	return d.String()
}

// Describe lists the effective configuration for `config check`. Secrets are
// never included.
func (c *Config) Describe() []Setting {
	var out []Setting
	add := func(name, value, note string) { out = append(out, Setting{name, value, note}) }

	repo := MaskURL(c.Repository)
	if repo == "" {
		repo = "(from RESTIC_REPOSITORY_FILE)"
	}
	add("RESTIC_REPOSITORY", repo, "")
	add("APPEND_ONLY", onOff(c.AppendOnly), appendOnlyNote(c.AppendOnly))
	if c.Copy != nil {
		add("COPY_REPOSITORY", MaskURL(c.Copy.Repository), "secondary repository, every backup is copied there")
		add("COPY_APPEND_ONLY", onOff(c.Copy.AppendOnly), "")
	}
	add("BACKUP_ROOT", c.Root, "")
	add("BACKUP_PATHS", strings.Join(c.Paths, ":"), "relative to BACKUP_ROOT")
	add("EXCLUDE_PATHS", orNone(strings.Join(c.Excludes, ":")), "")
	add("MCS_BACKUP_HOST", c.Host, "restic host name; the retention only touches snapshots of this host")
	if c.AppendOnly && c.Retention.IsZero() {
		add("RETENTION_POLICY", "-", "not needed, nothing is ever forgotten")
	} else {
		add("RETENTION_POLICY", "keeps "+c.Retention.Describe(), "plus every snapshot marked keep")
	}

	mode := "files"
	if c.PipeMode() {
		mode = "pipe (the output of PIPE_IN_SCRIPT is backed up as " + c.Pipe.InFilename + ")"
	}
	add("PIPE_IN_SCRIPT", mode, "")
	add("PIPE_OUT_SCRIPT", orNone(c.Pipe.OutScript), "")
	for _, h := range []struct{ name, path string }{
		{"PRE_BACKUP_SCRIPT", c.Hooks.PreBackup}, {"POST_BACKUP_SCRIPT", c.Hooks.PostBackup},
		{"PRE_RESTORE_SCRIPT", c.Hooks.PreRestore}, {"POST_RESTORE_SCRIPT", c.Hooks.PostRestore},
	} {
		add(h.name, orNone(h.path), "")
	}

	tz := c.Schedule.Location.String()
	switch {
	case c.Schedule.BackupFile != "":
		add("CRON_SCHEDULE_FILE", c.Schedule.BackupFile, "time zone "+tz+", reloaded when the file changes")
	case c.Schedule.Backup != "":
		add("CRON_SCHEDULE", c.Schedule.Backup, "time zone "+tz)
	default:
		add("CRON_SCHEDULE", "-", "no backup schedule: backups are started manually")
	}
	add("BACKUP_CATCH_UP", onOff(c.Schedule.CatchUp), "make up for a backup that was missed while the service was down")
	add("PRUNE_SCHEDULE", orNone(c.Schedule.Prune), "")
	add("CHECK_SCHEDULE", orNone(c.Schedule.Check), "")
	add("VERIFY_SCHEDULE", orNone(c.Schedule.Verify), "restore test: mode "+c.Verify.Mode+verifyNote(c))

	add("BACKUP_RETRIES", strconv.Itoa(c.Backup.Retries), "every "+c.Backup.RetryDelay.String()+" after a failed scheduled backup")
	add("BACKUP_MAX_SHRINK", fmt.Sprintf("%g%%", c.Backup.MaxShrink), "a backup this much smaller than the last one is marked suspicious")
	add("FORGET_MAX_DELETE", fmt.Sprintf("%g%%", c.Backup.ForgetMaxDelete), "forget that would delete more is held back until approved")
	add("ALLOW_EMPTY_SNAPSHOT", onOff(c.Backup.AllowEmpty), "")
	add("AUTO_UNLOCK_AFTER", dur(c.AutoUnlockAfter), "")
	add("BACKUP_TIMEOUT", dur(c.Timeouts.Backup), "")
	add("PRUNE_TIMEOUT", dur(c.Timeouts.Prune), "")
	add("CHECK_TIMEOUT", dur(c.Timeouts.Check), "")
	add("RESTORE_TIMEOUT", dur(c.Timeouts.Restore), "")
	add("POST_HOOK_TIMEOUT", dur(c.Timeouts.PostHook), "")
	add("SHUTDOWN_TIMEOUT", dur(c.Timeouts.Shutdown), "keep it below terminationGracePeriodSeconds")

	notify := "-"
	if c.Notify.Command != "" {
		notify = "configured, for " + strings.Join(c.Notify.On, ", ")
	}
	add("NOTIFY_COMMAND", notify, "")

	add("BACKUP_HTTP_PORT", strconv.Itoa(c.Server.HTTPPort), "metrics and probes")
	add("MCS_BACKUP_SOCKET", c.Server.Socket, "")
	add("MCS_BACKUP_STATE_DIR", c.Server.StateDir, "holds maintenance windows")
	web := "off"
	if c.Server.WebAddr != "" {
		web = "on at " + c.Server.WebAddr
	}
	add("MCS_BACKUP_WEB_ADDR", web, "can be switched on and off at runtime")
	add("MCS_BACKUP_RESTIC", c.Restic.Bin, "")
	return out
}

func appendOnlyNote(on bool) string {
	if on {
		return "no forget, prune or tag changes; the storage must allow deleting locks"
	}
	return ""
}

func verifyNote(c *Config) string {
	if c.PipeMode() && c.Verify.Script == "" {
		return ", not configured: set VERIFY_SCRIPT in pipe mode"
	}
	return ""
}
