package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/noris-network/mcs-backup/v2/internal/service"
	"github.com/noris-network/mcs-backup/v2/internal/servicetest"
)

const mcpHandshake = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}
{"jsonrpc":"2.0","method":"notifications/initialized"}
{"jsonrpc":"2.0","id":2,"method":"tools/list"}
`

// agentInput plays an agent: it sends the handshake and keeps the connection
// open until the answer to the last request has arrived, like a real client.
type agentInput struct {
	data   string
	answer func() bool
}

func (a *agentInput) Read(p []byte) (int, error) {
	if a.data != "" {
		n := copy(p, a.data)
		a.data = a.data[n:]
		return n, nil
	}
	for range 500 {
		if a.answer() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	return 0, io.EOF
}

// runMCP runs the mcp command against a fake service and returns what it wrote.
func runMCP(t *testing.T, fake *servicetest.Fake, flags ...string) (code int, out, errOut string) {
	t.Helper()
	var stdout, stderr syncBuffer
	a := &app{
		stdout: &stdout, stderr: &stderr,
		stdin:  &agentInput{data: mcpHandshake, answer: func() bool { return strings.Contains(stdout.String(), `"id":2`) }},
		getenv: func(string) string { return "" }, environ: func() []string { return nil },
		dial: func(string) service.Service { return fake }, now: time.Now, poll: 10 * time.Millisecond,
	}
	code = a.run(t.Context(), append([]string{"mcp"}, flags...))
	return code, stdout.String(), stderr.String()
}

func TestMCPOffersOnlyWhatWasAllowed(t *testing.T) {
	for _, tc := range []struct {
		flags       []string
		has, hasNot string
	}{
		{nil, "get_status", "start_backup"},
		{[]string{"--allow-write"}, "start_backup", "restore_overwrite"},
		{[]string{"--allow-destructive"}, "restore_overwrite", "web"},
	} {
		code, out, errOut := runMCP(t, &servicetest.Fake{}, tc.flags...)
		if code != 0 {
			t.Fatalf("%v: exit %d: %s", tc.flags, code, errOut)
		}
		if !strings.Contains(out, `"name":"`+tc.has+`"`) {
			t.Errorf("%v: %s is not offered:\n%s", tc.flags, tc.has, out)
		}
		if strings.Contains(out, `"name":"`+tc.hasNot) {
			t.Errorf("%v: %s is offered:\n%s", tc.flags, tc.hasNot, out)
		}
		// stdout carries nothing but the protocol
		for line := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
			if !strings.HasPrefix(line, `{"jsonrpc"`) {
				t.Errorf("stdout has a line that is not a protocol message: %q", line)
			}
		}
	}
}

func TestMCPNeedsTheService(t *testing.T) {
	fake := &servicetest.Fake{StatusFn: func(context.Context) (service.Status, error) {
		return service.Status{}, errors.New("connection refused")
	}}
	if code, out, _ := runMCP(t, fake); code == 0 || out != "" {
		t.Errorf("exit %d without a service, output:\n%s", code, out)
	}
}
