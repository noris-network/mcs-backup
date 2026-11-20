package api

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/noris-network/mcs-backup/v2/internal/service"
)

// maxBody limits the size of request bodies.
const maxBody = 1 << 20

// heartbeat keeps event streams alive through proxies and detects dead clients.
const heartbeat = 15 * time.Second

type server struct {
	svc service.Service
	log *slog.Logger
}

// NewHandler returns the HTTP handler for the /v1 API.
func NewHandler(svc service.Service, log *slog.Logger) http.Handler {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	s := &server{svc: svc, log: log}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /v1/status", reply(s, func(r *http.Request) (any, error) { return s.svc.Status(r.Context()) }))
	mux.HandleFunc("GET /v1/snapshots", reply(s, func(r *http.Request) (any, error) { return s.svc.Snapshots(r.Context()) }))
	mux.HandleFunc("GET /v1/retention", reply(s, func(r *http.Request) (any, error) { return s.svc.Retention(r.Context()) }))
	mux.HandleFunc("GET /v1/locks", reply(s, func(r *http.Request) (any, error) { return s.svc.Locks(r.Context()) }))
	mux.HandleFunc("GET /v1/files", reply(s, func(r *http.Request) (any, error) {
		q := r.URL.Query()
		return s.svc.Files(r.Context(), service.FilesRequest{Snapshot: q.Get("snapshot"), Path: q.Get("path")})
	}))
	mux.HandleFunc("GET /v1/find", reply(s, func(r *http.Request) (any, error) {
		q := r.URL.Query()
		return s.svc.Find(r.Context(), service.FindRequest{
			Pattern: q.Get("pattern"), Snapshot: q.Get("snapshot"), IgnoreCase: q.Get("ignore_case") == "true",
		})
	}))
	mux.HandleFunc("GET /v1/diff", reply(s, func(r *http.Request) (any, error) {
		q := r.URL.Query()
		return s.svc.Diff(r.Context(), service.DiffRequest{From: q.Get("from"), To: q.Get("to")})
	}))
	mux.HandleFunc("GET /v1/restore/preflight", reply(s, func(r *http.Request) (any, error) {
		req, err := preflightRequest(r.URL.Query())
		if err != nil {
			return nil, badRequest(err)
		}
		return s.svc.RestorePreflight(r.Context(), req)
	}))
	mux.HandleFunc("GET /v1/runs/{kind}/log", reply(s, func(r *http.Request) (any, error) {
		return s.svc.Log(r.Context(), service.Kind(r.PathValue("kind")))
	}))
	mux.HandleFunc("GET /v1/env", reply(s, func(r *http.Request) (any, error) {
		return s.svc.Env(r.Context(), r.URL.Query().Get("reveal") == "true")
	}))
	mux.HandleFunc("GET /v1/webui", reply(s, func(r *http.Request) (any, error) { return s.svc.WebUI(r.Context()) }))

	mux.HandleFunc("GET /v1/operation", s.operation)
	mux.HandleFunc("GET /v1/operation/events", s.events)
	mux.HandleFunc("POST /v1/operation/cancel", s.noContent(func(r *http.Request) error { return s.svc.Cancel(r.Context()) }))

	mux.HandleFunc("POST /v1/backup", accepted(s, func(r *http.Request, req service.BackupRequest) (service.Operation, error) {
		return s.svc.StartBackup(r.Context(), req)
	}))
	mux.HandleFunc("POST /v1/restore", accepted(s, func(r *http.Request, req service.RestoreRequest) (service.Operation, error) {
		return s.svc.StartRestore(r.Context(), req)
	}))
	mux.HandleFunc("POST /v1/verify", accepted(s, func(r *http.Request, req service.VerifyRequest) (service.Operation, error) {
		return s.svc.StartVerify(r.Context(), req)
	}))
	mux.HandleFunc("POST /v1/prune", accepted(s, func(r *http.Request, _ struct{}) (service.Operation, error) {
		return s.svc.StartPrune(r.Context())
	}))
	mux.HandleFunc("POST /v1/check", accepted(s, func(r *http.Request, _ struct{}) (service.Operation, error) {
		return s.svc.StartCheck(r.Context())
	}))
	mux.HandleFunc("POST /v1/retention/approve", accepted(s, func(r *http.Request, req confirmBody) (service.Operation, error) {
		return s.svc.ApproveForget(r.Context(), req.Confirm)
	}))

	mux.HandleFunc("PUT /v1/snapshots/{id}/keep", reply(s, func(r *http.Request) (any, error) {
		return s.svc.Keep(r.Context(), r.PathValue("id"))
	}))
	mux.HandleFunc("DELETE /v1/snapshots/{id}/keep", reply(s, func(r *http.Request) (any, error) {
		return s.svc.Unkeep(r.Context(), r.PathValue("id"))
	}))
	mux.HandleFunc("POST /v1/snapshots/{id}/acknowledge", body(s, func(r *http.Request, req confirmBody) (any, error) {
		return s.svc.Acknowledge(r.Context(), r.PathValue("id"), req.Confirm)
	}))
	mux.HandleFunc("POST /v1/locks/unlock", body(s, func(r *http.Request, req unlockBody) (any, error) {
		return nil, s.svc.Unlock(r.Context(), req.All, req.Confirm)
	}))
	mux.HandleFunc("POST /v1/schedule", body(s, func(r *http.Request, req service.ScheduleRequest) (any, error) {
		return s.svc.SetSchedule(r.Context(), req)
	}))
	mux.HandleFunc("POST /v1/notify/test", s.noContent(func(r *http.Request) error { return s.svc.NotifyTest(r.Context()) }))
	mux.HandleFunc("POST /v1/webui", body(s, func(r *http.Request, req service.WebUIRequest) (any, error) {
		return s.svc.SetWebUI(r.Context(), req)
	}))
	mux.HandleFunc("POST /v1/webui/login-link", reply(s, func(r *http.Request) (any, error) {
		u, err := s.svc.LoginLink(r.Context())
		return linkBody{URL: u}, err
	}))

	return s.withSource(mux)
}

