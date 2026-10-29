//go:build integration

package integration

import (
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// webInst starts the service with the web UI on a free port and returns the base URL.
func webInst(t *testing.T, extra ...string) (*inst, string) {
	t.Helper()
	i := newInst(t, extra...)
	ln, err := net.Listen("tcp", "127.0.0.1:0") //nolint:noctx // only to find a free port
	if err != nil {
		t.Fatal(err)
	}
	addr := "127.0.0.1:" + strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	_ = ln.Close()
	i.set("MCS_BACKUP_WEB_ADDR=" + addr)
	i.serve()
	return i, "http://" + addr
}

var linkPattern = regexp.MustCompile(`http://\S+/login\?code=\S+`)

// loginLink asks the service for a one-time login link.
func (i *inst) loginLink() string {
	i.t.Helper()
	out, errOut, code := i.bin("web", "--no-open")
	if code != 0 {
		i.t.Fatalf("web: exit %d\n%s\n%s", code, out, errOut)
	}
	link := linkPattern.FindString(out)
	if link == "" {
		i.t.Fatalf("no login link in %q", out)
	}
	return link
}

func browser(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Jar: jar, Timeout: 30 * time.Second}
}

func do(t *testing.T, c *http.Client, method, target string, header http.Header, form url.Values) (int, string) {
	t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(t.Context(), method, target, body)
	if err != nil {
		t.Fatal(err)
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for k, v := range header {
		req.Header[k] = v
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestWebUIRequiresALoginAndWorks(t *testing.T) {
	i, base := webInst(t)
	i.write("a.txt", "a")

	// nobody gets in without the login link, not even on localhost
	anon := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Timeout: 30 * time.Second}
	if code, _ := do(t, anon, http.MethodGet, base+"/", nil, nil); code == http.StatusOK {
		t.Fatalf("the overview is open without a login: %d", code)
	}
	if code, _ := do(t, anon, http.MethodPost, base+"/backup", nil, url.Values{}); code == http.StatusOK || code == http.StatusSeeOther {
		t.Fatalf("a backup can be started without a login: %d", code)
	}

	link := i.loginLink()
	c := browser(t)
	if code, body := do(t, c, http.MethodGet, link, nil, nil); code != http.StatusOK || !strings.Contains(body, "mcs-backup") {
		t.Fatalf("login: %d\n%s", code, body)
	}
	// the link works once
	other := browser(t)
	if code, body := do(t, other, http.MethodGet, link, nil, nil); code == http.StatusOK && !strings.Contains(strings.ToLower(body), "login") {
		t.Errorf("the one-time link worked twice: %d", code)
	}

	// start a backup from the browser
	if code, body := do(t, c, http.MethodPost, base+"/backup", http.Header{"Origin": {base}}, url.Values{"note": {"from the web"}}); code != http.StatusOK {
		t.Fatalf("backup: %d\n%s", code, body)
	}
	i.waitFor("the backup started in the browser", 60*time.Second, func() bool {
		out, _, code := i.bin("snapshots")
		return code == 0 && strings.Contains(out, "from the web")
	})
	for _, page := range []string{"/snapshots", "/locks", "/access", "/log/backup"} {
		if code, _ := do(t, c, http.MethodGet, base+page, nil, nil); code != http.StatusOK {
			t.Errorf("GET %s: %d", page, code)
		}
	}

	// a form posted from another site is refused
	code, _ := do(t, c, http.MethodPost, base+"/backup", http.Header{"Origin": {"http://evil.example"}, "Sec-Fetch-Site": {"cross-site"}}, url.Values{})
	if code != http.StatusForbidden {
		t.Errorf("cross-origin POST: %d, want 403", code)
	}

	// switched off: the session ends
	i.bin("web", "disable")
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, base+"/", nil)
	if resp, err := c.Do(req); err == nil {
		_ = resp.Body.Close()
		t.Error("the web UI still answers after it was switched off")
	}
}

func TestWebUIWithAToken(t *testing.T) {
	_, base := webInst(t, "MCS_BACKUP_WEB_TOKEN=secret-token")
	c := &http.Client{Timeout: 30 * time.Second}
	if code, _ := do(t, c, http.MethodGet, base+"/", nil, nil); code == http.StatusOK {
		t.Fatalf("open without credentials: %d", code)
	}
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, base+"/", nil)
	req.SetBasicAuth("operator", "secret-token")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("with the token: %d", resp.StatusCode)
	}
	req.SetBasicAuth("operator", "wrong")
	resp, err = c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Error("a wrong token was accepted")
	}
}
