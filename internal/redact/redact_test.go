package redact

import (
	"errors"
	"fmt"
	"testing"
)

func TestRedactor(t *testing.T) {
	r := New([]string{
		"RESTIC_PASSWORD=hunter2-secret", "AWS_SECRET_ACCESS_KEY=abcd1234wxyz", "PATH=/usr/bin", "SHORT_TOKEN=abc",
		"RESTIC_REPOSITORY=rest:https://backup:s3cr3t-pass@host/repo", "COPY_PASSWORD=hunter2-secret-and-more",
	})
	tests := map[string]string{
		"using password hunter2-secret now":                        "using password *** now",
		"key=abcd1234wxyz":                                         "key=***",
		"connect rest:https://backup:s3cr3t-pass@host/repo failed": "connect rest:https://backup:***@host/repo failed",
		"hunter2-secret-and-more":                                  "***",
		"nothing to hide in /usr/bin":                              "nothing to hide in /usr/bin",
		"abc stays":                                                "abc stays",
		// a file name that restic quotes must not steer a terminal
		"error: open /up/\x1b[2Jx: denied\n\tnext": "error: open /up/�[2Jx: denied\n\tnext",
	}
	for in, want := range tests {
		if got := r.String(in); got != want {
			t.Errorf("String(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSecret(t *testing.T) {
	secret := []string{
		"RESTIC_PASSWORD", "COPY_PASSWORD", "PGPASSWORD", "RCLONE_CONFIG_PASS", "RCLONE_SFTP_KEY_FILE_PASS", "SMTP_PASSWD",
		"BORG_PASSPHRASE", "MYSQL_PWD", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "MCS_BACKUP_WEB_TOKEN", "B2_ACCOUNT_KEY",
		"ST_KEY", "AZURE_ACCOUNT_KEY", "AZURE_ACCOUNT_SAS", "OS_APPLICATION_CREDENTIAL_SECRET", "NOTIFY_WEBHOOK_URL",
	}
	plain := []string{
		"PATH", "PWD", "OLDPWD", "HOME", "RESTIC_PASSWORD_FILE", "RESTIC_PASSWORD_COMMAND", "AWS_ACCESS_KEY_ID",
		"B2_ACCOUNT_ID", "GOOGLE_APPLICATION_CREDENTIALS", "AWS_WEB_IDENTITY_TOKEN_FILE", "FTP_PASSIVE", "KEYBOARD",
	}
	for _, name := range secret {
		if !Secret(name, "value") {
			t.Errorf("%s is a secret", name)
		}
	}
	for _, name := range plain {
		if Secret(name, "value") {
			t.Errorf("%s is no secret", name)
		}
	}
	for _, value := range []string{"true", "false", "1", "0"} {
		if Secret("RCLONE_SFTP_KEY_USE_AGENT", value) {
			t.Errorf("the boolean %q is no secret", value)
		}
	}

	// a flag whose name looks like a secret must not hide every "true" in the log
	r := New([]string{"AZURE_FORCE_CLI_CREDENTIAL=true", "RCLONE_SFTP_KEY_USE_AGENT=true", "RCLONE_CONFIG_PASS=rclone-pass"})
	if got := r.String("ok=true pass=rclone-pass"); got != "ok=true pass=***" {
		t.Errorf("String = %q", got)
	}
}

func TestError(t *testing.T) {
	r := New([]string{"RESTIC_PASSWORD=hunter2-secret"})
	if r.Error(nil) != nil {
		t.Error("Error(nil) must stay nil")
	}
	plain := errors.New("connection refused")
	if errors.Unwrap(r.Error(plain)) != nil {
		t.Error("an error without a secret must not be wrapped")
	}

	sentinel := errors.New("not found")
	err := r.Error(fmt.Errorf("%w: wrong password hunter2-secret", sentinel))
	if err.Error() != "not found: wrong password ***" {
		t.Errorf("Error() = %q", err)
	}
	if !errors.Is(err, sentinel) {
		t.Error("errors.Is must still see the original error")
	}
}
