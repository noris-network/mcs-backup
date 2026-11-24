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

	// op serializes switching on and off, which waits for the server; mu
	// guards the state below and is never held while waiting, so that the
	// status can always be read. op is taken before mu.
	op sync.Mutex

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
	m.op.Lock()
	defer m.op.Unlock()

	m.mu.Lock()
	svc := m.svc
	if svc == nil {
		m.mu.Unlock()
		return service.WebUIStatus{}, errors.New("the web UI is not connected to the service")
	}
	if addr == "" {
		addr = m.o.Addr
	}
	if addr == "" {
		addr = DefaultAddr
	}
	if m.addr != "" && (addr == m.spec || addr == m.addr) {
		defer m.mu.Unlock()
		m.setLimitLocked(d)
		return m.status(), nil
	}
	m.mu.Unlock()

	if _, _, err := net.SplitHostPort(addr); err != nil {
		return service.WebUIStatus{}, fmt.Errorf("invalid web UI address %q: %w", addr, err)
	}
	m.stop()
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", addr)
	if err != nil {
		return service.WebUIStatus{}, fmt.Errorf("the web UI cannot listen on %s: %w", addr, err)
	}
	m.auth.Reset()
	h := NewHandler(svc, m.auth, Options{Log: m.o.Log, Now: m.o.Now, Off: func() error { _, err := m.Disable(); return err }})
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			m.o.Log.Error("web UI stopped", "error", err)
		}
	}()

	m.mu.Lock()
	defer m.mu.Unlock()
	m.srv, m.done = srv, done
	m.addr, m.spec = ln.Addr().String(), addr
	m.setLimitLocked(d)
	m.o.Log.Info("web UI on", "addr", m.addr, "until", m.until)
	if !isLoopback(ln.Addr()) {
		m.o.Log.Warn("the web UI listens on a non-loopback address and speaks plain HTTP: put it behind a proxy that terminates TLS", "addr", m.addr)
	}
	return m.status(), nil
}

// setLimitLocked sets or clears the time limit. The caller holds mu.
func (m *Manager) setLimitLocked(d time.Duration) {
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
		m.op.Lock()
		defer m.op.Unlock()
		m.mu.Lock()
		stale := m.gen != gen || m.addr == ""
		m.mu.Unlock()
		if stale {
			return
		}
		m.stop()
		m.o.Log.Info("web UI switched off, its time is up")
	})
}

// stop shuts the server down and waits for it. The caller holds op but not
// mu: the web UI counts as off from the first moment, and its status can be
// read while the open requests are given time to end.
func (m *Manager) stop() {
	m.mu.Lock()
	if m.timer != nil {
		m.timer.Stop()
		m.timer = nil
	}
	m.gen++
	srv, done := m.srv, m.done
	m.srv, m.done, m.addr, m.spec, m.until = nil, nil, "", "", time.Time{}
	m.mu.Unlock()
	if srv == nil {
		return
	}
	m.auth.Reset()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		_ = srv.Close()
	}
	<-done
}

// Disable implements the daemon's WebUI interface.
func (m *Manager) Disable() (service.WebUIStatus, error) {
	m.op.Lock()
	defer m.op.Unlock()
	m.mu.Lock()
	on := m.addr != ""
	m.mu.Unlock()
	if on {
		m.stop()
		m.o.Log.Info("web UI off")
	}
	return m.Status(), nil
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
