package restic

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

const minResticVersion = "v0.13.1"

// Restic wrapper
type Restic struct {
	Repository   string
	RepositoryID string
	Password     string
	WorkDir      string
	DryRun       bool
	Debug        bool
	KeepPolicy   KeepPolicyT
	FlushFunc    func()
	BackupPaths  []string
	ExcludePaths []string
}

/////////////////////////////////////////////////////////////////////////////
// Snapshots

// The SnapshotT type holds all snapshot metadata
type SnapshotT struct {
	Time     time.Time `json:"time"`
	Parent   string    `json:"parent"`
	Tree     string    `json:"tree"`
	Paths    []string  `json:"paths"`
	Hostname string    `json:"hostname"`
	Username string    `json:"username"`
	UID      uint      `json:"uid"`
	GID      uint      `json:"gid"`
	Excludes []string  `json:"excludes"`
	ID       string    `json:"id"`
	ShortID  string    `json:"short_id"`
}

// The SnapshotsT type is a slice of SnapshotT
type SnapshotsT []SnapshotT

// Fprint prints tabular snapshot info like restic
func (s SnapshotsT) Fprint(out io.Writer) {
	fmt.Fprintf(out, "%-8s  %-19s  %s\n", "ID", "Time", "Paths")
	fmt.Fprintln(out, strings.Repeat("-", 95))
	for _, snapshot := range s {
		for n, path := range snapshot.Paths {
			if n == 0 {
				fmt.Fprintf(out,
					"%s  %s  %s\n",
					snapshot.ShortID, snapshot.Time.Format("2006-01-02 15:04:05"), path,
				)
			} else {
				fmt.Fprintf(out, "%31s%s\n", "", path)
			}
		}
	}
	fmt.Fprintln(out, strings.Repeat("-", 95))
	fmt.Fprintf(out, "%d snapshots\n", len(s))
}

// Print prints tabular snapshot info like restic to stdout
func (s SnapshotsT) Print() {
	s.Fprint(os.Stdout)
}

// Snapshots returns all available snapshots
func (r Restic) Snapshots() (SnapshotsT, error) {
	snapshots := SnapshotsT{}
	if err := r.genericCommand([]string{"snapshots"}, &snapshots); err != nil {
		return snapshots, fmt.Errorf("Snapshots: %w", err)
	}
	return snapshots, nil
}

// Opts struct
type Opts struct {
	DryRun       bool
	Debug        bool
	WorkDir      string
	BackupPaths  []string
	ExcludePaths []string
}

// NewFromEnv func
func NewFromEnv(opts Opts) *Restic {

	policy := KeepPolicyT{}
	if err := json.Unmarshal([]byte(os.Getenv("RETENTION_POLICY")), &policy); err != nil {
		log.Printf("RETENTION_POLICY is expected to be valid JSON")
		log.Fatalf("NewFromEnv: Unmarshal: %s", err)
	}

	return &Restic{
		Repository:   os.Getenv("RESTIC_REPOSITORY"),
		Password:     os.Getenv("RESTIC_PASSWORD"),
		KeepPolicy:   policy,
		WorkDir:      opts.WorkDir,
		Debug:        opts.Debug,
		DryRun:       opts.DryRun,
		BackupPaths:  opts.BackupPaths,
		ExcludePaths: opts.ExcludePaths,
		FlushFunc:    func() {},
	}
}

/////////////////////////////////////////////////////////////////////////////
// Backup

// report interval
const reportEvery = 5

// Metrics holds all backup metrics
// 	type Metrics struct {
// 	// "meta" metrics
// 	AvailableSnapshots uint64
// 	LastSnapshot       uint64

// 	Error      string
// 	InProgress bool
// 	//Success            bool

// 	// restic "summary" metrics
// 	//Summary
// }

// About struct
type About struct {
	AvailableSnapshots uint64
	LastSnapshot       uint64
}

// "meta" metrics

// restic "summary" metrics
//Summary
//}

// Summary struct
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

// Fprint prints backup stats like restic
func (s Summary) Fprint(out io.Writer) {

	fmt.Fprintf(out,
		"Files: %11d new, %5d changed, %5d unmodified\n",
		s.FilesNew, s.FilesChanged, s.FilesUnmodified)
	fmt.Fprintf(out,
		"Dirs: %12d new, %5d changed, %5d unmodified\n",
		s.DirsNew, s.DirsChanged, s.DirsUnmodified)
	fmt.Fprintf(out,
		"Added to the repo: %s\n",
		hfSize(s.DataAdded))
	fmt.Fprintln(out)
	fmt.Fprintf(out,
		"processed %d files, %s in %s\n",
		s.FilesProcessed, hfSize(s.DataProcessed),
		time.Duration(s.Elapsed)*time.Second)
	fmt.Fprintf(out,
		"snapshot %s saved\n",
		s.SnapshotID)
}

