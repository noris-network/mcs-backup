// Package redact hides the secret values of the configuration in text that
// leaves the service: logs, results, status and notifications. The same text
// quotes restic and the scripts, so control characters are taken out of it as
// well: a file name or a line of a script must not steer the terminal of
// whoever reads it.
package redact

import (
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/noris-network/mcs-backup/v2/internal/clean"
)

// Mask replaces a secret.
const Mask = "***"

// Redactor replaces known secret values with Mask.
type Redactor struct {
	r *strings.Replacer
}

var urlPassword = regexp.MustCompile(`://[^/@:\s]+:([^/@\s]+)@`)

// Secret reports whether the environment variable name holds a secret, judged
// by its name: PASSWORD, PASSWD, PASSPHRASE, SECRET, TOKEN or WEBHOOK anywhere
// in it, or PASS, PWD, KEY or SAS as a later word (RCLONE_CONFIG_PASS,
// MYSQL_PWD, B2_ACCOUNT_KEY, AZURE_ACCOUNT_SAS). A variable that names a file,
// a command or an identifier is no secret itself, and neither is a boolean
// value such as the true of RCLONE_SFTP_KEY_USE_AGENT.
func Secret(name, value string) bool {
	for _, suffix := range []string{"_FILE", "_COMMAND", "_ID"} {
		if strings.HasSuffix(name, suffix) {
			return false
		}
	}
	if _, err := strconv.ParseBool(value); err == nil {
		return false
	}
	for _, marker := range []string{"PASSWORD", "PASSWD", "PASSPHRASE", "SECRET", "TOKEN", "WEBHOOK"} {
		if strings.Contains(name, marker) {
			return true
		}
	}
	// short markers only as whole words, and not as the first one: PWD is the
	// working directory of the shell, FTP_PASSIVE is no password
	words := strings.Split(name, "_")
	for _, word := range words[1:] {
		switch word {
		case "PASS", "PWD", "KEY", "SAS":
			return true
		}
	}
	return false
}

// New collects the secret values of environ (KEY=VALUE pairs) and the
// passwords inside URLs.
func New(environ []string) *Redactor {
	seen := map[string]bool{}
	for _, kv := range environ {
		name, value, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		// String cleans the text before it replaces: a secret with a control
		// character in it is found in its cleaned form
		if Secret(name, value) && len(value) >= 4 && !strings.ContainsAny(value, "\n") {
			seen[value], seen[clean.Text(value)] = true, true
		}
		for _, m := range urlPassword.FindAllStringSubmatch(value, -1) {
			if len(m[1]) >= 3 {
				seen[m[1]], seen[clean.Text(m[1])] = true, true
			}
		}
	}
	// longer secrets first, so that a secret that contains another one is hidden completely
	secrets := make([]string, 0, len(seen))
	for s := range seen {
		secrets = append(secrets, s)
	}
	slices.SortFunc(secrets, func(a, b string) int {
		if len(a) != len(b) {
			return len(b) - len(a)
		}
		return strings.Compare(a, b)
	})
	args := make([]string, 0, 2*len(secrets))
	for _, s := range secrets {
		args = append(args, s, Mask)
	}
	return &Redactor{r: strings.NewReplacer(args...)}
}

// String returns s with the secrets replaced and without control characters
// other than line breaks and tabs.
//
// The text is cleaned first, so that replacing a control character cannot
// complete a secret. A mask next to the rest of a secret can spell a secret
// again ("b" and "b***" give "b***"), so the secrets are replaced until nothing
// changes. That ends: every replacement makes the text shorter or turns
// characters other than * into *.
func (r *Redactor) String(s string) string {
	s = clean.Text(s)
	for {
		t := r.r.Replace(s)
		if t == s {
			return s
		}
		s = t
	}
}

// Error returns err with the secrets in its message replaced. errors.Is and
// errors.As still see the original error, so only its message must be shown.
func (r *Redactor) Error(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if s := r.String(msg); s != msg {
		return &redacted{msg: s, err: err}
	}
	return err
}

type redacted struct {
	msg string
	err error
}

func (e *redacted) Error() string { return e.msg }
func (e *redacted) Unwrap() error { return e.err }
