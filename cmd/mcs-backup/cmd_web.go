package main

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/noris-network/mcs-backup/v2/internal/present"
	"github.com/noris-network/mcs-backup/v2/internal/service"
)

func (a *app) newWebCmd() *cobra.Command {
	var noOpen bool
	cmd := &cobra.Command{
		Use:   "web",
		Short: "Open the web UI (switches it on if needed)",
		Long: `Switch the web UI on if it is off, get a one-time login link from the service and
open it in the browser of this machine. In a pod, over SSH or without a graphical
session the link is printed instead, together with the command that makes the port
reachable from your machine.

The link works once and for two minutes; it logs the browser in with a session
cookie. Nothing permanent is put into a URL. The web UI stays on until it is
switched off ("web disable"), until its time limit ends ("web enable --for") or until
the service restarts (unless MCS_BACKUP_WEB_ADDR is set).`,
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, closeSvc := a.service(cmd)
			defer closeSvc()
			ctx := cmd.Context()
			st, err := svc.WebUI(ctx)
			if err != nil {
				return err
			}
			if !st.Enabled {
				if st, err = svc.SetWebUI(ctx, service.WebUIRequest{Enable: true}); err != nil {
					return err
				}
			}
			link, err := svc.LoginLink(ctx)
			if err != nil {
				return err
			}
			if a.wantJSON(cmd) {
				return a.printJSON(struct {
					URL    string              `json:"url"`
					Status service.WebUIStatus `json:"status"`
				}{link, st})
			}
			if open := a.browser(ctx); open != nil && !noOpen {
				if err := open(link); err == nil {
					fmt.Fprintf(a.stdout, "Opened the web UI at %s in your browser.\n", st.Addr)
					return nil
				}
			}
			fmt.Fprintf(a.stdout, "Open this link in a browser (it works once, for two minutes):\n\n  %s\n", link)
			if hint := a.webHint(link); hint != "" {
				fmt.Fprintf(a.stdout, "\nFrom your machine, first run:\n\n  %s\n", hint)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&noOpen, "no-open", false, "print the link instead of opening the browser")
	cmd.AddCommand(a.newWebEnableCmd(), a.newWebDisableCmd(), a.newWebStatusCmd())
	return cmd
}

func (a *app) newWebEnableCmd() *cobra.Command {
	var (
		addr string
		long time.Duration
	)
	cmd := &cobra.Command{
		Use:   "enable",
		Short: "Switch the web UI on",
		Long: `Switch the web UI on at --addr (MCS_BACKUP_WEB_ADDR, or 127.0.0.1:8484 if that is not set),
optionally for a limited time. The change holds until the service restarts. If the
web UI is on already, only the time limit changes.`,
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, closeSvc := a.service(cmd)
			defer closeSvc()
			st, err := svc.SetWebUI(cmd.Context(), service.WebUIRequest{Enable: true, Addr: addr, For: service.Duration(long)})
			if err != nil {
				return err
			}
			return a.printWebStatus(cmd, st)
		},
	}
	cmd.Flags().StringVar(&addr, "addr", "", "listen address, host:port")
	cmd.Flags().DurationVar(&long, "for", 0, "switch the web UI off again after this time")
	return cmd
}

func (a *app) newWebDisableCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "disable",
		Short: "Switch the web UI off and end all sessions",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, closeSvc := a.service(cmd)
			defer closeSvc()
			st, err := svc.SetWebUI(cmd.Context(), service.WebUIRequest{})
			if err != nil {
				return err
			}
			return a.printWebStatus(cmd, st)
		},
	}
}

func (a *app) newWebStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show whether the web UI is on",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, closeSvc := a.service(cmd)
			defer closeSvc()
			st, err := svc.WebUI(cmd.Context())
			if err != nil {
				return err
			}
			return a.printWebStatus(cmd, st)
		},
	}
}

func (a *app) printWebStatus(cmd *cobra.Command, st service.WebUIStatus) error {
	if a.wantJSON(cmd) {
		return a.printJSON(st)
	}
	switch {
	case !st.Enabled:
		fmt.Fprintln(a.stdout, "The web UI is off.")
	case st.Until.IsZero():
		fmt.Fprintf(a.stdout, "The web UI is on at %s.\n", st.Addr)
	default:
		fmt.Fprintf(a.stdout, "The web UI is on at %s until %s (in %s).\n", st.Addr, present.Stamp(st.Until), present.Duration(st.Until.Sub(a.now())))
	}
	return nil
}
