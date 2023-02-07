package app

import (
	"fmt"
	"log"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func init() {
	rootCmd.AddCommand(restoreCmd)
	f := restoreCmd.Flags()
	f.String("target", "", "directory to restore snapshot to (default: same as '--root')")
}

var restoreCmd = &cobra.Command{
	Use:   "restore [<id> [<extra>...]]",
	Short: "restore snapshot <id>, or <latest> without <id>",
	Long: "restore the given snapshot <id>, or <latest> when no <id> is given. to pass extra\n" +
		"arguments to the 'PostRestore' hook, e.g. a database name(s) to be restored, <id>\n" +
		"has to be provided, all subsequent arguments are passed to the hook.",
	Args: cobra.ArbitraryArgs,
	Run:  restoreFunc,
}

func restoreFunc(cmd *cobra.Command, args []string) {

	viper.BindPFlags(cmd.Flags())

	initializeMain(false)

	id := "latest"
	if len(args) == 1 {
		id = args[0]
	}

	if viper.GetBool("debug") {
		log.Printf("snapshot-id: %v", id)
	}

	if err := app.Hooks.PreRestore.Run(); err != nil {
		log.Fatalf("'PreRestore' hook failed: %v", err)
	}

	response, err := restic.Restore(id, viper.GetString("target"), app.Pipes.Out.Script)
	if err != nil {
		log.Fatalf("error: %v", err)
	}
	fmt.Println()
	fmt.Println(response)

	if err := app.Hooks.PostRestore.Run(); err != nil {
		log.Fatalf("'PostRestore' hook failed: %v", err)
	}
}
