package present

import (
	"time"

	"github.com/noris-network/mcs-backup/v2/internal/service"
)

// Level says how good something is: LevelOK and LevelWarn and LevelBad are
// shown in green, yellow and red, LevelNone in the normal color.
type Level string

const (
	LevelNone Level = ""
	LevelOK   Level = "ok"
	LevelWarn Level = "warn"
	LevelBad  Level = "bad"
)

// Card is one headline figure of the overview.
type Card struct {
	Title, Value, Sub string
	Level             Level
	// Dot: the value is a state and gets a colored dot.
	Dot bool
}

// Cards returns the headline figures of the overview: the repository, the last
// and the next backup and, if there is one, the secondary copy.
func Cards(st service.Status, now time.Time) []Card {
	cards := []Card{repoCard("Repository", st.Repository), lastBackupCard(st, now), nextBackupCard(st.Schedule, now)}
	if st.Copy != nil {
		cards = append(cards, repoCard("Secondary copy", *st.Copy))
	}
	return cards
}

func repoCard(title string, r service.RepoStatus) Card {
	if !r.Up {
		return Card{Title: title, Value: "unreachable", Sub: r.Error, Level: LevelBad, Dot: true}
	}
	sub := "id " + Short(r.ID)
	if r.Snapshots >= 0 {
		sub = Count(r.Snapshots, "snapshot")
		if r.SizeBytes >= 0 {
			sub += " · " + Bytes(r.SizeBytes)
		}
	}
	return Card{Title: title, Value: "reachable", Sub: sub, Level: LevelOK, Dot: true}
}

func lastBackupCard(st service.Status, now time.Time) Card {
	last := st.Repository.LastSuccess
	if last.IsZero() {
		return Card{Title: "Last backup", Value: "never", Sub: "no backup yet", Level: LevelWarn}
	}
	return Card{Title: "Last backup", Value: Ago(now, last), Sub: Stamp(last)}
}

func nextBackupCard(sc service.ScheduleStatus, now time.Time) Card {
	next, scheduled := sc.Next[service.KindBackup]
	switch {
	case sc.MaintenanceUntil.After(now):
		return Card{Title: "Next backup", Value: "paused", Sub: "until " + Stamp(sc.MaintenanceUntil), Level: LevelWarn}
	case !sc.Enabled:
		return Card{Title: "Next backup", Value: "disabled", Sub: "scheduled backups are off", Level: LevelWarn}
	case !scheduled:
		return Card{Title: "Next backup", Value: "manual", Sub: "no schedule"}
	}
	return Card{Title: "Next backup", Value: "in " + Duration(next.Sub(now)), Sub: Stamp(next)}
}

// Health sums the state of the service up in a word and a level, for the
// indicator in the header.
func Health(st service.Status) (string, Level) {
	if !st.Repository.Up {
		return "repository down", LevelBad
	}
	worst := service.SeverityInfo
	for _, w := range st.Warnings {
		if w.Severity == service.SeverityError || w.Severity == service.SeverityWarning && worst != service.SeverityError {
			worst = w.Severity
		}
	}
	switch {
	case worst == service.SeverityError:
		return "needs attention", LevelBad
	case worst == service.SeverityWarning:
		return "warnings", LevelWarn
	case st.Operation != nil:
		return "working", LevelNone
	}
	return "healthy", LevelOK
}
