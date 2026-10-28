package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// handler serves the endpoints that are reachable over TCP: metrics and the
// probes. Everything that controls mcs-backup is only reachable over the
// socket (or through the authenticated web UI).
func (d *Daemon) handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", promhttp.HandlerFor(d.reg, promhttp.HandlerOpts{}))
	// /healthz: the process lives.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	// /readyz: the configuration is loaded and the API is up. It does not
	// depend on the repository, otherwise a storage outage would take the
	// application next to the sidecar out of its service.
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		d.mu.Lock()
		ready := d.ready
		d.mu.Unlock()
		if !ready {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		fmt.Fprintln(w, "ready")
	})
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<h3>mcs-backup</h3><a href="/metrics">metrics</a>`)
	})
	return mux
}

// serveHTTP starts the TCP server and returns once it is listening.
func (d *Daemon) serveHTTP(ctx context.Context) (*http.Server, error) {
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", d.o.HTTPAddr)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", d.o.HTTPAddr, err)
	}
	d.mu.Lock()
	d.httpAddr = ln.Addr().String()
	d.mu.Unlock()
	srv := &http.Server{Handler: d.handler(), ReadHeaderTimeout: 10 * time.Second}
	d.wg.Go(func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			d.log.ErrorContext(ctx, "http server stopped", "error", err)
		}
	})
	d.log.InfoContext(ctx, "serving metrics", "addr", ln.Addr().String())
	return srv, nil
}

// Addr returns the address the HTTP server listens on, empty if it does not run.
func (d *Daemon) Addr() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.httpAddr
}
