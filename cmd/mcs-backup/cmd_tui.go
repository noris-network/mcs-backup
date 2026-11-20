package main

import (
	"context"
	"errors"
	"net/url"
	"os/exec"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/noris-network/mcs-backup/v2/internal/service"
	"github.com/noris-network/mcs-backup/v2/internal/tui"
)

func (a *app) newTUICmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Open the terminal user interface",
		Long: `Show the state of the service and operate it in the terminal: start backups, restore,
keep snapshots, handle locks and the schedule, and open the web UI. Everything
the command line can do with a running service is available here too. Press ? for
the keys.`,
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !a.interactive {
				return usageError(errors.New("the TUI needs a terminal; use the other commands in scripts (--json)"))
			}
			svc, closeSvc := a.serviceAs(cmd, service.SourceTUI)
			defer closeSvc()
			return tui.Run(cmd.Context(), svc, a.stdin, a.stdout, tui.Options{
				Now: a.now, Open: a.browser(cmd.Context()), Hint: a.webHint,
			})
		},
	}
}

// local reports whether the user sits at this machine: not in an SSH session
// and, on Linux, with a graphical session.
func (a *app) local() bool {
	if a.getenv("SSH_CONNECTION") != "" || a.getenv("KUBERNETES_SERVICE_HOST") != "" {
		return false
	}
	return runtime.GOOS == "darwin" || a.getenv("DISPLAY") != "" || a.getenv("WAYLAND_DISPLAY") != ""
}

// browser returns a function that opens a URL in the browser of this
// machine, nil if there is none to reach.
func (a *app) browser(ctx context.Context) func(string) error {
	if !a.local() {
		return nil
	}
	opener := "xdg-open"
	if runtime.GOOS == "darwin" {
		opener = "open"
	}
	if _, err := exec.LookPath(opener); err != nil {
		return nil
	}
	return func(link string) error { return exec.CommandContext(ctx, opener, link).Run() } //nolint:gosec // the opener is fixed, the link is made by the service
}

// webHint returns the port forward that the web UI of a pod needs before its
// link works on the machine of the user, empty outside Kubernetes.
func (a *app) webHint(link string) string {
	u, err := url.Parse(link)
	if err != nil || u.Port() == "" {
		return ""
	}
	p, ok := a.podRef()
	if !ok {
		return ""
	}
	return p.portForward(u.Port())
}
