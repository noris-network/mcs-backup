package app

import (
	"fmt"
	"log"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func init() {
	rootCmd.AddCommand(forgetCmd)
	f := forgetCmd.Flags()
	f.Bool("dry-run", false, "do not expire any snapshots")
}

var forgetCmd = &cobra.Command{
	Use:   "forget",
	Short: "expire snapshots according to given policy",
	Run:   forgetFunc,
}

func forgetFunc(cmd *cobra.Command, args []string) {

	viper.BindPFlags(cmd.Flags())

	requiredEnv = append(requiredEnv, "RETENTION_POLICY")

	initializeMain(false)

	// forget...
	response, err := restic.Forget()
	if err != nil {
		log.Fatalf("error: %v", err)
	}
	if len(response) > 1 {
		dryrun := ""
		if restic.DryRun {
			dryrun = "--dry-run "
		}
		log.Println(`*** output of kept/removed snapshots not supported for "mixed" backups`)
		log.Fatalf(`*** to see full output, run "restic forget %s" instead`, dryrun+restic.KeepPolicy.String())
	}
	fmt.Println()
	fmt.Printf("keep %v snapshots:\n", len(response[0].Keep))
	response[0].Keep.Fprint(os.Stdout)
	fmt.Println()
	fmt.Printf("remove %v snapshots:\n", len(response[0].Remove))
	response[0].Remove.Fprint(os.Stdout)
}
