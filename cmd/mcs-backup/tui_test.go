package main

import (
	"runtime"
	"strings"
	"testing"
)

func TestTUIRequiresATerminal(t *testing.T) {
	c := newCLI(t)
	if code := c.run("tui"); code != exitCodeUsage || !strings.Contains(c.err(), "needs a terminal") {
		t.Errorf("exit %d: %s", code, c.err())
	}
}

func TestWebHint(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{"local", nil, ""},
		// over SSH the user knows how they got there; only a pod needs a hint
		{"ssh", map[string]string{"SSH_CONNECTION": "1.2.3.4 5 6.7.8.9 22"}, ""},
		{"pod", map[string]string{"KUBERNETES_SERVICE_HOST": "10.0.0.1", "HOSTNAME": "app-0"}, "kubectl port-forward"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &app{getenv: func(k string) string { return tc.env[k] }}
			got := a.webHint("http://127.0.0.1:8080/login?code=x")
			if !strings.Contains(got, tc.want) || (tc.want == "" && got != "") {
				t.Errorf("webHint = %q, want it to contain %q", got, tc.want)
			}
			if tc.name == "pod" && (!strings.HasSuffix(got, " pod/app-0 8080:8080") || strings.Contains(got, "-n  ")) {
				t.Errorf("webHint = %q", got)
			}
		})
	}
	a := &app{getenv: func(string) string { return "x" }}
	if got := a.webHint("not a url"); got != "" {
		t.Errorf("webHint(garbage) = %q", got)
	}
}

func TestLocalSession(t *testing.T) {
	for name, tc := range map[string]struct {
		env  map[string]string
		want bool
	}{
		"ssh":     {map[string]string{"SSH_CONNECTION": "x", "DISPLAY": ":0"}, false},
		"pod":     {map[string]string{"KUBERNETES_SERVICE_HOST": "10.0.0.1", "DISPLAY": ":0"}, false},
		"wayland": {map[string]string{"WAYLAND_DISPLAY": "wayland-0"}, true},
		"console": {nil, runtime.GOOS == "darwin"},
	} {
		a := &app{getenv: func(k string) string { return tc.env[k] }}
		if got := a.local(); got != tc.want {
			t.Errorf("%s: local() = %v, want %v", name, got, tc.want)
		}
	}
}
