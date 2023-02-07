package main

import (
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func init() {
	rootCmd.AddCommand(forgetCmd)
	f := forgetCmd.Flags()
	f.BoolP("dry-run", "n", false, "dry-run")
	f.BoolP("debug", "D", enableDebugOutput, "debug")
}

var forgetCmd = &cobra.Command{
	Use:   "forget",
	Short: "run `restic forget` with mcs-backup environment and the configured policy",
	Run:   forgetFunc,
}

func forgetFunc(cmd *cobra.Command, args []string) {
	viper.BindPFlags(cmd.Flags())

	initEnv(enableDebugOutput)
	initializeRestic(true)

	rArgs := []string{"forget"}
	rArgs = append(rArgs, restic.KeepPolicy.Strings()...)
	rArgs = append(rArgs, args...)
	if restic.DryRun {
		rArgs = append(rArgs, "--dry-run")
	}
	resticFunc(rArgs)
}
