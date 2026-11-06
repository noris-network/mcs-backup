package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"iter"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/noris-network/mcs-backup/v2/internal/service"
)

// Client talks to a running service and implements service.Service.
type Client struct {
	base   string
	hc     *http.Client
	source string
	socket string
}

var _ service.Service = (*Client)(nil)

// NewClient returns a client for the service on the Unix socket. source
// (service.SourceCLI, ...) is sent with every request and logged by the service.
func NewClient(socket, source string) *Client {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
		MaxIdleConns:    2,
		IdleConnTimeout: 30 * time.Second,
	}
	c := newClient("http://mcs-backup", &http.Client{Transport: tr}, source)
	c.socket = socket
	return c
}

// As returns a client for the same service that reports source with its
// requests. It shares the connection with c.
func (c *Client) As(source string) service.Service {
	cp := *c
	cp.source = source
	return &cp
}

func newClient(base string, hc *http.Client, source string) *Client {
	return &Client{base: base, hc: hc, source: source}
}

// Close releases idle connections.
func (c *Client) Close() { c.hc.CloseIdleConnections() }

// do sends a request and decodes the response into out (if not nil).
func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	resp, err := c.send(ctx, method, path, in, "application/json")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent || out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.UnmarshalRead(resp.Body, out); err != nil {
		return fmt.Errorf("unexpected response from the service: %w", err)
	}
	return nil
}

// send performs the request and turns error responses into errors.
func (c *Client) send(ctx context.Context, method, path string, in any, accept string) (*http.Response, error) {
	var rd io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd) //nolint:gosec // the URL is the fixed base of the local socket
	if err != nil {
		return nil, err
	}
	req.Header.Set(sourceHeader, c.source)
	req.Header.Set("Accept", accept)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req) //nolint:gosec // the URL is the fixed base of the local socket
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		where := ""
		if c.socket != "" {
			where = " at " + c.socket
		}
		return nil, fmt.Errorf("%w%s: %w", ErrUnreachable, where, unwrapURLError(err))
	}
	if resp.StatusCode >= 300 {
		defer resp.Body.Close()
		var we wireError
		if err := json.UnmarshalRead(resp.Body, &we); err != nil || we.Error == "" {
			return nil, &remoteError{msg: "the service answered " + resp.Status, status: resp.StatusCode}
		}
		return nil, fromWire(resp.StatusCode, we)
	}
	return resp, nil
}

func unwrapURLError(err error) error {
	if ue, ok := errors.AsType[*url.Error](err); ok {
		return ue.Err
	}
	return err
}

func get[T any](ctx context.Context, c *Client, path string) (T, error) {
	var v T
	err := c.do(ctx, http.MethodGet, path, nil, &v)
	return v, err
}

func post[T any](ctx context.Context, c *Client, path string, in any) (T, error) {
	var v T
	err := c.do(ctx, http.MethodPost, path, in, &v)
	return v, err
}

// Status implements service.Service.
func (c *Client) Status(ctx context.Context) (service.Status, error) {
	return get[service.Status](ctx, c, "/v1/status")
}

// Snapshots implements service.Service.
func (c *Client) Snapshots(ctx context.Context) ([]service.Snapshot, error) {
	return get[[]service.Snapshot](ctx, c, "/v1/snapshots")
}

// Retention implements service.Service.
func (c *Client) Retention(ctx context.Context) (service.RetentionInfo, error) {
	return get[service.RetentionInfo](ctx, c, "/v1/retention")
}

// Locks implements service.Service.
func (c *Client) Locks(ctx context.Context) ([]service.Lock, error) {
	return get[[]service.Lock](ctx, c, "/v1/locks")
}

// Files implements service.Service.
func (c *Client) Files(ctx context.Context, req service.FilesRequest) (service.FileList, error) {
	q := url.Values{}
	if req.Snapshot != "" {
		q.Set("snapshot", req.Snapshot)
	}
	if req.Path != "" {
		q.Set("path", req.Path)
	}
	return get[service.FileList](ctx, c, "/v1/files?"+q.Encode())
}

// Find implements service.Service.
func (c *Client) Find(ctx context.Context, req service.FindRequest) (service.FindResult, error) {
	q := url.Values{"pattern": {req.Pattern}}
	if req.Snapshot != "" {
		q.Set("snapshot", req.Snapshot)
	}
	if req.IgnoreCase {
		q.Set("ignore_case", "true")
	}
	return get[service.FindResult](ctx, c, "/v1/find?"+q.Encode())
}

// RestorePreflight implements service.Service.
func (c *Client) RestorePreflight(ctx context.Context, req service.RestoreRequest) (service.RestorePreflight, error) {
	q := url.Values{"snapshot": {req.Snapshot}}
	if req.Target != "" {
		q.Set("target", req.Target)
	}
	for _, inc := range req.Includes {
		q.Add("include", inc)
	}
	if req.Mode != "" {
		q.Set("mode", string(req.Mode))
	}
	if req.NoSafetySnapshot {
		q.Set("no_safety_snapshot", "true")
	}
	if req.IgnoreSpaceWarning {
		q.Set("ignore_space_warning", "true")
	}
	return get[service.RestorePreflight](ctx, c, "/v1/restore/preflight?"+q.Encode())
}

