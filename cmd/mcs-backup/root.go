package main

import (
	"github.com/spf13/cobra"
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
	cmd.AddCommand(a.newVersionCmd())
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
