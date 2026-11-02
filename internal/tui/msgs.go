package tui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/noris-network/mcs-backup/v2/internal/service"
)

// Messages that carry the result of a call to the service.
type (
	statusMsg struct {
		st  service.Status
		err error
	}
	snapshotsMsg struct {
		list []service.Snapshot
		err  error
	}
	locksMsg struct {
		list []service.Lock
		err  error
	}
	retentionMsg struct {
		info service.RetentionInfo
		err  error
	}
	envMsg struct {
		info     service.EnvInfo
		revealed bool
		err      error
	}
	logMsg struct {
		log service.Log
		err error
	}
	preflightMsg struct {
		req service.RestoreRequest
		pre service.RestorePreflight
		err error
	}
	// doneMsg reports that an action finished; note replaces the default
	// confirmation.
	doneMsg struct {
		what string
		note string
		err  error
	}
	linkMsg struct {
		url string
		err error
	}
	eventMsg  struct{ ev service.Event }
	streamMsg struct{ err error }
	tickMsg   time.Time
)

const tickEvery = 150 * time.Millisecond

func tick() tea.Cmd {
	return tea.Tick(tickEvery, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func fetchStatus(ctx context.Context, svc service.Service) tea.Cmd {
	return func() tea.Msg {
		st, err := svc.Status(ctx)
		return statusMsg{st, err}
	}
}

func fetchSnapshots(ctx context.Context, svc service.Service) tea.Cmd {
	return func() tea.Msg {
		list, err := svc.Snapshots(ctx)
		return snapshotsMsg{list, err}
	}
}

func fetchLocks(ctx context.Context, svc service.Service) tea.Cmd {
	return func() tea.Msg {
		list, err := svc.Locks(ctx)
		return locksMsg{list, err}
	}
}

func fetchRetention(ctx context.Context, svc service.Service) tea.Cmd {
	return func() tea.Msg {
		info, err := svc.Retention(ctx)
		return retentionMsg{info, err}
	}
}

func fetchEnv(ctx context.Context, svc service.Service, reveal bool) tea.Cmd {
	return func() tea.Msg {
		info, err := svc.Env(ctx, reveal)
		return envMsg{info, reveal, err}
	}
}

func fetchLog(ctx context.Context, svc service.Service, kind service.Kind) tea.Cmd {
	return func() tea.Msg {
		l, err := svc.Log(ctx, kind)
		return logMsg{l, err}
	}
}

func fetchPreflight(ctx context.Context, svc service.Service, req service.RestoreRequest) tea.Cmd {
	return func() tea.Msg {
		pre, err := svc.RestorePreflight(ctx, req)
		return preflightMsg{req, pre, err}
	}
}

// act runs fn in the background and reports the outcome as a doneMsg.
func act(what string, fn func() error) tea.Cmd {
	return func() tea.Msg { return doneMsg{what: what, err: fn()} }
}

// pasteMsg carries text that is shown for selecting and pasting.
type pasteMsg struct{ text string }
