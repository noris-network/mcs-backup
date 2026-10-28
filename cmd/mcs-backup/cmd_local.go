package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"

	"github.com/spf13/cobra"

	"github.com/noris-network/mcs-backup/v2/internal/engine"
	"github.com/noris-network/mcs-backup/v2/internal/envexport"
)

func (a *app) newResticCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restic [restic arguments]",
		Short: "Run restic with the environment of this instance",
		Long: `Run restic against the repository of this instance, with the credentials and options
from the configuration. Everything after "restic" is passed on unchanged.`,
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := a.loadConfig()
			if err != nil {
				return err
			}
			c := exec.CommandContext(cmd.Context(), cfg.Restic.Bin, append(slices.Clone(cfg.Restic.Global), args...)...) //nolint:gosec // running restic is the purpose
			c.Env, c.Dir = cfg.ResticEnviron(), cfg.Root
			c.Stdin, c.Stdout, c.Stderr = a.stdin, a.stdout, a.stderr
			// let restic release its lock when the command is interrupted
			c.Cancel = func() error { return c.Process.Signal(os.Interrupt) }
			if err := c.Run(); err != nil {
				if ee, ok := errors.AsType[*exec.ExitError](err); ok {
					return &exitError{code: ee.ExitCode(), err: fmt.Errorf("restic exited with code %d", ee.ExitCode()), silent: true}
				}
				return err
			}
			return nil
		},
	}
}

func (a *app) newEnvCmd() *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "env",
		Short: "Print what is needed to run restic against this repository",
		Long: `Print the restic environment of this instance (repository, password, backend
credentials) so that restic can be run elsewhere, for example to restore on another
machine. The service does not have to run.

The secrets are printed in clear text: whoever can run this command can read them
anyway. Formats: export (default, for eval "$(mcs-backup env)"), dotenv (an .env
file) and docker (a docker run command with the restic image). Hints about
credentials that are not part of the environment, such as files, IAM roles or SSH
keys, are printed as comments.`,
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			style, err := envexport.ParseStyle(format)
			if err != nil {
				return usageError(err)
			}
			cfg, err := a.loadConfig()
			if err != nil {
				return err
			}
			info := engine.EnvInfo(cfg, true)
			if a.wantJSON(cmd) {
				return a.printJSON(info)
			}
			return envexport.Write(a.stdout, info, style)
		},
	}
	cmd.Flags().StringVar(&format, "format", string(envexport.StyleExport), "output format: export, dotenv or docker")
	return cmd
}

func (a *app) newRestoreIfEmptyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restore-if-empty",
		Short: "Restore the latest snapshot if BACKUP_ROOT is empty (disaster recovery)",
		Long: `Meant for an init container or for a unit that starts before the application: if
BACKUP_ROOT is empty, the latest regular snapshot is restored into it. The service
does not have to run.

A directory with data is left alone without even contacting the repository, so
the start of the application never depends on the storage being reachable. A
repository that does not exist, or has no snapshot yet, is a fresh installation
and not an error. The repository is never created here.

Exit codes: 0 restored or nothing to do, 1 the repository could not be read or
the restore failed (a Kubernetes init container is run again), 2 pipe mode is
configured, which this command does not support.`,
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := a.loadConfig()
			if err != nil {
				return err
			}
			eng := engine.NewFromConfig(cfg, nil)
			action, err := eng.RestoreIfEmpty(cmd.Context(), engine.Sink{Log: func(line string) { fmt.Fprintln(a.stdout, line) }})
			if errors.Is(err, engine.ErrPipeMode) {
				return usageError(errors.New("restore-if-empty does not support pipe mode: restoring the data of an application needs its own procedure (restore with PIPE_OUT_SCRIPT)"))
			}
			if err != nil {
				return err
			}
			fmt.Fprintln(a.stdout, restoreIfEmptyMessage(action, cfg.Root))
			return nil
		},
	}
}

func restoreIfEmptyMessage(action engine.RestoreIfEmptyAction, root string) string {
	switch action {
	case engine.ActionRestored:
		return "restored the latest snapshot into " + root
	case engine.ActionTargetNotEmpty:
		return root + " is not empty: nothing to restore"
	case engine.ActionNoRepository:
		return "the repository does not exist yet (fresh installation): nothing to restore"
	case engine.ActionNoSnapshot:
		return "the repository has no regular snapshot yet (fresh installation): nothing to restore"
	}
	return string(action)
}
