package config

import (
	"strings"
	"testing"
)

func TestMaskURL(t *testing.T) {
	for in, want := range map[string]string{
		"s3:https://s3.example.com/bucket":                "s3:https://s3.example.com/bucket",
		"rest:https://backup:s3cr3t@host:8000/repo":       "rest:https://backup:***@host:8000/repo",
		"sftp:user@host:/path":                            "sftp:user@host:/path",
		"/srv/restic":                                     "/srv/restic",
		"rest:http://a:b@one/x and rest:http://c:d@two/y": "rest:http://a:***@one/x and rest:http://c:***@two/y",
	} {
		if got := MaskURL(in); got != want {
			t.Errorf("MaskURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDescribe(t *testing.T) {
	c := mustLoad(t, testEnv(t,
		"RESTIC_REPOSITORY=rest:https://backup:topsecret@host/repo", "CRON_SCHEDULE=0 2 * * *",
		"COPY_REPOSITORY=s3:host/offsite", "COPY_PASSWORD=copysecret", "NOTIFY_COMMAND=curl x", "AUTO_UNLOCK_AFTER=2h",
	))
	got := map[string]Setting{}
	var all strings.Builder
	for _, s := range c.Describe() {
		got[s.Name] = s
		all.WriteString(s.Name + " " + s.Value + " " + s.Note + "\n")
	}
	for name, want := range map[string]string{
		"RESTIC_REPOSITORY": "rest:https://backup:***@host/repo", "CRON_SCHEDULE": "0 2 * * *", "COPY_REPOSITORY": "s3:host/offsite",
		"RETENTION_POLICY": "keeps one per day for 7 days", "PRUNE_SCHEDULE": "0 3 * * 0", "AUTO_UNLOCK_AFTER": "2h0m0s",
		"BACKUP_MAX_SHRINK": "50%", "SHUTDOWN_TIMEOUT": "25s", "RESTORE_TIMEOUT": "off", "NOTIFY_COMMAND": "configured, for failure, warning, recovered",
	} {
		if got[name].Value != want {
			t.Errorf("%s = %q, want %q", name, got[name].Value, want)
		}
	}
	for _, secret := range []string{"topsecret", "copysecret", "secret"} {
		if strings.Contains(all.String(), secret) {
			t.Errorf("the description leaks %q", secret)
		}
	}

	c = mustLoad(t, testEnv(t, "APPEND_ONLY=true", "RETENTION_POLICY="))
	got = map[string]Setting{}
	for _, s := range c.Describe() {
		got[s.Name] = s
	}
	if got["APPEND_ONLY"].Value != "on" || !strings.Contains(got["RETENTION_POLICY"].Note, "nothing is ever forgotten") || got["CRON_SCHEDULE"].Value != "-" {
		t.Errorf("append-only description: %+v / %+v", got["APPEND_ONLY"], got["RETENTION_POLICY"])
	}
}
