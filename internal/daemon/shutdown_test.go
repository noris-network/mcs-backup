package daemon

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/noris-network/mcs-backup/v2/internal/engine"
	"github.com/noris-network/mcs-backup/v2/internal/restic"
	"github.com/noris-network/mcs-backup/v2/internal/service"
)

func TestShutdownWaitsForACooperativeOperation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := newTestDaemon(t, func(f *fakeEngine) {
			f.backup = func(ctx context.Context, _ service.BackupRequest, _ engine.Sink) engine.BackupResult {
				<-ctx.Done()
				time.Sleep(3 * time.Second) // the post hook
				return engine.BackupResult{Result: service.ResultCancelled}
			}
		}, "SHUTDOWN_TIMEOUT=25s")
		ctx, stop := start(t, d)
		synctest.Wait()
		_, _ = d.StartBackup(ctx, service.BackupRequest{})
		synctest.Wait()

		begin := time.Now()
		stop()
		if took := time.Since(begin); took < 3*time.Second || took > 5*time.Second {
			t.Errorf("shutdown took %v, want the operation's 3 seconds", took)
		}
		if res := d.lastRuns[service.KindBackup]; res.Result != service.ResultCancelled {
			t.Errorf("last run = %+v: the operation must be finished before the service exits", res)
		}
		if d.notes.endedFirst || d.notes.stopLeft != 22*time.Second {
			t.Errorf("notifier cut short: %v, given %v: the queued notifications get the rest of the budget",
				d.notes.endedFirst, d.notes.stopLeft)
		}
	})
}

func TestShutdownStopsAStubbornOperationAfterTheBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		d := newTestDaemon(t, func(f *fakeEngine) {
			f.backup = func(context.Context, service.BackupRequest, engine.Sink) engine.BackupResult {
				<-release // ignores the cancellation
				return engine.BackupResult{Result: service.ResultCancelled}
			}
		}, "SHUTDOWN_TIMEOUT=2s")
		ctx, stop := start(t, d)
		synctest.Wait()
		_, _ = d.StartBackup(ctx, service.BackupRequest{})
		synctest.Wait()

		begin := time.Now()
		stop()
		took := time.Since(begin)
		if took < 2*time.Second || took > 15*time.Second {
			t.Errorf("shutdown took %v, want the 2 second budget plus a short grace", took)
		}
		select {
		case <-d.hardStop:
		default:
			t.Error("the hard stop was not signalled")
		}
		if d.notes.stopLeft != 3*time.Second {
			t.Errorf("the notifier got %v after the budget was used up, want 3s", d.notes.stopLeft)
		}
		close(release)
		synctest.Wait()
	})
}

func TestShutdownRefusesNewOperations(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := newTestDaemon(t, nil)
		ctx, stop := start(t, d)
		synctest.Wait()
		stop()
		if _, err := d.StartBackup(ctx, service.BackupRequest{}); !errors.Is(err, errShuttingDown) {
			t.Errorf("StartBackup() after the shutdown: %v", err)
		}
	})
}

func TestRunFailsOnUnsupportedRestic(t *testing.T) {
	d := newTestDaemon(t, nil)
	d.Daemon.eng = oldRestic{d.eng}
	if err := d.Run(t.Context()); err == nil || !strings.Contains(err.Error(), "too old") {
		t.Errorf("Run() = %v", err)
	}
}

type oldRestic struct{ *fakeEngine }

func (oldRestic) CheckVersion(context.Context) (restic.Version, error) {
	return restic.Version{Major: 0, Minor: 16}, errors.New("restic 0.16.0 is too old")
}

func TestHTTPServer(t *testing.T) {
	d := newTestDaemon(t, func(f *fakeEngine) { f.setSnaps(snap("a", "me", time.Hour, 100)) })
	d.o.HTTPAddr = "127.0.0.1:0"
	ctx, stop := start(t, d)
	defer stop()

	var addr string
	for range 100 {
		if addr = d.Addr(); addr != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if addr == "" {
		t.Fatal("the HTTP server did not start")
	}
	fetch := func(path string) (int, string) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+path, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}
	if code, _ := fetch("/healthz"); code != http.StatusOK {
		t.Errorf("/healthz = %d", code)
	}
	if code, _ := fetch("/readyz"); code != http.StatusOK {
		t.Errorf("/readyz = %d", code)
	}
	code, body := fetch("/metrics")
	if code != http.StatusOK || !strings.Contains(body, "mcsbackup_info{") || !strings.Contains(body, "go_goroutines") {
		t.Errorf("/metrics = %d\n%.300s", code, body)
	}
	for _, path := range []string{"/v1/status", "/api/mcs-backup/status", "/backup"} {
		if code, _ := fetch(path); code != http.StatusNotFound {
			t.Errorf("%s = %d: nothing that controls mcs-backup is reachable over TCP", path, code)
		}
	}

	// the port is taken
	other := newTestDaemon(t, nil)
	other.o.HTTPAddr = addr
	if err := other.Run(t.Context()); err == nil || !strings.Contains(err.Error(), "listen") {
		t.Errorf("second Run() = %v", err)
	}
}
