package main

import (
	"errors"
	"strconv"
	"time"

	m "github.com/noris-network/mcs-backup/internal/metrics"
	r "github.com/noris-network/mcs-backup/internal/restic"
	s "github.com/noris-network/mcs-backup/internal/s3"
)

var backupLockingChan = make(chan struct{}, 1)

func fullBackupRun() error {
	t0 := time.Now()

	if !backupEnabled {
		loki.Warnf("backup is disabled.")
		return nil
	}

	// locking... only one backup process can run at a time
	select {
	case backupLockingChan <- struct{}{}:
		defer func() { <-backupLockingChan }()
	default:
		return errors.New("another restic operation is in progress")
	}

	// define some variables used in anonymous funcs
	var summary r.Summary
	var err error
	var forgetResponse r.ForgetResponseT
	var snapshots r.SnapshotsT
	var s3metrics s.Metrics
	var noProvider m.NoProvider
	var phase string

	for {
		// phase: prepare
		phase = "prepare"
		if runPhase(phase, metrics, []string{}, func() (any, error) {
			err := app.Hooks.PreBackup.Run()
			return noProvider, err
		}) != nil {
			break
		}

		// phase: backup
		phase = "backup"
		if runPhase(phase, metrics, []string{}, func() (any, error) {
			summary, err = restic.Backup(app.Pipes.In.Script)
			return summary, err
		}) != nil {
			break
		}
		if summary.SnapshotIsEmpty {
			loki.Warnf("  skipped, snapshot is empty")
			break
		} else {
			loki.Infof("  done, snapshot %#v saved", summary.SnapshotID)
		}

		// phase: forget -- apply retention policy
		phase = "forget"
		if runPhase(phase, metrics, []string{}, func() (any, error) {
			forgetResponse, err = restic.Forget()
			return noProvider, err
		}) != nil {
			break
		}
		loki.Infof("  done, %v snapshot(s) removed", len(forgetResponse[0].Remove))

		// phase: prune
		if housekeepingInterval > 0 {
			if time.Now().After(nextHousekeepingRun) {
				phase = "prune"
				if runPhase(phase, metrics, []string{}, func() (any, error) {
					_, err = restic.Prune()
					return noProvider, err
				}) != nil {
					break
				}
				loki.Infof("  done, repository is healthy")
				nextHousekeepingRun = time.Now().Add(housekeepingInterval)
			}
		}

		// phase: getstats
		phase = "getstats"
		if runPhase(phase, metrics, []string{}, func() (any, error) {
			snapshots, err = restic.Snapshots()
			if err != nil {
				return metadata{}, err
			}
			n := uint64(len(snapshots))
			return metadata{
				SnapshotsAvailable: n,
				SnapshotLatest:     uint64(snapshots[n-1].Time.Unix()),
				SnapshotsForgot:    uint64(len(forgetResponse[0].Remove)),
			}, err
		}) != nil {
			break
		}
		loki.Infof("  done")

		// phase: s3stats
		phase = "s3stats"
		if runPhase(phase, metrics, []string{s3.Bucket}, func() (any, error) {
			s3metrics, err = s3.GetMetrics()
			return s3metrics, err
		}) != nil {
			break
		}
		loki.Infof("  done")

		// phase: wrapup
		phase = "wrapup"
		if runPhase(phase, metrics, []string{}, func() (any, error) {
			err := app.Hooks.PostBackup.Run()
			return noProvider, err
		}) != nil {
			break
		}

		//lint:ignore SA4004 always break out of loop
		break
	}

	metrics.Apply(m.Pair{Datum: running{PhaseRunning: 0}, Publish: true})

	// build final report
	report := status{
		Duration: time.Since(t0).Truncate(time.Millisecond).Seconds(),
	}
	if err != nil {
		loki.Errorf("backup failed")
		flush()
		report.Error = phase + ": " + err.Error()
	} else {
		loki.Infof("backup finished")
		flush()
		report.Successful = phaseToCode["overall"]
	}
	metrics.Apply(
		m.Pair{
			Datum:       report,
			LabelValues: []string{"overall", strconv.FormatBool(report.Successful > 0)},
			Publish:     true,
		},
	)

	return err
}
