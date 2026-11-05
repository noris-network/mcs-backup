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
	}
	for in, want := range tests {
		if got := r.String(in); got != want {
			t.Errorf("String(%q) = %q, want %q", in, got, want)
		}
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
