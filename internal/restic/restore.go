package restic

import (
	"bytes"
	"log"

	cmdchain "github.com/rainu/go-command-chain"
)

// Restore restores a backup. When a pipeCommand is given, restic will pipe
// data to it, think "restic | pipeCommand".
func (r Restic) Restore(snapshot, newTarget, pipeCommand string) (string, error) {

	builder := cmdchain.Builder()

	commandLine := []string{"restic"}
	if r.UseS3V1 {
		commandLine = append(commandLine, "-o", "s3.list-objects-v1=true")
	}

	cmd1Out := &bytes.Buffer{}
	cmd1Err := &bytes.Buffer{}
	cmd2Err := &bytes.Buffer{}

	if pipeCommand != "" {
		commandLine = append(commandLine, "dump", snapshot, "/stdin")
		if r.Debug {
			log.Printf(">>> %#v", commandLine)
			log.Printf(">>> pipe command: %#v", pipeCommand)
		}
		builder.
			Join(commandLine[0], commandLine[1:]...).WithErrorForks(cmd1Err).
			Join(pipeCommand).WithErrorForks(cmd2Err).WithAdditionalOutputForks(cmd1Out)
	} else {
		target := r.WorkDir
		if len(newTarget) > 0 {
			target = newTarget
		}
		commandLine = append(commandLine, "restore", "--target", target, snapshot)
		for _, path := range r.BackupPaths {
			if path != "" {
				commandLine = append(commandLine, "--include", path)
			}
		}
		if r.Debug {
			log.Printf(">>> %#v", commandLine)
		}
		builder.Join(commandLine[0], commandLine[1:]...)
	}
	err := builder.Finalize().Run()
	if err != nil {
		if len(cmd1Err.String()) > 0 {
			log.Printf(">>> restic: %v", cmd1Err)
		}
		if len(cmd2Err.String()) > 0 {
			log.Printf(">>> pipe command: %v", cmd2Err)
		}
	}
	return cmd1Out.String(), err
}
