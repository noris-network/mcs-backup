//go:build integration

package integration

import (
	"net"
	"net/http"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/noris-network/mcs-backup/v2/internal/service"
)

// restServer starts rest-server in append-only mode and returns the repository URL.
func restServer(t *testing.T) string {
	t.Helper()
	if restServerBin == "" {
		t.Skip("REST_SERVER_BIN is not set")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0") //nolint:noctx // only to find a free port
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	cmd := exec.CommandContext(t.Context(), restServerBin, "--path", filepath.Join(t.TempDir(), "repos"), "--listen", addr, "--append-only", "--no-auth") //nolint:gosec // test binary
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Wait() })
	deadline := time.Now().Add(15 * time.Second)
	for {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+addr+"/", nil)
		if resp, err := http.DefaultClient.Do(req); err == nil {
			_ = resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("rest-server did not come up")
		}
		time.Sleep(50 * time.Millisecond)
	}
	return "rest:http://" + addr + "/" + strconv.Itoa(int(time.Now().UnixNano()%1000000))
}

func TestAppendOnlyRepositoryWorks(t *testing.T) {
	url := restServer(t)
	i := newInst(t, "RESTIC_REPOSITORY="+url, "APPEND_ONLY=true", "RETENTION_POLICY=")
	i.repo = url
	i.write("a.txt", "1")
	i.start()

	for _, n := range []string{"1", "22", "333"} {
		i.write("a.txt", n)
		if res := i.mustBackup(n, false); res.Result != service.ResultSuccess {
			t.Fatalf("backup: %+v", res)
		}
	}
	if n := len(i.snapshots()); n != 3 {
		t.Fatalf("snapshots = %d, want 3: nothing is ever forgotten", n)
	}
	if res := i.verify(""); res.Result != service.ResultSuccess {
		t.Fatalf("verify: %s %s %s\n%s", res.Result, res.Message, res.Error, i.log)
	}

	// what an append-only repository does not allow is refused up front
	if _, err := i.d.Keep(i.ctx(), "latest"); err == nil {
		t.Error("keep is not possible without changing tags")
	}
	if err := i.d.Unlock(i.ctx(), true, service.ConfirmRemoveAll); err == nil {
		t.Error("remove-all must be refused")
	}
	if err := i.d.Unlock(i.ctx(), false, ""); err != nil {
		t.Errorf("removing stale locks must work: %v", err)
	}
	ret, err := i.d.Retention(i.ctx())
	if err != nil || !ret.AppendOnly || len(ret.Remove) != 0 {
		t.Errorf("Retention() = %+v, %v", ret, err)
	}
}
