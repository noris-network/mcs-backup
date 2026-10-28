package main

import (
	"bufio"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/noris-network/mcs-backup/v2/internal/config"
	"github.com/noris-network/mcs-backup/v2/internal/service"
)

// service connects to the running service. The returned function releases the connection.
func (a *app) service(cmd *cobra.Command) (service.Service, func()) {
	socket, _ := cmd.Flags().GetString("socket")
	if socket == "" {
		socket = a.getenv("MCS_BACKUP_SOCKET")
	}
	if socket == "" {
		socket = config.DefaultSocket()
	}
	svc := a.dial(socket)
	closer, ok := svc.(interface{ Close() })
	return svc, func() {
		if ok {
			closer.Close()
		}
	}
}

func (a *app) wantJSON(cmd *cobra.Command) bool {
	b, _ := cmd.Flags().GetBool("json")
	return b
}

// printJSON writes v as indented JSON to stdout.
func (a *app) printJSON(v any) error {
	data, err := json.Marshal(v, json.Deterministic(true), jsontext.Multiline(true))
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(a.stdout, string(data))
	return err
}

// confirm asks for the word that confirms a destructive action. With
// --confirm (assume is true) the answer is given, without a terminal the
// action is refused.
func (a *app) confirm(assume bool, word, what string) (string, error) {
	if assume {
		return word, nil
	}
	if !a.interactive {
		return "", usageError(fmt.Errorf("%s needs confirmation: run it in a terminal or pass --confirm", what))
	}
	fmt.Fprintf(a.stderr, "%s\nType %q to confirm: ", what, word)
	line, err := bufio.NewReader(a.stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", usageError(errors.New("not confirmed"))
	}
	if strings.TrimSpace(line) != word {
		return "", usageError(errors.New("not confirmed"))
	}
	return word, nil
}

// waitFor follows an operation until it ends and prints its result. The log
// of the operation goes to stderr, the result to stdout; with --json only the
// result is printed. The exit code reflects the result.
func (a *app) waitFor(ctx context.Context, cmd *cobra.Command, svc service.Service, op service.Operation, noWait bool) error {
	if noWait {
		if a.wantJSON(cmd) {
			return a.printJSON(op)
		}
		fmt.Fprintf(a.stdout, "%s started (operation %s). Follow it with: mcs-backup status --follow\n", op.Kind, op.ID)
		return nil
	}
	res, err := a.follow(ctx, svc, op, !a.wantJSON(cmd))
	if err != nil {
		return err
	}
	if a.wantJSON(cmd) {
		if err := a.printJSON(res); err != nil {
			return err
		}
	} else {
		a.renderResult(res)
	}
	if !res.Result.OK() {
		return &exitError{code: exitCodeError, err: fmt.Errorf("%s %s", res.Kind, res.Result), silent: true}
	}
	return nil
}

// follow waits until the operation op has ended and returns its result. It
// streams the log of the operation when show is set.
func (a *app) follow(ctx context.Context, svc service.Service, op service.Operation, show bool) (service.RunResult, error) {
	ectx, cancel := context.WithCancel(ctx)
	defer cancel()

	type item struct {
		ev  service.Event
		err error
	}
	items := make(chan item)
	go func() {
		defer close(items)
		for ev, err := range svc.Events(ectx) {
			select {
			case items <- item{ev, err}:
			case <-ectx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()

	tick := time.NewTicker(a.poll)
	defer tick.Stop()
	poll := make(chan struct{}, 1)
	poll <- struct{}{} // look at once: the operation may be over already
	sawLog := false
	events := items

	finish := func(r service.RunResult) (service.RunResult, error) {
		if show && !sawLog {
			// the operation ended before the stream started: show what it logged
			if l, err := svc.Log(ctx, op.Kind); err == nil {
				writeLines(a.stderr, l.Lines)
			}
		}
		return r, nil
	}
	for {
		select {
		case <-ctx.Done():
			return service.RunResult{}, fmt.Errorf("interrupted: the %s keeps running in the service (stop it with: mcs-backup cancel)", op.Kind)
		case <-tick.C:
			select {
			case poll <- struct{}{}:
			default:
			}
		case <-poll:
			st, err := svc.Status(ctx)
			if err != nil {
				return service.RunResult{}, err
			}
			if r, ok := st.LastRuns[op.Kind]; ok && r.ID == op.ID {
				return finish(r)
			}
		case it, ok := <-events:
			if !ok {
				events = nil // the stream ended: the polling carries on
				continue
			}
			if it.err != nil {
				if errors.Is(it.err, context.Canceled) {
					continue
				}
				events = nil // fall back to polling
				continue
			}
			ev := it.ev
			switch {
			case ev.Type == service.EventLog && show:
				sawLog = true
				fmt.Fprintln(a.stderr, ev.Line)
			case ev.Type == service.EventPhase && show && ev.Operation != nil && ev.Operation.ID == op.ID:
				fmt.Fprintf(a.stderr, "-- %s\n", ev.Operation.Phase)
			case ev.Type == service.EventFinished && ev.Result != nil && ev.Result.ID == op.ID:
				return finish(*ev.Result)
			}
		}
	}
}

// printJSONLine writes v as one line of JSON.
func (a *app) printJSONLine(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(a.stdout, string(data))
	return err
}
