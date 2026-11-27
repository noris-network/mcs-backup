package schedule

import (
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skipf("tzdata not available: %v", err)
	}
	tests := []struct {
		name    string
		expr    string
		loc     *time.Location
		from    time.Time
		want    time.Time
		wantErr bool
	}{
		{
			name: "daily in UTC",
			expr: "30 2 * * *", loc: time.UTC,
			from: time.Date(2026, 1, 1, 3, 0, 0, 0, time.UTC),
			want: time.Date(2026, 1, 2, 2, 30, 0, 0, time.UTC),
		},
		{
			name: "evaluated in the given location",
			expr: "0 3 * * 0", loc: berlin,
			from: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			want: time.Date(2026, 1, 4, 3, 0, 0, 0, berlin),
		},
		{
			name: "own time zone wins",
			expr: "CRON_TZ=UTC 0 3 * * *", loc: berlin,
			from: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			want: time.Date(2026, 1, 1, 3, 0, 0, 0, time.UTC),
		},
		{
			name: "descriptor",
			expr: "@daily", loc: time.UTC,
			from: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC),
			want: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
		},
		{name: "empty", expr: "  ", loc: time.UTC, wantErr: true},
		{name: "too few fields", expr: "* * *", loc: time.UTC, wantErr: true},
		{name: "out of range", expr: "61 * * * *", loc: time.UTC, wantErr: true},
		// robfig/cron panics on these; a schedule file is read while the service runs
		{name: "time zone without expression", expr: "TZ=Europe/Berlin", loc: time.UTC, wantErr: true},
		{name: "empty time zone prefix", expr: "CRON_TZ=", loc: time.UTC, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := Parse(tt.expr, tt.loc)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Parse() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if got := s.Next(tt.from); !got.Equal(tt.want) {
				t.Errorf("Next(%v) = %v, want %v", tt.from, got, tt.want)
			}
		})
	}
}
