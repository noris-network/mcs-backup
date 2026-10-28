package daemon

import (
	"regexp"
	"slices"
	"strings"
)

// redactor hides secrets in log lines before they are stored or sent to a client.
type redactor struct {
	r *strings.Replacer
}

var urlPassword = regexp.MustCompile(`://[^/@:\s]+:([^/@\s]+)@`)

// secretName reports whether an environment variable holds a secret.
func secretName(name string) bool {
	for _, marker := range []string{"PASSWORD", "SECRET", "TOKEN", "_KEY", "CREDENTIAL", "SAS"} {
		if strings.Contains(name, marker) {
			return true
		}
	}
	return false
}

// newRedactor collects the secret values of environ (KEY=VALUE pairs) and the
// passwords inside repository URLs.
func newRedactor(environ []string) *redactor {
	seen := map[string]bool{}
	for _, kv := range environ {
		name, value, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if secretName(name) && len(value) >= 4 && !strings.ContainsAny(value, "\n") {
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
		args = append(args, s, "***")
	}
	return &redactor{r: strings.NewReplacer(args...)}
}

func (r *redactor) redact(s string) string { return r.r.Replace(s) }
