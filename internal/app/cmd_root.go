package app

import (
	"log"
	"os"

	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
)

// minimal required environment variables
var requiredEnv = []string{
	"AWS_ACCESS_KEY_ID",
	"AWS_SECRET_ACCESS_KEY",
	"RESTIC_PASSWORD",
	"RESTIC_REPOSITORY",
	"RETENTION_POLICY",
}

var rootCmd = &cobra.Command{
	Use: "backup",
	Run: func(cmd *cobra.Command, args []string) {
		cmd.Usage()
	},
}

func init() {

	// try to load .env, ignore error
	err := godotenv.Load()
	if err == nil {
		log.Printf("loaded environment from '.env'")
	}

	// debug
	rootCmd.PersistentFlags().Bool("debug", false, "enable debug mode")

	// backup root
	rootDefault := os.Getenv("BACKUP_ROOT")
	if rootDefault == "" {
		rootDefault, _ = os.Getwd()
	}

	// backup paths
	pathsDefault := os.Getenv("BACKUP_PATHS")

	// exclude paths
	excludePathsDefault := os.Getenv("EXCLUDE_PATHS")

	// add flags
	p := rootCmd.PersistentFlags()
	p.String("root", rootDefault, "backup root directory")
	p.String("paths", pathsDefault, "backup paths, ':'-separated")
	p.String("exclude-paths", excludePathsDefault, "exclude paths, ':'-separated")
}

// Execute func
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		log.Println(err)
		os.Exit(1)
	}
}
