package main

import (
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(snapshotsCmd)
}

var snapshotsCmd = &cobra.Command{
	Use:                "snapshots",
	Short:              "run `restic snapshots` with mcs-backup environment",
	DisableFlagParsing: true,
	Run: func(cmd *cobra.Command, args []string) {
		resticFunc(append([]string{"snapshots"}, args...))
	},
}
