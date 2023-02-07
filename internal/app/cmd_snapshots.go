package app

import (
	"fmt"
	"log"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func init() {
	rootCmd.AddCommand(snapshotsCmd)
}

var snapshotsCmd = &cobra.Command{
	Use:     "snapshots",
	Aliases: []string{"list"},
	Short:   "list available snapshots",
	Args:    cobra.NoArgs,
	Run:     snapshotsFunc,
}

func snapshotsFunc(cmd *cobra.Command, args []string) {

	viper.BindPFlags(cmd.Flags())

	initializeMain(false)

	snapshots, err := restic.Snapshots()
	if err != nil {
		log.Fatalf("error: %v", err)
	}
	fmt.Println()
	snapshots.Print()
}
