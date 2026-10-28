package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestNewLogger(t *testing.T) {
	tests := []struct {
		name      string
		env       map[string]string
		wantJSON  bool
		wantDebug bool
	}{
		{"default", nil, true, false},
		{"text", map[string]string{"MCS_BACKUP_LOG_FORMAT": "text"}, false, false},
		{"debug", map[string]string{"MCS_BACKUP_DEBUG": "true"}, true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			log := newLogger(&buf, func(k string) string { return tt.env[k] })
			log.Debug("debug message")
			log.Info("info message")
			out := buf.String()
			if got := strings.Contains(out, `"msg":"info message"`); got != tt.wantJSON {
				t.Errorf("json output = %v, want %v: %s", got, tt.wantJSON, out)
			}
			if got := strings.Contains(out, "debug message"); got != tt.wantDebug {
				t.Errorf("debug output = %v, want %v: %s", got, tt.wantDebug, out)
			}
			if !strings.Contains(out, "info message") {
				t.Errorf("info message missing: %s", out)
			}
		})
	}
}
