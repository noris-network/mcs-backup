// Package api serves service.Service over HTTP/JSON on a Unix socket and
// provides a client that implements service.Service again.
package api

import (
	"errors"
	"net/http"

	"github.com/noris-network/mcs-backup/v2/internal/service"
)

// Request header that tells where a request comes from (cli, tui, web, mcp).
const sourceHeader = "X-Mcs-Source"

// Error codes on the wire.
const (
	codeBusy        = "busy"
	codeConfirm     = "confirm"
	codeNotFound    = "not_found"
	codeAppendOnly  = "append_only"
	codeUnavailable = "unavailable"
	codeBadRequest  = "bad_request"
	codeError       = "error"
)

// wireError is the body of every error response.
type wireError struct {
	Error string `json:"error"`
	Code  string `json:"code"`
	// Current is the running operation of a busy error.
	Current *service.Operation `json:"current,omitempty"`
}

// toWire maps an error of the service to an HTTP status and error body.
func toWire(err error) (int, wireError) {
	if busy, ok := errors.AsType[*service.BusyError](err); ok {
		cur := busy.Current
		return http.StatusConflict, wireError{Error: err.Error(), Code: codeBusy, Current: &cur}
	}
	switch {
	case errors.Is(err, service.ErrConfirm):
		return http.StatusBadRequest, wireError{Error: err.Error(), Code: codeConfirm}
	case errors.Is(err, service.ErrNotFound):
		return http.StatusNotFound, wireError{Error: err.Error(), Code: codeNotFound}
	case errors.Is(err, service.ErrAppendOnly):
		return http.StatusForbidden, wireError{Error: err.Error(), Code: codeAppendOnly}
	case errors.Is(err, service.ErrUnavailable):
		return http.StatusNotImplemented, wireError{Error: err.Error(), Code: codeUnavailable}
	}
	return http.StatusInternalServerError, wireError{Error: err.Error(), Code: codeError}
}

// remoteError is an error reported by the service. It keeps the service's
// message and still satisfies errors.Is for the sentinel of its code.
type remoteError struct {
	msg      string
	sentinel error
	status   int
}

func (e *remoteError) Error() string { return e.msg }

func (e *remoteError) Is(target error) bool { return e.sentinel != nil && target == e.sentinel }

// StatusCode returns the HTTP status of the response.
func (e *remoteError) StatusCode() int { return e.status }

// fromWire maps an error response back to an error.
func fromWire(status int, w wireError) error {
	switch w.Code {
	case codeBusy:
		be := &service.BusyError{}
		if w.Current != nil {
			be.Current = *w.Current
		}
		return be
	case codeConfirm:
		return &remoteError{w.Error, service.ErrConfirm, status}
	case codeNotFound:
		return &remoteError{w.Error, service.ErrNotFound, status}
	case codeAppendOnly:
		return &remoteError{w.Error, service.ErrAppendOnly, status}
	case codeUnavailable:
		return &remoteError{w.Error, service.ErrUnavailable, status}
	}
	return &remoteError{w.Error, nil, status}
}

// ErrUnreachable: the service does not answer on its socket.
var ErrUnreachable = errors.New("cannot reach the mcs-backup service")

// ErrInstanceRunning: another service already answers on the socket.
var ErrInstanceRunning = errors.New("instance already running")
