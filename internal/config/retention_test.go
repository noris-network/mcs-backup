package config

import (
	"reflect"
	"testing"
)

func TestParseRetention(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		want     Retention
		wantArgs []string
		wantText string
		wantErr  bool
	}{
		{
			name: "json", in: `{"weekly":8,"daily":7,"last":10}`,
			want:     Retention{Last: 10, Daily: 7, Weekly: 8},
			wantArgs: []string{"--keep-last", "10", "--keep-daily", "7", "--keep-weekly", "8"},
			wantText: "the last 10 snapshots, one per day for 7 days, one per week for 8 weeks",
		},
		{
			name: "yaml", in: "yearly: 1\nmonthly: 1\nlast: 1\nhourly: 1",
			want:     Retention{Last: 1, Hourly: 1, Monthly: 1, Yearly: 1},
			wantArgs: []string{"--keep-last", "1", "--keep-hourly", "1", "--keep-monthly", "1", "--keep-yearly", "1"},
			wantText: "the last snapshot, one per hour for 1 hour, one per month for 1 month, one per year for 1 year",
		},
		{name: "empty", in: "", wantErr: true},
		{name: "all zero", in: `{"last":0}`, wantErr: true},
		{name: "negative", in: `{"last":-1,"daily":3}`, wantErr: true},
		{name: "unknown key", in: `{"dayly":3}`, wantErr: true},
		{name: "not a mapping", in: `7`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseRetention(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseRetention() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if got != tt.want {
				t.Errorf("policy = %+v, want %+v", got, tt.want)
			}
			if args := got.Args(); !reflect.DeepEqual(args, tt.wantArgs) {
				t.Errorf("Args() = %q, want %q", args, tt.wantArgs)
			}
			if text := got.Describe(); text != tt.wantText {
				t.Errorf("Describe() = %q, want %q", text, tt.wantText)
			}
		})
	}
}
