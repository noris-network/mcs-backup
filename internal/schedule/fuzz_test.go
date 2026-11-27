package schedule

import (
	"testing"
	"time"
)

// FuzzParse checks that a cron expression from the configuration never
// panics the parser and that an accepted one yields a next time.
func FuzzParse(f *testing.F) {
	for _, s := range []string{
		"15 2 * * *", "*/5 * * * *", "0 3 * * 0", "@daily", "@every 1h", "TZ=Europe/Berlin 0 4 1 * *",
		"CRON_TZ=", "TZ=", "60 * * * *", "* * * * * *", "0-59/0 * * * *", "@", "1,2,3-5/2 0 L * *",
	} {
		f.Add(s)
	}
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		f.Fatal(err)
	}
	start := time.Date(2026, 3, 29, 1, 30, 0, 0, berlin) // just before a DST change
	f.Fuzz(func(t *testing.T, s string) {
		sched, err := Parse(s, berlin)
		if err != nil {
			return
		}
		if next := sched.Next(start); !next.IsZero() && !next.After(start) {
			t.Errorf("Parse(%q).Next(%v) = %v", s, start, next)
		}
	})
}
