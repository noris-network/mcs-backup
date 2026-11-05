// Package redact hides the secret values of the configuration in text that
// leaves the service: logs, results, status and notifications.
package redact

import (
	"regexp"
	"slices"
	"strings"
)

// Mask replaces a secret.
const Mask = "***"

// Redactor replaces known secret values with Mask.
type Redactor struct {
	r *strings.Replacer
}

var urlPassword = regexp.MustCompile(`://[^/@:\s]+:([^/@\s]+)@`)

// SecretName reports whether an environment variable holds a secret.
func SecretName(name string) bool {
	for _, marker := range []string{"PASSWORD", "SECRET", "TOKEN", "_KEY", "CREDENTIAL", "SAS"} {
		if strings.Contains(name, marker) {
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
		if SecretName(name) && len(value) >= 4 && !strings.ContainsAny(value, "\n") {
			seen[value] = true
		}
		for _, m := range urlPassword.FindAllStringSubmatch(value, -1) {
			if len(m[1]) >= 3 {
				seen[m[1]] = true
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

// String returns s with the secrets replaced.
func (r *Redactor) String(s string) string { return r.r.Replace(s) }
