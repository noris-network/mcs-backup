package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var backupCmd = &cobra.Command{
	Use:   "backup",
	Short: "run backup, create new snapshot",
	Args:  cobra.NoArgs,
	Run:   backupFunc,
}

func init() {
	rootCmd.AddCommand(backupCmd)
	f := backupCmd.Flags()
	f.Bool("enable", false, "enable backups")
	f.Bool("disable", false, "disable backups")
	f.Bool("status", false, "show backup status")
	f.Duration("maintenance", 0, "disable for maintenance, re-enable after given duration")
}

func backupFunc(cmd *cobra.Command, args []string) {
	viper.BindPFlags(cmd.Flags())

	enable := viper.GetBool("enable")
	disable := viper.GetBool("disable")
	status := viper.GetBool("status")
	maintenance := viper.GetDuration("maintenance")

	initEnv(false)
	initServer()

	// configure service via http
	if enable || disable || status || maintenance > 0 {
		endpoint := "/api/mcs-backup/"
		switch {
		case enable:
			endpoint += "enable"
		case disable:
			endpoint += "disable"
		case status:
			endpoint += "status"
		case maintenance > 0:
			endpoint += fmt.Sprintf("maintenance/%v", maintenance)
		}
		res, err := ezRPC(endpoint, "")
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		fmt.Println(res)
		os.Exit(0)
	}

	body, err := ezRPC("/healthz", "")
	if err == nil && body == "ok" {
		fmt.Println("mcs-backup service found... will trigger backup via API...")
		err = ezStream("/api/mcs-backup", "")
		if err != nil {
			fmt.Printf("error: %v", err)
			os.Exit(1)
		}
		os.Exit(0)
	} else {
		fmt.Println("mcs-backup service not found.")
	}
	os.Exit(1)
}
