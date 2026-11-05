package redact

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestHandler(t *testing.T) {
	const secret = "hunter2-secret"
	var buf bytes.Buffer
	r := New([]string{"RESTIC_PASSWORD=" + secret})
	log := slog.New(r.Handler(slog.NewJSONHandler(&buf, nil)))

	log.With("repo", "rest:https://u:"+secret+"@host").WithGroup("op").Warn("wrong password "+secret,
		"error", errors.New("Fatal: "+secret), "count", 3,
		slog.Group("hook", "output", "printed "+secret))

	out := buf.String()
	if strings.Contains(out, secret) {
		t.Fatalf("a secret got into the log: %s", out)
	}
	for _, want := range []string{
		`"msg":"wrong password ***"`, `"repo":"rest:https://u:***@host"`, `"error":"Fatal: ***"`,
		`"count":3`, `"hook":{"output":"printed ***"}`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("%s is missing: %s", want, out)
		}
	}
}
