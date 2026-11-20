// Package mcpserver exposes the service to AI agents over the Model Context
// Protocol. What an agent may do depends on the tier the operator chose:
// reading is always possible, changing needs Write, and everything that can
// destroy data needs Destructive. Tools above the tier are not registered at
// all, so the agent does not even see them.
//
// The tiers are guard rails for well-meaning agents, not a security boundary:
// whoever can start this server (kubectl exec, a shell) can do everything.
package mcpserver

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/noris-network/mcs-backup/v2/internal/present"
	"github.com/noris-network/mcs-backup/v2/internal/service"
)

// Options select what an agent may do.
type Options struct {
	// Write allows operations that add or change things: backups, tags,
	// restores into an empty directory, schedule changes.
	Write bool
	// Destructive allows operations that can lose data: restores over live
	// data or into a directory with files, acknowledging a suspicious backup,
	// approving a held forget, removing all locks. It implies Write.
	Destructive bool
	Version     string
	Log         *slog.Logger
}

// Server serves one service to one agent.
type Server struct {
	svc service.Service
	o   Options
	// appendOnly: the repository is append-only, so tools that change tags
	// or delete are not offered.
	appendOnly bool
	// prunable: a repository is not append-only, so prune has something to do.
	prunable bool
	host     string
	srv      *mcp.Server
}

// New builds the server. It asks the service once for what it needs to decide
// which tools exist, so the service has to be reachable.
func New(ctx context.Context, svc service.Service, o Options) (*Server, error) {
	if o.Destructive {
		o.Write = true
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	st, err := svc.Status(service.WithSource(ctx, service.SourceMCP))
	if err != nil {
		return nil, err
	}
	s := &Server{svc: svc, o: o, appendOnly: st.AppendOnly, host: st.Host, prunable: !st.AppendOnly || st.Copy != nil}
	s.srv = mcp.NewServer(&mcp.Implementation{Name: "mcs-backup", Version: o.Version}, &mcp.ServerOptions{
		Instructions: s.instructions(st),
		Logger:       o.Log,
	})
	s.registerRead()
	if o.Write {
		s.registerWrite()
	}
	if o.Destructive {
		s.registerDestructive()
	}
	return s, nil
}

// MCP returns the underlying server, for tests and custom transports.
func (s *Server) MCP() *mcp.Server { return s.srv }

// Run serves the agent on in and out until it disconnects or ctx ends.
func (s *Server) Run(ctx context.Context, in io.ReadCloser, out io.WriteCloser) error {
	return s.srv.Run(ctx, &mcp.IOTransport{Reader: in, Writer: out})
}

func (s *Server) instructions(st service.Status) string {
	tier := "read-only: you can look at the state of the backups, but not change anything"
	switch {
	case s.o.Destructive:
		tier = "read, write and destructive operations are allowed"
	case s.o.Write:
		tier = "read and write operations are allowed; operations that can lose data are not available"
	}
	text := fmt.Sprintf(`This is mcs-backup, a restic based backup service. Instance: host %q, repository %s.
Access level: %s.

Rules:
- One operation runs at a time. start_* tools return at once; use get_operation (with wait_seconds) to wait for the result. A busy error names the running operation.
- Look before you act: get_status shows warnings that need attention, list_snapshots the snapshots, get_restore_preflight what a restore would do.
- Restoring over live data, acknowledging a much smaller backup, approving a held forget and removing all locks lose data or protection. Do these only when the user asked for exactly that, and pass the confirmation word the tool asks for.
- The retention policy decides what is deleted; you cannot delete snapshots directly.
- Secrets are never shown. get_restic_env lists variable names only.`, s.host, repoWord(st), tier)
	if s.appendOnly {
		text += "\n- The repository is append-only: tags cannot be changed and nothing is ever deleted, so the tools for those are not offered."
	}
	return text
}

func repoWord(st service.Status) string {
	if st.Repository.Up {
		return "reachable"
	}
	return "NOT reachable at the moment"
}

// ctx marks calls as coming from an agent.
func ctx(c context.Context) context.Context { return service.WithSource(c, service.SourceMCP) }

// reply renders v as the text of a tool result.
func reply(v any) (*mcp.CallToolResult, any, error) {
	data, err := json.Marshal(v, json.Deterministic(true), jsontext.Multiline(true))
	if err != nil {
		return nil, nil, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(data)}}}, nil, nil
}

// say renders a plain sentence as the text of a tool result.
func say(format string, args ...any) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(format, args...)}}}, nil, nil
}

// explain turns an error of the service into advice for the agent.
func explain(err error) error {
	if b, ok := errors.AsType[*service.BusyError](err); ok {
		return fmt.Errorf("another operation is running (%s, started by %s). Wait for it with get_operation or cancel it with cancel_operation", b.Current.Kind, b.Current.Source)
	}
	switch {
	case errors.Is(err, service.ErrConfirm):
		return fmt.Errorf("the confirmation was missing or wrong: %w", err)
	case errors.Is(err, service.ErrAppendOnly):
		return errors.New("not possible: the repository is append-only")
	case errors.Is(err, service.ErrUnavailable):
		return errors.New(present.Unavailable(err))
	}
	return err
}

// readOnly, additive and destructive are the annotations of the tiers.
var (
	readOnly    = &mcp.ToolAnnotations{ReadOnlyHint: true}
	additive    = &mcp.ToolAnnotations{DestructiveHint: new(false)}
	destructive = &mcp.ToolAnnotations{DestructiveHint: new(true)}
)

type none struct{}
