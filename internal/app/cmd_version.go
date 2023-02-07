package app

import (
	"log"
	"runtime"

	"github.com/spf13/cobra"
)

// PrintAppInfo to log
func PrintAppInfo() {
	log.Printf("Version:       %v", Build)
	log.Printf("Go version:    %v", runtime.Version())
	log.Printf("Git commit:    %v", appGitCommit)
	log.Printf("Built:         %v", appBuildEpochString)
}

func init() {
	rootCmd.AddCommand(
		&cobra.Command{
			Use:   "version",
			Short: "print build and version info",
			Run: func(cmd *cobra.Command, args []string) {
				PrintAppInfo()
			},
		},
	)
}
