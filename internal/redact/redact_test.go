package redact

import "testing"

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
	}
	for in, want := range tests {
		if got := r.String(in); got != want {
			t.Errorf("String(%q) = %q, want %q", in, got, want)
		}
	}
}
