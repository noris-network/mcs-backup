package restic

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// SnapshotT holds all snapshot metadata
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

// SnapshotsT is just a slice of SnapshotT
type SnapshotsT []SnapshotT

// Snapshots returns all available snapshots
func (r Restic) Snapshots() (SnapshotsT, error) {
	snapshots := SnapshotsT{}
	if err := r.genericCommand([]string{"snapshots"}, &snapshots); err != nil {
		return snapshots, fmt.Errorf("Snapshots: %w", err)
	}
	return snapshots, nil
}

// Prune checks the repository and removes data that is not referenced anymore
func (r Restic) Prune() (string, error) {
	output := ""
	if err := r.genericCommand([]string{"prune"}, &output); err != nil {
		log.Printf("error: %v", output)
		return "", fmt.Errorf("Prune: %w", err)
	}
	return output, nil
}

// Check checks repository integrity
func (r Restic) Check() error {
	output := ""
	if err := r.genericCommand([]string{"check"}, &output); err != nil {
		return fmt.Errorf("Check: %+v", err)
	}
	return nil
}

// RepoConfig struct
type RepoConfig struct {
	Version           int    `json:"version"`
	ID                string `json:"id"`
	ChunkerPolynomial string `json:"chunker_polynomial"`
}

// RepoID return the repository ID
func (r Restic) getRepositoryID() (string, error) {
	output := RepoConfig{}
	if err := r.genericCommand([]string{"cat", "config"}, &output); err != nil {
		return "", fmt.Errorf("RepoID: %+v", err)
	}
	return output.ID, nil
}

// KeepPolicyT holds the retention policy
type KeepPolicyT struct {
	Last    int `yaml:"last"`
	Hourly  int `yaml:"hourly"`
	Daily   int `yaml:"daily"`
	Weekly  int `yaml:"weekly"`
	Monthly int `yaml:"monthly"`
	Yearly  int `yaml:"yearly"`
}

// ForgetResponseT is a subset of the restic response, `reasons` has been omited.
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

// UnlockAll removes all locks
func (r Restic) UnlockAll() error {
	msg := ""
	err := r.genericCommand([]string{"unlock", "--remove-all"}, &msg)
	return err
}

// genericCommand executes restic, suitable for most actions.
func (r Restic) genericCommand(command []string, result any) error {
	lockedRe := regexp.MustCompile(`repository is already locked.+lock was created at.+? \((.+) ago`)
	inRetry := false
	for {
		err := r.genericCommandInner(command, result)
		if err == nil || inRetry {
			return err
		}
		inRetry = true
		var match []string
		if match = lockedRe.FindStringSubmatch(err.Error()); match == nil {
			return err
		}
		log.Printf("Warn: repository is locked")
		if r.AutoUnlockAfter == 0 {
			log.Printf("Info: AUTO_UNLOCK_AFTER not set")
			return err
		}
		lockedDuration, pErr := time.ParseDuration(match[1])
		if pErr != nil {
			log.Printf("Error: time.ParseDuration: %v", pErr)
			return err
		}
		if lockedDuration >= r.AutoUnlockAfter {
			log.Printf("Info: unlocking repository")
			if uErr := r.UnlockAll(); uErr != nil {
				log.Printf("Error: unlockAll: %v", uErr)
				return err
			}
			log.Printf("Info: successfully removed locks")
			continue
		}
		log.Printf("Info: lock needs to be stale for at least %v to be automatically removed", r.AutoUnlockAfter)
		return err
	}
}

// genericCommandInner really executes restic, is wrapped by genericCommand to
// allow being re-run in case a locking error occurs
func (r Restic) genericCommandInner(command []string, result any) error {
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
	commandLine := []string{"restic", "--no-cache"}
	if r.UseS3V1 {
		commandLine = append(commandLine, "-o", "s3.list-objects-v1=true")
	}
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
		return fmt.Errorf("run: error: %+v / stdout: %#v", err, stderr.String())
	}

	if r.Debug {
		log.Println("~~~~~~~~~~~~~~~~~~~~~~~~~")
		log.Print(stdout.String())
		log.Println("~~~~~~~~~~~~~~~~~~~~~~~~~")
	}

	// return stdout in `result`
	// ... unmarshall to struct from json
	if outputAsJSON {
		if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
			log.Printf("<<< %v >>>\n", stdout.String())
			return fmt.Errorf("unmarshal: %w", err)
		}
	} else {
		// ... as plain text string
		*resultStr = stdout.String()
	}
	return nil
}
