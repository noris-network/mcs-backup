package main

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/spf13/cobra"

	"github.com/noris-network/mcs-backup/v2/internal/api"
	"github.com/noris-network/mcs-backup/v2/internal/config"
	"github.com/noris-network/mcs-backup/v2/internal/daemon"
	"github.com/noris-network/mcs-backup/v2/internal/engine"
	"github.com/noris-network/mcs-backup/v2/internal/notify"
)

// loadConfig reads the configuration from the environment. An invalid
// configuration is a usage error (exit code 2) that names every problem.
func (a *app) loadConfig() (*config.Config, error) {
	cfg, err := config.Load(a.environ())
	if err != nil {
		return nil, usageError(fmt.Errorf("invalid configuration:\n%w", err))
	}
	return cfg, nil
}

func (a *app) newServeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run the backup service",
		Long: `Run the backup service: it schedules and runs backups, serves the metrics and
probes on BACKUP_HTTP_PORT, and listens on a Unix socket for the other commands.`,
		Args: usageArgs(cobra.NoArgs),
		RunE: a.serve,
	}
}

func (a *app) serve(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()
	cfg, err := a.loadConfig()
	if err != nil {
		return err
	}
	log := newLogger(a.stdout, a.getenv)
	for _, w := range cfg.Warnings {
		log.WarnContext(ctx, w)
	}

	// Only one service per socket: this has to come before anything else starts.
	ln, err := api.Listen(ctx, cfg.Server.Socket)
	if err != nil {
		return err
	}

	var notifier daemon.Notifier
	if cfg.Notify.Command != "" {
		notifier = notify.New(notify.Config{
			Command: cfg.Notify.Command, On: cfg.Notify.On, Timeout: cfg.Notify.Timeout,
			Host: cfg.Host, Env: cfg.Environ(), Log: log,
		})
	}
	d := daemon.New(daemon.Options{
		Config: cfg, Engine: engine.NewFromConfig(cfg, log), Notifier: notifier, Logger: log,
		Version: version(), HTTPAddr: fmt.Sprintf(":%d", cfg.Server.HTTPPort),
	})

	log.InfoContext(ctx, "mcs-backup starting", "version", version(), "host", cfg.Host, "socket", cfg.Server.Socket,
		"http_port", cfg.Server.HTTPPort, "append_only", cfg.AppendOnly)

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		wg   sync.WaitGroup
		errs [2]error
	)
	wg.Go(func() { errs[0] = d.Run(runCtx); cancel() })
	wg.Go(func() { errs[1] = api.Serve(runCtx, ln, d, log); cancel() })
	wg.Wait()
	return errors.Join(errs[:]...)
}
