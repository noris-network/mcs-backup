// Package webui is the optional web interface. It is built from html/template
// and a pinned copy of htmx that is embedded in the binary; there is no build
// step and nothing is loaded from the internet. Every request needs a login.
package webui

import (
	"embed"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/noris-network/mcs-backup/v2/internal/present"
	"github.com/noris-network/mcs-backup/v2/internal/service"
)

//go:embed static templates
var assets embed.FS

const (
	sessionCookie = "mcs_session"
	flashCookie   = "mcs_flash"
)

// Options configure the handler.
type Options struct {
	Log *slog.Logger
	Now func() time.Time
	// Off switches the web UI off; nil hides the button.
	Off func() error
}

// Handler serves the web UI.
type Handler struct {
	svc   service.Service
	auth  *Auth
	o     Options
	pages map[string]*template.Template
	mux   *http.ServeMux

	mu          sync.Mutex
	lastRestore *service.RestoreRequest // the last restore started here, to offer a repeat
}

// NewHandler creates the handler. All pages need a session from auth or its Basic Auth token.
func NewHandler(svc service.Service, auth *Auth, o Options) http.Handler {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	h := &Handler{svc: svc, auth: auth, o: o, pages: map[string]*template.Template{}, mux: http.NewServeMux()}
	funcs := template.FuncMap{
		"bytes": present.Bytes, "stamp": present.Stamp, "short": present.Short, "flags": present.SnapshotFlags, "kindname": present.KindName,
		"ago":      func(t time.Time) string { return present.Ago(o.Now(), t) },
		"duration": func(d service.Duration) string { return present.Duration(d.Std()) },
		"join":     strings.Join,
		"percent":  func(f float64) int { return int(math.Round(f * 100)) },
		"flagclass": func(flag string) string {
			switch flag {
			case "keep":
				return "ok"
			case "SUSPICIOUS":
				return "bad"
			case "pre-restore":
				return "warn"
			}
			return ""
		},
	}
	for _, name := range []string{"overview", "snapshots", "locks", "access", "log", "restore", "restore_check", "files", "diff", "login_failed", "off"} {
		h.pages[name] = template.Must(template.New("").Funcs(funcs).ParseFS(assets, "templates/layout.html", "templates/live.html", "templates/"+name+".html"))
	}
	h.routes()
	return secure(h.mux)
}

func (h *Handler) routes() {
	m := h.mux
	m.Handle("GET /static/", http.FileServerFS(assets))
	m.HandleFunc("GET /login", h.login)

	auth := func(pattern string, f http.HandlerFunc) { m.Handle(pattern, h.authed(f)) }
	auth("GET /{$}", h.overview)
	auth("GET /ui/live", h.live)
	auth("GET /snapshots", h.snapshots)
	auth("GET /locks", h.locks)
	auth("GET /access", h.access)
	auth("GET /log/{kind}", h.logPage)
	auth("GET /files", h.files)
	auth("GET /diff", h.diff)
	auth("GET /restore", h.restoreForm)
	auth("POST /restore/check", h.restoreCheck)

	auth("POST /restore", h.action("/", h.restore))
	auth("POST /backup", h.action("/", h.backup))
	auth("POST /verify", h.action("/", h.verify))
	auth("POST /cancel", h.action("/", h.cancel))
	auth("POST /schedule", h.action("/", h.schedule))
	auth("POST /notify-test", h.action("/", h.notifyTest))
	auth("POST /forget/approve", h.action("/snapshots", h.approveForget))
	auth("POST /snapshots/{id}/keep", h.action("/snapshots", h.keep(true)))
	auth("POST /snapshots/{id}/unkeep", h.action("/snapshots", h.keep(false)))
	auth("POST /snapshots/{id}/acknowledge", h.action("/snapshots", h.acknowledge))
	auth("POST /locks/unlock", h.action("/locks", h.unlock))
	auth("POST /web/off", h.webOff)
	auth("POST /logout", h.logout)
}

// secure adds the headers that keep the pages from being framed, sniffed or
// loaded with foreign scripts, and rejects cross-origin changes.
func secure(next http.Handler) http.Handler {
	cop := http.NewCrossOriginProtection()
	return cop.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		hd.Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'; form-action 'self'; base-uri 'none'")
		hd.Set("X-Frame-Options", "DENY")
		hd.Set("X-Content-Type-Options", "nosniff")
		hd.Set("Referrer-Policy", "no-referrer")
		hd.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	}))
}