// Operation implements service.Service.
func (c *Client) Operation(ctx context.Context) (*service.Operation, error) {
	resp, err := c.send(ctx, http.MethodGet, "/v1/operation", nil, "application/json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		return nil, nil
	}
	var op service.Operation
	if err := json.UnmarshalRead(resp.Body, &op); err != nil {
		return nil, fmt.Errorf("unexpected response from the service: %w", err)
	}
	return &op, nil
}

// Log implements service.Service.
func (c *Client) Log(ctx context.Context, kind service.Kind) (service.Log, error) {
	return get[service.Log](ctx, c, "/v1/runs/"+url.PathEscape(string(kind))+"/log")
}

// Env implements service.Service.
func (c *Client) Env(ctx context.Context, reveal bool) (service.EnvInfo, error) {
	path := "/v1/env"
	if reveal {
		path += "?reveal=true"
	}
	return get[service.EnvInfo](ctx, c, path)
}

// StartBackup implements service.Service.
func (c *Client) StartBackup(ctx context.Context, req service.BackupRequest) (service.Operation, error) {
	return post[service.Operation](ctx, c, "/v1/backup", req)
}

// StartRestore implements service.Service.
func (c *Client) StartRestore(ctx context.Context, req service.RestoreRequest) (service.Operation, error) {
	return post[service.Operation](ctx, c, "/v1/restore", req)
}

// StartVerify implements service.Service.
func (c *Client) StartVerify(ctx context.Context, req service.VerifyRequest) (service.Operation, error) {
	return post[service.Operation](ctx, c, "/v1/verify", req)
}

// ApproveForget implements service.Service.
func (c *Client) ApproveForget(ctx context.Context, confirm string) (service.Operation, error) {
	return post[service.Operation](ctx, c, "/v1/retention/approve", confirmBody{Confirm: confirm})
}

// Cancel implements service.Service.
func (c *Client) Cancel(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, "/v1/operation/cancel", nil, nil)
}

// Keep implements service.Service.
func (c *Client) Keep(ctx context.Context, ref string) (service.TagResult, error) {
	var v service.TagResult
	err := c.do(ctx, http.MethodPut, "/v1/snapshots/"+url.PathEscape(ref)+"/keep", nil, &v)
	return v, err
}

// Unkeep implements service.Service.
func (c *Client) Unkeep(ctx context.Context, ref string) (service.TagResult, error) {
	var v service.TagResult
	err := c.do(ctx, http.MethodDelete, "/v1/snapshots/"+url.PathEscape(ref)+"/keep", nil, &v)
	return v, err
}

// Acknowledge implements service.Service.
func (c *Client) Acknowledge(ctx context.Context, ref, confirm string) (service.TagResult, error) {
	return post[service.TagResult](ctx, c, "/v1/snapshots/"+url.PathEscape(ref)+"/acknowledge", confirmBody{Confirm: confirm})
}

// Unlock implements service.Service.
func (c *Client) Unlock(ctx context.Context, all bool, confirm string) error {
	return c.do(ctx, http.MethodPost, "/v1/locks/unlock", unlockBody{All: all, Confirm: confirm}, nil)
}

// SetSchedule implements service.Service.
func (c *Client) SetSchedule(ctx context.Context, req service.ScheduleRequest) (service.Status, error) {
	return post[service.Status](ctx, c, "/v1/schedule", req)
}

// NotifyTest implements service.Service.
func (c *Client) NotifyTest(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, "/v1/notify/test", nil, nil)
}

// WebUI implements service.Service.
func (c *Client) WebUI(ctx context.Context) (service.WebUIStatus, error) {
	return get[service.WebUIStatus](ctx, c, "/v1/webui")
}

// SetWebUI implements service.Service.
func (c *Client) SetWebUI(ctx context.Context, req service.WebUIRequest) (service.WebUIStatus, error) {
	return post[service.WebUIStatus](ctx, c, "/v1/webui", req)
}

// LoginLink implements service.Service.
func (c *Client) LoginLink(ctx context.Context) (string, error) {
	v, err := post[linkBody](ctx, c, "/v1/webui/login-link", nil)
	return v.URL, err
}

// Events implements service.Service: it follows the server-sent event stream
// until ctx ends or the service closes it.
func (c *Client) Events(ctx context.Context) iter.Seq2[service.Event, error] {
	return func(yield func(service.Event, error) bool) {
		resp, err := c.send(ctx, http.MethodGet, "/v1/operation/events", nil, "text/event-stream")
		if err != nil {
			yield(service.Event{}, err)
			return
		}
		defer resp.Body.Close()

		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64<<10), maxBody)
		var name, data string
		for sc.Scan() {
			line := sc.Text()
			switch {
			case line == "":
				if data == "" {
					name = ""
					continue
				}
				if name == "error" {
					var we wireError
					_ = json.Unmarshal([]byte(data), &we)
					yield(service.Event{}, fromWire(0, we))
					return
				}
				var ev service.Event
				if err := json.Unmarshal([]byte(data), &ev); err != nil {
					yield(service.Event{}, fmt.Errorf("unexpected event: %w", err))
					return
				}
				if !yield(ev, nil) {
					return
				}
				name, data = "", ""
			case strings.HasPrefix(line, ":"): // heartbeat
			case strings.HasPrefix(line, "event:"):
				name = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			case strings.HasPrefix(line, "data:"):
				data += strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " ")
			}
		}
		if err := sc.Err(); err != nil && ctx.Err() == nil {
			yield(service.Event{}, fmt.Errorf("%w: the event stream ended: %w", ErrUnreachable, err))
		}
	}
}
