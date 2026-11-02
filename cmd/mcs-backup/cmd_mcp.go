package main

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/noris-network/mcs-backup/v2/internal/mcpserver"
	"github.com/noris-network/mcs-backup/v2/internal/service"
)

func (a *app) newMCPCmd() *cobra.Command {
	var write, destructive bool
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Serve the Model Context Protocol on stdin/stdout for AI agents",
		Long: `Let an AI agent look at and operate the backups through the Model Context Protocol.
The server speaks on stdin and stdout and talks to the running service over its
socket. It starts nothing on its own: connect an agent to it, for example

  kubectl exec -i <pod> -c mcs-backup -- mcs-backup mcp
  kubectl exec -i <pod> -c mcs-backup -- mcs-backup mcp --allow-write

What the agent may do is decided here, not by the agent:

  (default)            look: status, snapshots, retention, logs, locks, restore checks
  --allow-write        also start backups and restore tests, cancel, keep snapshots,
                       remove stale locks, pause the schedule, restore into an empty directory
  --allow-destructive  also restore over live data, accept a much smaller backup, approve a
                       held forget and remove all locks (implies --allow-write)

Tools above the chosen level are not offered at all. Secrets are never returned. These
levels are guard rails for well-meaning agents, not a security boundary: whoever can
run this command can do everything the command line can.`,
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, closeSvc := a.serviceAs(cmd, service.SourceMCP)
			defer closeSvc()
			// stdout belongs to the protocol: the log goes to stderr
			log := newLogger(a.stderr, a.getenv)
			srv, err := mcpserver.New(cmd.Context(), svc, mcpserver.Options{Write: write || destructive, Destructive: destructive, Version: version(), Log: log})
			if err != nil {
				return err
			}
			return srv.Run(cmd.Context(), io.NopCloser(a.stdin), nopWriteCloser{a.stdout})
		},
	}
	cmd.Flags().BoolVar(&write, "allow-write", false, "let the agent change things: start backups, tags, schedule, restore into an empty directory")
	cmd.Flags().BoolVar(&destructive, "allow-destructive", false, "let the agent do things that can lose data (implies --allow-write)")
	return cmd
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }
