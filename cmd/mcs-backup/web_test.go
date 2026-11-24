package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/noris-network/mcs-backup/v2/internal/service"
)

func TestWebSwitchesOnAndPrintsTheLink(t *testing.T) {
	c := newCLI(t)
	on := false
	c.fake.WebUIFn = func(context.Context) (service.WebUIStatus, error) {
		return service.WebUIStatus{Enabled: on, Addr: "127.0.0.1:8484"}, nil
	}
	c.fake.SetWebUIFn = func(_ context.Context, req service.WebUIRequest) (service.WebUIStatus, error) {
		on = req.Enable
		return service.WebUIStatus{Enabled: on, Addr: "127.0.0.1:8484"}, nil
	}
	c.fake.LoginLinkFn = func(context.Context) (string, error) { return "http://127.0.0.1:8484/login?code=abc", nil }

	if code := c.run("web", "--no-open"); code != 0 {
		t.Fatalf("exit %d: %s", code, c.err())
	}
	if !strings.Contains(c.out(), "http://127.0.0.1:8484/login?code=abc") || !strings.Contains(c.out(), "works once") {
		t.Errorf("output:\n%s", c.out())
	}
	if calls := c.fake.CallsTo("SetWebUI"); len(calls) != 1 || !calls[0].Args[0].(service.WebUIRequest).Enable {
		t.Errorf("the web UI should have been switched on: %v", c.fake)
	}

	// already on: no change
	if code := c.run("web", "--no-open"); code != 0 || len(c.fake.CallsTo("SetWebUI")) != 1 {
		t.Errorf("exit %d, calls %v", code, c.fake)
	}

	if code := c.run("web", "--json"); code != 0 || !strings.Contains(c.out(), `"url": "http://127.0.0.1:8484/login?code=abc"`) {
		t.Errorf("exit %d\n%s", code, c.out())
	}
}

func TestWebInAPodNamesThePortForwardFirst(t *testing.T) {
	c := newCLI(t)
	c.vars = map[string]string{"KUBERNETES_SERVICE_HOST": "10.0.0.1", "HOSTNAME": "app-0"}
	c.fake.WebUIFn = func(context.Context) (service.WebUIStatus, error) {
		return service.WebUIStatus{Enabled: true, Addr: "127.0.0.1:8484"}, nil
	}
	c.fake.LoginLinkFn = func(context.Context) (string, error) { return "http://127.0.0.1:8484/login?code=abc", nil }
	if code := c.run("web"); code != 0 {
		t.Fatalf("exit %d: %s", code, c.err())
	}
	out := c.out()
	fwd, link := strings.Index(out, "pod/app-0 8484:8484"), strings.Index(out, "http://127.0.0.1:8484/login?code=abc")
	if fwd < 0 || link < 0 || fwd > link || !strings.HasPrefix(out, "First forward the port") || !strings.Contains(out, "Then open this link") {
		t.Errorf("output:\n%s", out)
	}

	// elsewhere, also over SSH, the link alone
	c.vars = map[string]string{"SSH_CONNECTION": "1.2.3.4 5 6.7.8.9 22"}
	if code := c.run("web"); code != 0 || strings.Contains(c.out(), "forward") || strings.Contains(c.out(), "ssh") {
		t.Errorf("exit %d, output:\n%s", code, c.out())
	}
}

func TestWebEnableDisableStatus(t *testing.T) {
	c := newCLI(t)
	until := cliNow.Add(30 * time.Minute)
	c.fake.SetWebUIFn = func(_ context.Context, req service.WebUIRequest) (service.WebUIStatus, error) {
		if !req.Enable {
			return service.WebUIStatus{}, nil
		}
		return service.WebUIStatus{Enabled: true, Addr: "0.0.0.0:9000", Until: until}, nil
	}
	if code := c.run("web", "enable", "--addr", "0.0.0.0:9000", "--for", "30m"); code != 0 {
		t.Fatalf("exit %d: %s", code, c.err())
	}
	if !strings.Contains(c.out(), "on at 0.0.0.0:9000 until") || !strings.Contains(c.out(), "in 30m") {
		t.Errorf("output:\n%s", c.out())
	}
	req := c.fake.CallsTo("SetWebUI")[0].Args[0].(service.WebUIRequest)
	if req.Addr != "0.0.0.0:9000" || req.For.Std() != 30*time.Minute || !req.Enable {
		t.Errorf("request = %+v", req)
	}

	if code := c.run("web", "disable"); code != 0 || !strings.Contains(c.out(), "The web UI is off.") {
		t.Errorf("exit %d\n%s", code, c.out())
	}
	if code := c.run("web", "status", "--json"); code != 0 || !strings.Contains(c.out(), `"enabled": false`) {
		t.Errorf("exit %d\n%s", code, c.out())
	}
}

func TestWebUnavailable(t *testing.T) {
	c := newCLI(t)
	c.fake.WebUIFn = func(context.Context) (service.WebUIStatus, error) {
		return service.WebUIStatus{}, service.ErrUnavailable
	}
	if code := c.run("web"); code != exitCodeError {
		t.Errorf("exit %d: %s", code, c.err())
	}
}
