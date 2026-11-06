package main

import (
	"github.com/spf13/cobra"

	"github.com/noris-network/mcs-backup/v2/internal/config"
)

func (a *app) newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "mcs-backup",
		Short:         "Backup service around restic",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.SetOut(a.stdout)
	cmd.SetErr(a.stderr)
	cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return usageError(err)
	})
	pf := cmd.PersistentFlags()
	pf.String("socket", a.getenv("MCS_BACKUP_SOCKET"), "socket of the running service (default "+config.DefaultSocket()+")")
	pf.Bool("json", false, "print machine readable JSON")

	info := a.newConfigCheckCmd("info", true)
	info.Short = "Check the configuration and the access to the repositories"

	cmd.AddCommand(
		a.newVersionCmd(),
		a.newServeCmd(),
		a.newConfigCmd(),
		info,
		a.newResticCmd(),
		a.newEnvCmd(),
		a.newTUICmd(),
		a.newWebCmd(),
		a.newMCPCmd(),
		a.newRestoreIfEmptyCmd(),
		a.newStatusCmd(),
		a.newSnapshotsCmd(),
		a.newLsCmd(),
		a.newFindCmd(),
		a.newBackupCmd(),
		a.newRestoreCmd(),
		a.newCancelCmd(),
		a.newVerifyCmd(),
		a.newKeepCmd("keep"),
		a.newKeepCmd("unkeep"),
		a.newAcknowledgeCmd(),
		a.newApproveForgetCmd(),
		a.newForgetCmd(),
		a.newLocksCmd(),
		a.newUnlockCmd(),
		a.newLogCmd(),
		a.newNotifyCmd(),
	)
	return cmd
}

// usageArgs turns positional argument errors into usage errors (exit code 2).
func usageArgs(validate cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := validate(cmd, args); err != nil {
			return usageError(err)
		}
		return nil
	}
}
