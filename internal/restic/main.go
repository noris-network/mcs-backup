package restic

import (
	"errors"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"
	"time"

	"golang.org/x/mod/semver"
	"gopkg.in/yaml.v2"
)

const minResticVersion = "v0.14.0"

// Restic wrapper
type Restic struct {
	Repository      string
	RepositoryID    string
	Password        string
	WorkDir         string
	DryRun          bool
	Debug           bool
	KeepPolicy      KeepPolicyT
	FlushFunc       func()
	BackupPaths     []string
	ExcludePaths    []string
	UseS3V1         bool
	AutoUnlockAfter time.Duration
}

// Opts struct
type Opts struct {
	DryRun bool
	Debug  bool
}

// NewFromEnv func
func NewFromEnv(opts Opts) *Restic {

	autoUnlock, err := time.ParseDuration(os.Getenv("AUTO_UNLOCK_AFTER"))
	if err != nil && os.Getenv("AUTO_UNLOCK_AFTER") != "" {
		log.Fatalf("NewFromEnv: AUTO_UNLOCK_AFTER: ParseDuration : %v", err)
	}

	backupPaths := strings.Split(os.Getenv("BACKUP_PATHS"), ":")
	if len(backupPaths) == 1 && backupPaths[0] == "" {
		backupPaths = []string{}
	}

	policy := KeepPolicyT{}
	if err := yaml.Unmarshal([]byte(os.Getenv("RETENTION_POLICY")), &policy); err != nil {
		log.Fatalf("NewFromEnv: Unmarshal: %s", err)
	}
	if len(policy.Strings()) == 0 {
		log.Fatalf("RETENTION_POLICY is empty")
	}

	log.Printf("retention policy: %q", policy)

	excludePaths := strings.Split(os.Getenv("EXCLUDE_PATHS"), ":")
	if len(excludePaths) == 1 && excludePaths[0] == "" {
		excludePaths = []string{}
	}

	workDir := os.Getenv("BACKUP_ROOT")
	if workDir == "" {
		workDir, _ = os.Getwd()
		// may be required by some pre-, post-, or pipe-scripts
		os.Setenv("BACKUP_ROOT", workDir)
	}

	return &Restic{
		AutoUnlockAfter: autoUnlock,
		BackupPaths:     backupPaths,
		Debug:           opts.Debug,
		DryRun:          opts.DryRun,
		ExcludePaths:    excludePaths,
		FlushFunc:       func() {},
		KeepPolicy:      policy,
		Password:        os.Getenv("RESTIC_PASSWORD"),
		Repository:      os.Getenv("RESTIC_REPOSITORY"),
		UseS3V1:         os.Getenv("S3_USE_V1") == "true",
		WorkDir:         workDir,
	}
}

// Preflight checks the restic version, if the repo could be opened, if it does
// not exist it will be created. The s3 bucket will automatically be created by
// restic, if necessary.
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

	// the fastest way to check if a repository already exists and is readable is
	// to just initialize it (again). this is safe, nothing bad will happen in
	// case it already exists
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
