package main

import (
	"time"

	m "github.com/noris-network/mcs-backup/internal/metrics"
)

type phaseFunc func() (any, error)

var phaseToCode = map[string]uint64{
	"prepare":  1,
	"backup":   2,
	"forget":   3,
	"prune":    4,
	"getstats": 5,
	"s3stats":  6,
	"wrapup":   7,
	"overall":  8,
}

func runPhase(phase string, metrics *m.Metrics, labels []string, f phaseFunc) error {
	loki.Infof("- start phase %s", phase)
	flush()

	metrics.Apply(
		m.Pair{
			Datum:       status{InProgress: true},
			LabelValues: []string{phase, "true"},
		},
	)

	metrics.Apply(m.Pair{Datum: running{PhaseRunning: phaseToCode[phase]}, Publish: true})

	t0 := time.Now()
	response, err := f()
	elapsed := time.Since(t0).Truncate(time.Millisecond).Seconds()

	if err != nil {
		flush()
		metrics.Apply(
			m.Pair{
				Datum:       status{Duration: elapsed, Error: err.Error()},
				LabelValues: []string{phase, "false"},
				Publish:     true,
			},
		)
		return err
	}

	metrics.Apply(
		m.Pair{
			Datum:       status{Duration: elapsed, Successful: phaseToCode[phase]},
			LabelValues: []string{phase, "true"},
			Publish:     true,
		},
		m.Pair{
			Datum:       response,
			LabelValues: labels,
			Publish:     true,
		},
	)

	return nil
}
