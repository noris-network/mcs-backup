package restic

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"
)

// report interval
const reportEvery = 5 * time.Second

// Summary of backup metrics returned by restic
type Summary struct {
	DataProcessed   uint64  `json:"total_bytes_processed"`
	DataAdded       uint64  `json:"data_added"`
	DataBlobs       uint64  `json:"data_blobs"`
	DirsChanged     uint64  `json:"dirs_changed"`
	DirsNew         uint64  `json:"dirs_new"`
	DirsUnmodified  uint64  `json:"dirs_unmodified"`
	Elapsed         float64 `json:"total_duration"`
	FilesChanged    uint64  `json:"files_changed"`
	FilesNew        uint64  `json:"files_new"`
	FilesProcessed  uint64  `json:"total_files_processed"`
	FilesUnmodified uint64  `json:"files_unmodified"`
	SnapshotID      string  `json:"snapshot_id"`
	TreeBlobs       uint64  `json:"tree_blobs"`
	SnapshotIsEmpty bool
}

// messageT all fields that a running backup spews out
type messageT struct {
	MessageType    string  `json:"message_type"`
	PercentDone    float64 `json:"percent_done"`
	FilesDone      int     `json:"files_done"`
	TotalFiles     uint64  `json:"total_files"`
	TotalBytes     uint64  `json:"total_bytes"`
	SecondsElapsed int     `json:"seconds_elapsed"`
	SnapshotID     string  `json:"snapshot_id"`
	Summary
}

// Backup runs a backup, when pipeCommand is not empty it will be run and it's
// stdout will be piped into restic, think "pipeCommand | restic".
func (r Restic) Backup(pipeCommand string) (Summary, error) {

	summary := Summary{}
	snapshotID := ""

	backupPaths := r.BackupPaths
	if len(backupPaths) == 0 {
		backupPaths = []string{"./"}
	}

	excludePaths := []string{}
	for _, path := range r.ExcludePaths {
		excludePaths = append(excludePaths, "--exclude", path)
	}

	// required for pipe mode
	pipeError := make(chan error, 1)
	pipeMsg := make(chan string, 1)
	if pipeCommand != "" {
		backupPaths = []string{"--stdin"}
	}

	// build and configure command
	commandLine := []string{"restic", "backup", "--json", "--host=dummy"}
	if r.UseS3V1 {
		commandLine = append(commandLine, "-o", "s3.list-objects-v1=true")
	}
	commandLine = append(commandLine, excludePaths...)
	commandLine = append(commandLine, backupPaths...)
	if r.Debug {
		log.Printf(">>> %#v", commandLine)
	}
	cmd := exec.Command(commandLine[0], commandLine[1:]...)
	cmd.Dir = r.WorkDir
	stderr := bytes.Buffer{}
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return summary, fmt.Errorf("Backup: StdoutPipe: %w", err)
	}

	// read input from pipe?
	if pipeCommand != "" {
		if r.Debug {
			log.Printf(">>> pipe command: %#v", pipeCommand)
		}
		pipeStderr := bytes.Buffer{}
		pipeReader, pipeWriter, _ := os.Pipe()
		pipeCmd := exec.Command(pipeCommand)
		pipeCmd.Stdout = pipeWriter
		pipeCmd.Dir = r.WorkDir
		pipeCmd.Stderr = &pipeStderr
		cmd.Stdin = pipeReader
		if err := pipeCmd.Start(); err != nil {
			return summary, fmt.Errorf("Backup: Start InPipe: %w", err)
		}
		go func() {
			err := pipeCmd.Wait()
			if err != nil {
				log.Printf("InPipe: %v", err)
				pipeMsg <- pipeStderr.String()
			}
			pipeWriter.Close()
			pipeError <- err
		}()
	} else {
		pipeError <- nil
	}

	// start command
	if err := cmd.Start(); err != nil {
		return summary, fmt.Errorf("Backup: Start: %w", err)
	}

	// re-usable report printer
	printReport := func(message messageT) {
		log.Printf(
			"  [%s] Files done: %6d  Files total: %6d",
			hfDuration(message.SecondsElapsed), message.FilesDone, message.TotalFiles)
		r.FlushFunc()
	}

	// read stdout as lines
	scanner := bufio.NewScanner(stdout)
	lastReport := time.Time{}
	lastMessage := messageT{}
	for scanner.Scan() {
		message := messageT{}
		data := scanner.Bytes()
		err = json.Unmarshal(data, &message)
		if err != nil {
			log.Printf("<<< %s >>>", data)
			log.Printf("<<< %v >>>", data)
			return summary, fmt.Errorf("Backup: Unmarshal: %w", err)
		}
		if message.MessageType == "summary" {
			summary = message.Summary
			snapshotID = message.SnapshotID
			summary.SnapshotID = snapshotID
			continue
		}
		lastMessage = message
		if time.Since(lastReport) > reportEvery {
			lastReport = time.Now()
			printReport(message)
		}
	}
	printReport(lastMessage)

	// wait for the command to exit
	err = cmd.Wait()
	if err != nil {
		if snapshotID != "" {
			r.Forget(snapshotID)
		}
		msg := stderr.String()
		// ignore failed backup when no files are present
		if !strings.Contains(msg, "snapshot is empty") {
			return summary, fmt.Errorf("Backup: %w (%v)", err, msg)
		}
		summary.SnapshotIsEmpty = true
	}
	err = <-pipeError
	if err != nil {
		if snapshotID != "" {
			r.Forget(snapshotID)
		}
		msg := <-pipeMsg
		return summary, fmt.Errorf("Backup: InPipe: %w (%#v)", err, msg)
	}
	return summary, nil
}

// hfDuration -- print durations in a 'human friendly' way like restic. this
// simple implementation just works for durations up to 23h59m59s, but this
// should be sufficiant for backup durations 🤞
func hfDuration(s int) string {
	return time.Time{}.Add(time.Duration(s) * time.Second).Format("15:04:05")
}
