package webui_test

import (
	"context"
	"net"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
	"time"

	"github.com/noris-network/mcs-backup/v2/internal/service"
	"github.com/noris-network/mcs-backup/v2/internal/servicetest"
	"github.com/noris-network/mcs-backup/v2/internal/webui"
)

func newManager(t *testing.T, addr string) *webui.Manager {
	t.Helper()
	fake := &servicetest.Fake{}
	fake.StatusFn = func(context.Context) (service.Status, error) {
		return service.Status{Version: "2.0.0", Host: "app", Repository: service.RepoStatus{Up: true}, Schedule: service.ScheduleStatus{Enabled: true}}, nil
	}
	m := webui.NewManager(webui.ManagerOptions{Addr: addr})
	m.Attach(fake)
	t.Cleanup(m.Close)
	return m
}

func browser() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, Timeout: 5 * time.Second}
}

func get(t *testing.T, c *http.Client, url string) (int, string) {
	t.Helper()
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	resp, err := c.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	var b strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return resp.StatusCode, b.String()
}

func TestManagerIsOffByDefault(t *testing.T) {
	m := newManager(t, "127.0.0.1:0")
	if st := m.Status(); st.Enabled || st.Addr != "" {
		t.Errorf("status = %+v", st)
	}
	if _, err := m.LoginLink(); err == nil {
		t.Error("a login link for a web UI that is off")
	}
	if st, err := m.Disable(); err != nil || st.Enabled {
		t.Errorf("Disable() = %+v, %v", st, err)
	}
}

func TestManagerEnableLoginDisable(t *testing.T) {
	m := newManager(t, "127.0.0.1:0")
	st, err := m.Enable("", 0)
	if err != nil || !st.Enabled || st.Addr == "" || !st.Until.IsZero() {
		t.Fatalf("Enable() = %+v, %v", st, err)
	}
	link, err := m.LoginLink()
	if err != nil || !strings.HasPrefix(link, "http://"+st.Addr+"/login?code=") {
		t.Fatalf("LoginLink() = %q, %v", link, err)
	}

	c := browser()
	if code, _ := get(t, c, "http://"+st.Addr+"/"); code != http.StatusUnauthorized {
		t.Errorf("no login: status %d", code)
	}
	if code, body := get(t, c, link); code != http.StatusOK || !strings.Contains(body, "Overview") {
		t.Errorf("login link: status %d\n%s", code, body)
	}
	if code, _ := get(t, c, "http://"+st.Addr+"/"); code != http.StatusOK {
		t.Errorf("after login: status %d", code)
	}
	// the link works once
	if code, _ := get(t, browser(), link); code != http.StatusUnauthorized {
		t.Errorf("second use of the link: status %d", code)
	}

	if st, err := m.Disable(); err != nil || st.Enabled {
		t.Fatalf("Disable() = %+v, %v", st, err)
	}
	if code, body := get(t, c, "http://"+st.Addr+"/"); code != 0 {
		t.Errorf("the web UI still answers: %d %s", code, body)
	}
}

func TestManagerSwitchingOffEndsSessions(t *testing.T) {
	m := newManager(t, "127.0.0.1:0")
	st, _ := m.Enable("", 0)
	link, _ := m.LoginLink()
	c := browser()
	get(t, c, link)

	// back on, possibly at another port: the old session must not work
	_, _ = m.Disable()
	st2, err := m.Enable("127.0.0.1:0", 0)
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := get(t, c, "http://"+st2.Addr+"/"); code != http.StatusUnauthorized {
		t.Errorf("old session accepted after the web UI was switched off and on (was %s): status %d", st.Addr, code)
	}
}

func TestManagerTimeLimit(t *testing.T) {
	m := newManager(t, "127.0.0.1:0")
	st, err := m.Enable("", 150*time.Millisecond)
	if err != nil || st.Until.IsZero() {
		t.Fatalf("Enable() = %+v, %v", st, err)
	}
	if !m.Status().Enabled {
		t.Fatal("off too early")
	}
	deadline := time.Now().Add(3 * time.Second)
	for m.Status().Enabled && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if m.Status().Enabled {
		t.Fatal("the web UI did not switch itself off")
	}
}

func TestManagerEnableAgainOnlyChangesTheLimit(t *testing.T) {
	m := newManager(t, "127.0.0.1:0")
	first, _ := m.Enable("", time.Hour)
	link, _ := m.LoginLink()
	c := browser()
	get(t, c, link)

	second, err := m.Enable("", 0)
	if err != nil || second.Addr != first.Addr || !second.Until.IsZero() {
		t.Fatalf("Enable() = %+v, %v (first %+v)", second, err, first)
	}
	if code, _ := get(t, c, "http://"+second.Addr+"/"); code != http.StatusOK {
		t.Errorf("enabling again logged the browser out: status %d", code)
	}
}

func TestManagerReportsBusyAndInvalidAddresses(t *testing.T) {
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	m := newManager(t, "")
	if _, err := m.Enable(ln.Addr().String(), 0); err == nil || !strings.Contains(err.Error(), "cannot listen on") {
		t.Errorf("busy port: %v", err)
	}
	if _, err := m.Enable("not an address", 0); err == nil || !strings.Contains(err.Error(), "invalid web UI address") {
		t.Errorf("invalid address: %v", err)
	}
	if m.Status().Enabled {
		t.Error("enabled despite the errors")
	}
}

func TestManagerLoginLinkForAWildcardAddress(t *testing.T) {
	m := newManager(t, "")
	st, err := m.Enable("0.0.0.0:0", 0)
	if err != nil {
		t.Fatal(err)
	}
	link, err := m.LoginLink()
	_, port, _ := net.SplitHostPort(st.Addr)
	if err != nil || !strings.HasPrefix(link, "http://127.0.0.1:"+port+"/login?code=") {
		t.Errorf("LoginLink() = %q, %v", link, err)
	}
}

func TestWebOffButtonSwitchesTheManagerOff(t *testing.T) {
	m := newManager(t, "127.0.0.1:0")
	st, _ := m.Enable("", 0)
	link, _ := m.LoginLink()
	c := browser()
	get(t, c, link)

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://"+st.Addr+"/web/off", nil)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status %d", resp.StatusCode)
	}
	deadline := time.Now().Add(3 * time.Second)
	for m.Status().Enabled && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if m.Status().Enabled {
		t.Error("the button did not switch the web UI off")
	}
}

func TestManagerNeedsAService(t *testing.T) {
	m := webui.NewManager(webui.ManagerOptions{})
	if _, err := m.Enable("127.0.0.1:0", 0); err == nil {
		t.Error("enabled without a service")
	}
}

func TestManagerStatusDoesNotWaitForTheShutdown(t *testing.T) {
	t.Parallel()
	m := newManager(t, "127.0.0.1:0")
	st, err := m.Enable("", 0)
	if err != nil {
		t.Fatal(err)
	}
	// a request that never ends holds the shutdown until it gives up
	conn, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", st.Addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("GET / HTTP/1.1\r\nHost: x\r\n")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond) // the server has read the start of the request

	off := make(chan struct{})
	go func() { m.Close(); close(off) }()
	deadline := time.Now().Add(2 * time.Second)
	for m.Status().Enabled {
		if time.Now().After(deadline) {
			t.Fatal("the status still says on")
		}
		time.Sleep(5 * time.Millisecond)
	}
	select {
	case <-off:
		t.Fatal("the shutdown did not wait for the open request: the test proves nothing")
	default:
	}
	if _, err := m.LoginLink(); err == nil {
		t.Error("a login link while the web UI shuts down")
	}
	<-off
}
