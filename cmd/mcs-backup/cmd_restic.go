package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(resticCmd)
}

var resticCmd = &cobra.Command{
	Use:                "restic",
	Short:              "run restic with mcs-backup environment",
	DisableFlagParsing: true,
	Run: func(cmd *cobra.Command, args []string) {
		resticFunc(args)
	},
}

func resticFunc(args []string) {
	initEnv(enableDebugOutput)

	args = append([]string{"restic"}, args...)
	if enableDebugOutput {
		log.Printf(">>> %#v", args)
	}

	fmt.Printf("%v\n", strings.Repeat(">", 80))
	restic := exec.Command(args[0], args[1:]...)
	restic.Stdout = os.Stdout
	restic.Stderr = os.Stderr
	err := restic.Run()
	fmt.Printf("%v\n", strings.Repeat("<", 80))
	if err != nil {
		os.Exit(1)
	}
}