// Print prints backup stats like restic to stdout
func (s Summary) Print() {
	s.Fprint(os.Stdout)
}

// messageT knows of all fields that a running backup spews out
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

// RepoConfig struct
type RepoConfig struct {
	Version           int    `json:"version"`
	ID                string `json:"id"`
	ChunkerPolynomial string `json:"chunker_polynomial"`
}

// Backup func
func (r Restic) Backup(pipeCommand string) (Summary, error) {

	summary := Summary{}
	snapshotID := ""

	backupPaths := []string{"./"}
	if len(r.BackupPaths) > 0 && r.BackupPaths[0] != "" {
		backupPaths = r.BackupPaths
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
	cmdStrings := []string{"backup", "--json", "--host=dummy", "-o", "s3.list-objects-v1=true"}
	cmdStrings = append(cmdStrings, excludePaths...)
	cmdStrings = append(cmdStrings, backupPaths...)
	cmd := exec.Command("restic", cmdStrings...)
	cmd.Dir = r.WorkDir
	stderr := bytes.Buffer{}
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return summary, fmt.Errorf("Backup: StdoutPipe: %w", err)
	}

	// read input from pipe?
	if pipeCommand != "" {
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
		if time.Since(lastReport) > reportEvery*time.Second {
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

/////////////////////////////////////////////////////////////////////////////
// Restore

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
		pipeStderr := bytes.Buffer{}
		pipeStdout := bytes.Buffer{}
		pipeReader, pipeWriter, _ := os.Pipe()
		pipeCmd := exec.Command(pipeCommand)
		pipeCmd.Stdin = pipeReader
		pipeCmd.Dir = r.WorkDir
		pipeCmd.Stderr = &pipeStderr
		pipeCmd.Stdout = &pipeStdout

		resticCmd = exec.Command("restic", "-o", "s3.list-objects-v1=true", "dump", snapshot, "/stdin")
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
		cmdStrings := []string{"-o", "s3.list-objects-v1=true", "restore", snapshot, "--target", target}
		for _, path := range r.BackupPaths {
			if path != "" {
				cmdStrings = append(cmdStrings, "--include", path)
			}
		}
		resticCmd = exec.Command("restic", cmdStrings...)
		resticCmd.Stdout = &resticStdout
		pipeError <- nil
	}

	// collecting command output
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

/////////////////////////////////////////////////////////////////////////////
// Prune

// Prune checks the repository and removes data that is not referenced anymore
func (r Restic) Prune() (string, error) {
	output := ""
	if err := r.genericCommand([]string{"prune"}, &output); err != nil {
		log.Printf("error: %v", output)
		return "", fmt.Errorf("Prune: %w", err)
	}
	return output, nil
}

/////////////////////////////////////////////////////////////////////////////
// preflight check

// Preflight checks if the repo could be opened, if it does not exist it will be
// created. The s3 bucket will automatically be created by restic, if necessary.
func (r *Restic) Preflight() error {

	// check restic version
	output := ""
	if err := r.genericCommand([]string{"version"}, &output); err != nil {
		return fmt.Errorf("Preflight: version: %w", err)
	}
	semverRE := regexp.MustCompile(`restic (\d+\.\d+\.\d+) compiled`)
	match := semverRE.FindStringSubmatch(output)
	if match == nil {
		return errors.New("Preflight: semverRE did not match")
	}
	resticVersion := "v" + match[1]
	if semver.Compare(resticVersion, minResticVersion) == -1 {
		return fmt.Errorf("Preflight: please upgrade restic to at least %v", minResticVersion)
	}

	// the fastest way to check if a repository already exists and is readable
	// is to just try to initialize it again
	if err := r.genericCommand([]string{"init"}, &output); err != nil {

		// repo successfully opened (already exists)?
		if !strings.Contains(err.Error(), "repository master key and config already initialized") {
			return fmt.Errorf("Preflight: init: %w", err)
		}

	} else {
		log.Printf("repository not found, created new")
	}

	// get repo ID
	repositoryID, err := r.getRepositoryID()
	if err != nil {
		return fmt.Errorf("Preflight: getRepositoryID: %w", err)
	}
	r.RepositoryID = repositoryID
	log.Printf("repositoryID: %v", repositoryID)

	return nil
}

/////////////////////////////////////////////////////////////////////////////
// Check

// Check checks repository integrity
func (r Restic) Check() error {
	output := ""
	if err := r.genericCommand([]string{"check"}, &output); err != nil {
		return fmt.Errorf("Check: %+v", err)
	}
	return nil
}

/////////////////////////////////////////////////////////////////////////////
// RepoID

// RepoID return the repository ID
func (r Restic) getRepositoryID() (string, error) {
	output := RepoConfig{}
	if err := r.genericCommand([]string{"cat", "config"}, &output); err != nil {
		return "", fmt.Errorf("RepoID: %+v", err)
	}
	return output.ID, nil
}

/////////////////////////////////////////////////////////////////////////////
// Stats

// StatsT type
type StatsT struct {
	TotalSize      int `json:"total_size"`
	TotalFileCount int `json:"total_file_count"`
}

// Stats returns repository stats
func (r Restic) Stats() (StatsT, error) {
	stats := StatsT{}
	err := r.genericCommand([]string{"stats"}, stats)
	return stats, err
}

/////////////////////////////////////////////////////////////////////////////
// forget

// KeepPolicyT type
type KeepPolicyT struct {
	Last    int `json:"last"`
	Hourly  int `json:"hourly"`
	Daily   int `json:"daily"`
	Weekly  int `json:"weekly"`
	Monthly int `json:"monthly"`
	Yearly  int `json:"yearly"`
}

// ForgetResponseT type is a subset of the restic response, `reasons` has been omited.
type ForgetResponseT []struct {
	Keep   SnapshotsT `json:"keep"`
	Remove SnapshotsT `json:"remove"`
}

// Strings returns the policy as command line compatible strings
func (p KeepPolicyT) Strings() []string {
	s := []string{}
	allNull := true
	if p.Last > 0 {
		s = append(s, "--keep-last", strconv.Itoa(p.Last))
		allNull = false
	}
	if p.Hourly > 0 {
		s = append(s, "--keep-hourly", strconv.Itoa(p.Hourly))
		allNull = false
	}
	if p.Daily > 0 {
		s = append(s, "--keep-daily", strconv.Itoa(p.Daily))
		allNull = false
	}
	if p.Weekly > 0 {
		s = append(s, "--keep-weekly", strconv.Itoa(p.Weekly))
		allNull = false
	}
	if p.Monthly > 0 {
		s = append(s, "--keep-monthly", strconv.Itoa(p.Monthly))
		allNull = false
	}
	if p.Yearly > 0 {
		s = append(s, "--keep-yearly", strconv.Itoa(p.Yearly))
		allNull = false
	}
	if allNull {
		log.Printf("warning: KeepPolicy is empty")
	}
	return s
}

// Strings returns the policy as command line compatible string
func (p KeepPolicyT) String() string {
	return strings.Join(p.Strings(), " ")
}

// Forget deletes expired, and returns info on deleted and remaining snapshots.
func (r Restic) Forget(snapshotIDs ...string) (ForgetResponseT, error) {
	response := ForgetResponseT{}
	command := []string{"forget"}
	if len(snapshotIDs) > 0 {
		command = append(command, snapshotIDs...)
	} else {
		command = append(command, r.KeepPolicy.Strings()...)
	}
	err := r.genericCommand(command, &response)
	return response, err
}

/////////////////////////////////////////////////////////////////////////////
// generic

// genericCommand executes restic suitable for most actions.
func (r Restic) genericCommand(command []string, result interface{}) error {

	// detect preferred output mode
	outputAsJSON := true
	resultStr, ok := result.(*string)
	if ok {
		outputAsJSON = false
	}

	// collecting command output
	stdout := bytes.Buffer{}
	stderr := bytes.Buffer{}

	// build command line
	commandLine := []string{"restic", "--no-cache", "-o", "s3.list-objects-v1=true"}
	if outputAsJSON {
		commandLine = append(commandLine, "--json")
	}
	commandLine = append(commandLine, command...)
	if r.DryRun && command[0] != "init" && command[0] != "version" && command[0] != "restore" {
		commandLine = append(commandLine, "--dry-run")
	}

	// new command
	if r.Debug {
		log.Printf(">>> %#v", commandLine)
	}
	cmd := exec.Command(commandLine[0], commandLine[1:]...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// execute command
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("Run: error: %+v / stdout: %#v", err, stderr.String())
	}

	// return stdout in `result`
	// ... unmarshall to struct from json
	if outputAsJSON {
		if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
			log.Printf("<<< %v >>>\n", stdout.String())
			return fmt.Errorf("Unmarshal: %w", err)
		}
	} else {
		// ... as plain text string
		*resultStr = stdout.String()
	}
	return nil
}

// hfSize -- print data sizes in a 'human friendly' way
func hfSize(b uint64) string {
	const unit = 1000
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "kMGTPE"[exp])
}

// hfDuration -- print durations in a 'human friendly' way. this simple
// implementation just works for durations up to 23h59m59s, but this
// should be sufficiant for backup durations 🤞
func hfDuration(s int) string {
	return time.Time{}.Add(time.Duration(s) * time.Second).Format("15:04:05")
}