// authed lets a request pass if it has a live session or the Basic Auth token.
func (h *Handler) authed(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(sessionCookie); err == nil && h.auth.Valid(c.Value) {
			next(w, r)
			return
		}
		if _, pass, ok := r.BasicAuth(); ok {
			ok, wait := h.auth.CheckToken(pass)
			if ok {
				next(w, r)
				return
			}
			if wait > 0 {
				h.o.Log.WarnContext(r.Context(), "web UI token locked after too many wrong tokens", "remote", r.RemoteAddr)
				w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(wait.Seconds()))))
				http.Error(w, "too many wrong tokens, try again later", http.StatusTooManyRequests)
				return
			}
		}
		if h.auth.HasToken() {
			w.Header().Set("WWW-Authenticate", `Basic realm="mcs-backup", charset="UTF-8"`)
		}
		w.WriteHeader(http.StatusUnauthorized)
		h.render(w, r, "login_failed", "", nil)
	})
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	session, ok := h.auth.Redeem(r.URL.Query().Get("code"))
	if !ok {
		h.o.Log.WarnContext(r.Context(), "web UI login refused", "remote", r.RemoteAddr)
		w.WriteHeader(http.StatusUnauthorized)
		h.render(w, r, "login_failed", "", nil)
		return
	}
	h.o.Log.InfoContext(r.Context(), "web UI login", "remote", r.RemoteAddr)
	setCookie(w, r, sessionCookie, session, 0)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		h.auth.Revoke(c.Value)
	}
	setCookie(w, r, sessionCookie, "", -1)
	w.WriteHeader(http.StatusUnauthorized)
	h.render(w, r, "login_failed", "", nil)
}

// setCookie sets a cookie that scripts cannot read and that is not sent along
// with requests from other sites. Secure is set when the request came over
// HTTPS, which is the case behind a TLS terminating proxy; the web UI itself
// speaks plain HTTP.
func setCookie(w http.ResponseWriter, r *http.Request, name, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure follows the request, see above
		Name: name, Value: value, Path: "/", MaxAge: maxAge, HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: isHTTPS(r),
	})
}

func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

// ctx returns the context for calls to the service, which records where the
// request came from.
func (h *Handler) ctx(r *http.Request) *http.Request {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return r.WithContext(service.WithSource(r.Context(), service.SourceWeb+"@"+host))
}

// page is what every template gets.
type page struct {
	Nav        string
	Flash      *flash
	Data       any
	Status     *service.Status
	AppendOnly bool
	CanOff     bool
	// Health is the word and the color of the indicator in the header.
	Health, HealthLevel string
}

type flash struct {
	OK   bool
	Text string
}

// render writes a page. It shows and clears the flash message left by an action.
func (h *Handler) render(w http.ResponseWriter, r *http.Request, name, nav string, data any) {
	p := page{Nav: nav, Data: data, CanOff: h.o.Off != nil}
	if c, err := r.Cookie(flashCookie); err == nil {
		if ok, text, found := strings.Cut(c.Value, "|"); found {
			if t, err := url.QueryUnescape(text); err == nil {
				p.Flash = &flash{OK: ok == "ok", Text: t}
			}
		}
		setCookie(w, r, flashCookie, "", -1)
	}
	if nav != "" {
		if st, err := h.svc.Status(r.Context()); err == nil {
			p.Status, p.AppendOnly = &st, st.AppendOnly
			label, level := present.Health(st)
			p.Health, p.HealthLevel = label, string(level)
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.pages[name].ExecuteTemplate(w, "page", p); err != nil {
		h.o.Log.ErrorContext(r.Context(), "web UI page failed", "page", name, "error", err)
	}
}

func (h *Handler) setFlash(w http.ResponseWriter, r *http.Request, ok bool, text string) {
	kind := "err"
	if ok {
		kind = "ok"
	}
	setCookie(w, r, flashCookie, kind+"|"+url.QueryEscape(text), 0)
}

// action wraps a form post: it runs fn, leaves a message and goes back to the page.
func (h *Handler) action(back string, fn func(*http.Request) (string, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r = h.ctx(r)
		msg, err := fn(r)
		if err != nil {
			h.o.Log.InfoContext(r.Context(), "web UI action failed", "path", r.URL.Path, "error", err)
			h.setFlash(w, r, false, describe(err))
		} else {
			h.setFlash(w, r, true, msg)
		}
		http.Redirect(w, r, back, http.StatusSeeOther)
	}
}

// describe turns an error of the service into a sentence for the page.
func describe(err error) string {
	if b, ok := errors.AsType[*service.BusyError](err); ok {
		return fmt.Sprintf("Another operation is running (%s). Wait for it or cancel it.", present.KindName(b.Current.Kind))
	}
	switch {
	case errors.Is(err, service.ErrConfirm):
		return "The confirmation did not match; nothing was done."
	case errors.Is(err, service.ErrAppendOnly):
		return "Not possible: the repository is append-only."
	case errors.Is(err, service.ErrUnavailable):
		s := present.Unavailable(err)
		return strings.ToUpper(s[:1]) + s[1:] + "."
	}
	s := err.Error()
	if s == "" {
		return "Failed without an error message."
	}
	first, size := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(first)) + s[size:]
}
