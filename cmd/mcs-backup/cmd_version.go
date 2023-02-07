package main

import (
	"log"
	"runtime"
	"runtime/debug"
	"time"

	"github.com/spf13/cobra"
)

var build = "dev-build"
var buildInfo map[string]any

func init() {
	buildInfo = map[string]any{
		"Version":    build,
		"Go version": runtime.Version(),
	}
	info, ok := debug.ReadBuildInfo()
	if ok {
		for _, kv := range info.Settings {
			switch kv.Key {
			case "vcs.revision":
				buildInfo["Git commit"] = kv.Value
			case "vcs.time":
				LastCommit, _ := time.Parse(time.RFC3339, kv.Value)
				buildInfo["Last Commit"] = LastCommit.String()
			}
		}
	}
}

func printInfo(i map[string]any) {
	for k, v := range i {
		log.Printf("%-20v %v", k+":", v)
	}
}

func init() {
	rootCmd.AddCommand(
		&cobra.Command{
			Use:   "version",
			Short: "print build and version info",
			Run: func(cmd *cobra.Command, args []string) {
				log.SetFlags(0)
				printInfo(buildInfo)
			},
		},
	)
}
