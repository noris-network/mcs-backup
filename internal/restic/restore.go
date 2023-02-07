package restic

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"os/exec"
)

// Restore restores a backup. When a pipeCommand is given, restic will pipe
// data to it, think "restic | pipeCommand".
func (r Restic) Restore(snapshot, newTarget, pipeCommand string) (string, error) {

	var resticCmd *exec.Cmd
	resticStdout := bytes.Buffer{}

	// required for pipe mode
	pipeMsg := make(chan string, 1)
	pipeError := make(chan error, 1)
	pipeClose := make(chan struct{}, 1)

	// write restic output to pipe?
	if pipeCommand != "" {
		if r.Debug {
			log.Printf(">>> pipe command: %#v", pipeCommand)
		}
		pipeStderr := bytes.Buffer{}
		pipeStdout := bytes.Buffer{}
		pipeReader, pipeWriter, _ := os.Pipe()
		pipeCmd := exec.Command(pipeCommand)
		pipeCmd.Stdin = pipeReader
		pipeCmd.Dir = r.WorkDir
		pipeCmd.Stderr = &pipeStderr
		pipeCmd.Stdout = &pipeStdout
		commandLine := []string{"restic"}
		if r.UseS3V1 {
			commandLine = append(commandLine, "-o", "s3.list-objects-v1=true")
		}
		commandLine = append(commandLine, "dump", snapshot, "/stdin")
		if r.Debug {
			log.Printf(">>> %#v", commandLine)
		}
		resticCmd = exec.Command(commandLine[0], commandLine[1:]...)
		resticCmd.Stdout = pipeWriter
		if err := pipeCmd.Start(); err != nil {
			return "", fmt.Errorf("outpipe: %w", err)
		}
		go func() {
			<-pipeClose
			pipeWriter.Close()
			pipeReader.Close()
			err := pipeCmd.Wait()
			if err != nil {
				log.Printf("OutPipe: %v", err)
				pipeMsg <- pipeStderr.String()
			}
			pipeError <- err
		}()
	} else {
		target := r.WorkDir
		if len(newTarget) > 0 {
			target = newTarget
		}
		commandLine := []string{"restic"}
		if r.UseS3V1 {
			commandLine = append(commandLine, "-o", "s3.list-objects-v1=true")
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
		resticCmd = exec.Command(commandLine[0], commandLine[1:]...)
		resticCmd.Stdout = &resticStdout
		pipeError <- nil
	}

	// for collecting command output
	stderr := bytes.Buffer{}
	resticCmd.Stderr = &stderr

	// execute command
	if err := resticCmd.Run(); err != nil {
		return "", fmt.Errorf("Restore: restic: %+v (%#v)", err, stderr.String())
	}
	pipeClose <- struct{}{}
	err := <-pipeError
	if err != nil {
		msg := <-pipeMsg
		return "", fmt.Errorf("Restore: OutPipe: %w (%#v)", err, msg)
	}

	return resticStdout.String(), nil
}
