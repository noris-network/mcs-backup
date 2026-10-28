package webui

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/noris-network/mcs-backup/v2/internal/service"
)

// DefaultAddr is where the web UI listens when nothing else is configured.
const DefaultAddr = "127.0.0.1:8484"

// ManagerOptions configure a Manager.
type ManagerOptions struct {
	// Addr is the configured listen address (MCS_BACKUP_WEB_ADDR), the one
	// Enable uses when it is not given another.
	Addr string
	// Token is the optional fixed token for Basic Auth.
	Token string
	Log   *slog.Logger
	Now   func() time.Time
}

// Manager switches the web UI on and off while the service runs. It
// implements the daemon's WebUI interface.
type Manager struct {
	o    ManagerOptions
	auth *Auth

	mu    sync.Mutex
	svc   service.Service
	srv   *http.Server
	addr  string // the address the server listens on, empty while off
	spec  string // the address as it was asked for (the port may have been 0)
	until time.Time
	timer *time.Timer
	gen   int // numbers the periods the web UI was on, so that old timers do nothing
	done  chan struct{}
}

// NewManager creates a Manager with the web UI off. Attach connects it to
// the service before the web UI is used.
func NewManager(o ManagerOptions) *Manager {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	return &Manager{o: o, auth: NewAuth(o.Token, o.Now)}
}

// Attach tells the Manager which service the web UI operates.
func (m *Manager) Attach(svc service.Service) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.svc = svc
}

// Status implements the daemon's WebUI interface.
func (m *Manager) Status() service.WebUIStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status()
}

func (m *Manager) status() service.WebUIStatus {
	return service.WebUIStatus{Enabled: m.addr != "", Addr: m.addr, Until: m.until}
}

// Enable switches the web UI on at addr (the configured address if empty)
// for d (without limit if zero). If it is on already, only the time limit
// changes, unless addr names another address.
func (m *Manager) Enable(addr string, d time.Duration) (service.WebUIStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.svc == nil {
		return service.WebUIStatus{}, errors.New("the web UI is not connected to the service")
	}
	if addr == "" {
		addr = m.o.Addr
	}
	if addr == "" {
		addr = DefaultAddr
	}
	if m.addr != "" && (addr == m.spec || addr == m.addr) {
		m.setLimit(d)
		return m.status(), nil
	}
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return service.WebUIStatus{}, fmt.Errorf("invalid web UI address %q: %w", addr, err)
	}
	m.stop()
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", addr)
	if err != nil {
		return service.WebUIStatus{}, fmt.Errorf("the web UI cannot listen on %s: %w", addr, err)
	}
	m.auth.Reset()
	h := NewHandler(m.svc, m.auth, Options{Log: m.o.Log, Now: m.o.Now, Off: func() error { _, err := m.Disable(); return err }})
	m.srv = &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	m.addr, m.spec = ln.Addr().String(), addr
	m.done = make(chan struct{})
	go func(srv *http.Server, done chan struct{}) {
		defer close(done)
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			m.o.Log.Error("web UI stopped", "error", err)
		}
	}(m.srv, m.done)
	m.setLimit(d)
	m.o.Log.Info("web UI on", "addr", m.addr, "until", m.until)
	if !isLoopback(ln.Addr()) {
		m.o.Log.Warn("the web UI listens on a non-loopback address and speaks plain HTTP: put it behind a proxy that terminates TLS", "addr", m.addr)
	}
	return m.status(), nil
}

// setLimit sets or clears the time limit.
func (m *Manager) setLimit(d time.Duration) {
	if m.timer != nil {
		m.timer.Stop()
		m.timer = nil
	}
	m.until = time.Time{}
	m.gen++
	if d <= 0 {
		return
	}
	m.until = m.o.Now().Add(d)
	gen := m.gen
	m.timer = time.AfterFunc(d, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.gen != gen || m.addr == "" {
			return
		}
		m.stop()
		m.o.Log.Info("web UI switched off, its time is up")
	})
}

// stop shuts the server down. The caller holds mu.
func (m *Manager) stop() {
	if m.timer != nil {
		m.timer.Stop()
		m.timer = nil
	}
	m.gen++
	if m.srv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := m.srv.Shutdown(ctx); err != nil {
		_ = m.srv.Close()
	}
	<-m.done
	m.srv, m.addr, m.spec, m.until = nil, "", "", time.Time{}
	m.auth.Reset()
}

// Disable implements the daemon's WebUI interface.
func (m *Manager) Disable() (service.WebUIStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.addr != "" {
		m.stop()
		m.o.Log.Info("web UI off")
	}
	return m.status(), nil
}

// Close switches the web UI off for good.
func (m *Manager) Close() { _, _ = m.Disable() }

// LoginLink implements the daemon's WebUI interface: a link that logs a
// browser in once, within two minutes.
func (m *Manager) LoginLink() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.addr == "" {
		return "", errors.New("the web UI is off")
	}
	host, port, _ := net.SplitHostPort(m.addr)
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "127.0.0.1"
	}
	u := url.URL{Scheme: "http", Host: net.JoinHostPort(host, port), Path: "/login", RawQuery: url.Values{"code": {m.auth.NewCode()}}.Encode()}
	return u.String(), nil
}

func isLoopback(a net.Addr) bool {
	t, ok := a.(*net.TCPAddr)
	return ok && t.IP.IsLoopback()
}
