package redact

import (
	"strings"
	"testing"

	"github.com/noris-network/mcs-backup/v2/internal/clean"
)

// FuzzRedactor checks that a secret of the configuration never shows in what
// leaves the service, wherever it stands in the text and whatever surrounds it.
func FuzzRedactor(f *testing.F) {
	for _, s := range [][3]string{
		{"hunter2-secret", "error: ", " was wrong"},
		{"b***", "b", ""},
		{"ab�c", "", "\x01"},
		{"pa ss'word", "\x1b[31m", "\n"},
		{"s3cr3t", "https://user:", "@host/path"},
	} {
		f.Add(s[0], s[1], s[2])
	}
	f.Fuzz(func(t *testing.T, secret, before, after string) {
		if len(secret) < 4 || strings.Contains(secret, "\n") || !Secret("RESTIC_PASSWORD", secret) {
			return
		}
		r := New([]string{"RESTIC_PASSWORD=" + secret})
		for _, in := range []string{
			before + secret + after,
			// cleaning turns the control character into the replacement character of the secret
			before + strings.ReplaceAll(secret, "�", "\x01") + after,
		} {
			out := r.String(in)
			if strings.Contains(out, secret) || strings.Contains(out, clean.Text(secret)) {
				t.Errorf("secret %q shows in %q (from %q)", secret, out, in)
			}
			if out != clean.Text(out) {
				t.Errorf("String(%q) = %q keeps a control character", in, out)
			}
		}
	})
}
