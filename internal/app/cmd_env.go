package app

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func init() {
	rootCmd.AddCommand(envCmd)
	f := envCmd.Flags()
	f.BoolP("export", "e", false, "print as export statements")
}

var envCmd = &cobra.Command{
	Use:   "env",
	Short: "dump restic environment",
	Run:   envFunc,
}

func envFunc(cmd *cobra.Command, args []string) {

	viper.BindPFlags(cmd.Flags())
	export := viper.GetBool("export")

	initializeMain(true)

	fmt.Print(printEnvLine(export, "AWS_ACCESS_KEY_ID", os.Getenv("AWS_ACCESS_KEY_ID")))
	fmt.Print(printEnvLine(export, "AWS_SECRET_ACCESS_KEY", os.Getenv("AWS_SECRET_ACCESS_KEY")))
	fmt.Print(printEnvLine(export, "RESTIC_PASSWORD", restic.Password))
	fmt.Print(printEnvLine(export, "RESTIC_REPOSITORY", restic.Repository))
}

func printEnvLine(export bool, key string, value any) string {
	format := "%v=%q\n"
	if export {
		// leading space prevents from saving in shell history (bash: HISTCONTROL)
		format = " export %v=%q\n"
	}
	return fmt.Sprintf(format, key, value)
}
