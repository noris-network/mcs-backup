// Package tui is the terminal user interface of mcs-backup. It talks to the
// service through service.Service only, so it works against the daemon of a
// running instance and against a fake in tests.
package tui

import (
	"context"
	"errors"
	"io"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/noris-network/mcs-backup/v2/internal/service"
)

// Options tune the TUI; the zero value works.
type Options struct {
	// Now returns the current time, time.Now if nil.
	Now func() time.Time
	// Open opens a URL in the browser of this machine; nil if that is not
	// possible (for example in a pod).
	Open func(url string) error
	// Hint tells how to reach a web UI link from another machine, for
	// example with a kubectl port-forward command; nil or empty for none.
	Hint func(link string) string
}

// Run shows the TUI until the user quits or ctx ends.
func Run(ctx context.Context, svc service.Service, in io.Reader, out io.Writer, o Options) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	m := New(service.WithSource(ctx, service.SourceTUI), svc, o)
	p := tea.NewProgram(m, tea.WithContext(ctx), tea.WithInput(in), tea.WithOutput(out))
	go pump(ctx, svc, p.Send)
	_, err := p.Run()
	if errors.Is(err, tea.ErrProgramKilled) && ctx.Err() != nil {
		return nil
	}
	return err
}

// pump forwards the live events of the service to the program and
// reconnects when the stream breaks.
func pump(ctx context.Context, svc service.Service, send func(tea.Msg)) {
	for ctx.Err() == nil {
		connected := false
		for ev, err := range svc.Events(ctx) {
			if err != nil {
				send(streamMsg{err: err})
				break
			}
			if !connected {
				connected = true
				send(streamMsg{})
			}
			send(eventMsg{ev})
		}
		select {
		case <-ctx.Done():
		case <-time.After(2 * time.Second):
		}
	}
}
