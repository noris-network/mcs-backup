package main

import (
	"log"
	"os"

	"github.com/spf13/cobra"
)

// required environment variables
var requiredEnv = []string{
	"AWS_ACCESS_KEY_ID",
	"AWS_SECRET_ACCESS_KEY",
	"RESTIC_PASSWORD",
	"RESTIC_REPOSITORY",
	"RETENTION_POLICY",
}

var enableDebugOutput = os.Getenv("MCS_BACKUP_DEBUG") == "true"
var initEnvRan = false

var rootCmd = &cobra.Command{
	Use: "mcs-backup",
	Run: func(cmd *cobra.Command, args []string) {
		cmd.Usage()
	},
}

// Execute func
func main() {
	if err := rootCmd.Execute(); err != nil {
		log.Println(err)
		os.Exit(1)
	}
}