type confirmBody struct {
	Confirm string `json:"confirm"`
}

type unlockBody struct {
	All     bool   `json:"all"`
	Confirm string `json:"confirm"`
}

type linkBody struct {
	URL string `json:"url"`
}

// knownSources are the values of the source header that are accepted.
func knownSources() []string {
	return []string{service.SourceCLI, service.SourceTUI, service.SourceWeb, service.SourceMCP}
}

// withSource records where a request comes from.
func (s *server) withSource(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		src := r.Header.Get(sourceHeader)
		if !slices.Contains(knownSources(), src) {
			src = "unknown"
		}
		next.ServeHTTP(w, r.WithContext(service.WithSource(r.Context(), src)))
	})
}

func (s *server) writeJSON(w http.ResponseWriter, status int, v any) {
	// encode first: a value that cannot be encoded must not end as an empty 200
	data, err := json.Marshal(v)
	if err != nil {
		s.log.Error("cannot encode the response", "error", err)
		status, data = http.StatusInternalServerError, []byte(`{"error":"cannot encode the response","code":"error"}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := w.Write(data); err != nil {
		s.log.Warn("cannot write the response", "error", err)
	}
}

func (s *server) writeError(w http.ResponseWriter, err error) {
	if be, ok := errors.AsType[*badRequestError](err); ok {
		s.writeJSON(w, http.StatusBadRequest, wireError{Error: be.Error(), Code: codeBadRequest})
		return
	}
	status, we := toWire(err)
	s.writeJSON(w, status, we)
}

// badRequestError marks a malformed request.
type badRequestError struct{ err error }

func (e *badRequestError) Error() string { return e.err.Error() }
func (e *badRequestError) Unwrap() error { return e.err }

func badRequest(err error) error { return &badRequestError{err} }

// reply serves a handler without a request body.
func reply(s *server, fn func(*http.Request) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v, err := fn(r)
		if err != nil {
			s.writeError(w, err)
			return
		}
		s.writeJSON(w, http.StatusOK, v)
	}
}

// decode reads the JSON body into v. An empty body leaves v untouched.
func decode(r *http.Request, v any) error {
	data, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, maxBody))
	if err != nil {
		return badRequest(fmt.Errorf("read the request: %w", err))
	}
	if len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, v, json.RejectUnknownMembers(true)); err != nil {
		return badRequest(fmt.Errorf("invalid request: %w", err))
	}
	return nil
}

// body serves a handler with a JSON request body.
func body[T any](s *server, fn func(*http.Request, T) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req T
		if err := decode(r, &req); err != nil {
			s.writeError(w, err)
			return
		}
		v, err := fn(r, req)
		if err != nil {
			s.writeError(w, err)
			return
		}
		if v == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		s.writeJSON(w, http.StatusOK, v)
	}
}

// accepted serves a handler that starts an operation: 202 with the operation.
func accepted[T any](s *server, fn func(*http.Request, T) (service.Operation, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req T
		if err := decode(r, &req); err != nil {
			s.writeError(w, err)
			return
		}
		op, err := fn(r, req)
		if err != nil {
			s.writeError(w, err)
			return
		}
		s.writeJSON(w, http.StatusAccepted, op)
	}
}

func (s *server) noContent(fn func(*http.Request) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := fn(r); err != nil {
			s.writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *server) operation(w http.ResponseWriter, r *http.Request) {
	op, err := s.svc.Operation(r.Context())
	switch {
	case err != nil:
		s.writeError(w, err)
	case op == nil:
		w.WriteHeader(http.StatusNoContent)
	default:
		s.writeJSON(w, http.StatusOK, op)
	}
}

// preflightRequest reads a restore request from query parameters.
func preflightRequest(q url.Values) (service.RestoreRequest, error) {
	req := service.RestoreRequest{
		Snapshot: q.Get("snapshot"), Target: q.Get("target"), Includes: q["include"],
		Mode: service.RestoreMode(q.Get("mode")),
	}
	for name, dst := range map[string]*bool{
		"no_safety_snapshot": &req.NoSafetySnapshot, "ignore_space_warning": &req.IgnoreSpaceWarning, "preview": &req.Preview,
	} {
		if v := q.Get(name); v != "" {
			b, err := strconv.ParseBool(v)
			if err != nil {
				return req, fmt.Errorf("invalid %s: %q", name, v)
			}
			*dst = b
		}
	}
	if req.Snapshot == "" {
		req.Snapshot = "latest"
	}
	return req, nil
}

// events streams the service's events as Server-Sent Events.
func (s *server) events(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		s.writeError(w, errors.New("streaming is not supported"))
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fl.Flush()

	type item struct {
		ev  service.Event
		err error
	}
	items := make(chan item)
	go func() {
		defer close(items)
		for ev, err := range s.svc.Events(ctx) {
			select {
			case items <- item{ev, err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()

	tick := time.NewTicker(heartbeat)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			fmt.Fprint(w, ": ping\n\n")
		case it, ok := <-items:
			if !ok {
				return
			}
			if it.err != nil {
				data, _ := json.Marshal(wireError{Error: it.err.Error(), Code: codeError})
				fmt.Fprintf(w, "event: error\ndata: %s\n\n", data)
				fl.Flush()
				return
			}
			data, err := json.Marshal(it.ev)
			if err != nil {
				s.log.WarnContext(ctx, "cannot encode an event", "error", err)
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", data)
		}
		fl.Flush()
	}
}

// Listen opens the Unix socket at path. If another service already answers on
// it, ErrInstanceRunning is returned; a socket file left behind by a crashed
// service is replaced.
func Listen(ctx context.Context, path string) (net.Listener, error) {
	if fi, err := os.Lstat(path); err == nil {
		if fi.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("%s exists and is not a socket", path)
		}
		d := net.Dialer{Timeout: time.Second}
		if conn, err := d.DialContext(ctx, "unix", path); err == nil {
			_ = conn.Close()
			return nil, fmt.Errorf("%w: it answers on %s", ErrInstanceRunning, path)
		}
		if err := os.Remove(path); err != nil {
			return nil, fmt.Errorf("remove the stale socket: %w", err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, err
	}
	ln, err := (&net.ListenConfig{}).Listen(ctx, "unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o660); err != nil { //nolint:gosec // the owner and the group may use the socket
		_ = ln.Close()
		return nil, err
	}
	return ln, nil
}

// Serve serves the API on ln until ctx ends and removes the socket file.
func Serve(ctx context.Context, ln net.Listener, svc service.Service, log *slog.Logger) error {
	base, cancelBase := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelBase()
	srv := &http.Server{
		Handler:           NewHandler(svc, log),
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return base },
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	cancelBase()                                                                 // ends the event streams, which would otherwise hold the shutdown
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second) //nolint:contextcheck // must outlive the cancelled ctx
	defer cancel()
	err := srv.Shutdown(shutdown) //nolint:contextcheck // the shutdown must outlive the cancelled ctx
	if addr, ok := ln.Addr().(*net.UnixAddr); ok {
		_ = os.Remove(addr.Name)
	}
	if err != nil {
		return err
	}
	if err := <-errc; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
