package main

import (
	"fmt"
	"log"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var envCmd = &cobra.Command{
	Use:   "env",
	Short: "dump restic environment",
	Run:   envFunc,
}

func init() {
	rootCmd.AddCommand(envCmd)
	f := envCmd.Flags()
	f.BoolP("export", "e", false, "print export statements")
	f.BoolP("space", "s", false, "print export statements with leading space")
	f.BoolP("dotenv", "d", false, "print in .env format")
}

func envFunc(cmd *cobra.Command, args []string) {
	viper.BindPFlags(cmd.Flags())
	export := viper.GetBool("export")
	space := viper.GetBool("space")
	dotenv := viper.GetBool("dotenv")

	initEnv(viper.GetBool("debug"))

	if dotenv {
		if space || export {
			log.Fatalf("--dotenv can not be combined with --space or --export")
		}
	}

	fmt.Print(printEnvLine(export, space, dotenv, "AWS_ACCESS_KEY_ID", os.Getenv("AWS_ACCESS_KEY_ID")))
	fmt.Print(printEnvLine(export, space, dotenv, "AWS_SECRET_ACCESS_KEY", os.Getenv("AWS_SECRET_ACCESS_KEY")))
	fmt.Print(printEnvLine(export, space, dotenv, "RESTIC_PASSWORD", os.Getenv("RESTIC_PASSWORD")))
	fmt.Print(printEnvLine(export, space, dotenv, "RESTIC_REPOSITORY", os.Getenv("RESTIC_REPOSITORY")))
}

func printEnvLine(export, space, dotenv bool, key string, value any) string {
	format := "%v="
	if dotenv {
		format += "%s\n"
	} else {
		format += "%q\n"
	}
	if export {
		format = "export " + format
	}
	if space {
		format = " " + format
	}
	return fmt.Sprintf(format, key, value)
}
