package config

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Retention is the snapshot retention policy, mapped to restic's --keep-*
// flags. Snapshots tagged "keep" are always retained in addition.
type Retention struct {
	Last    int `yaml:"last"`
	Hourly  int `yaml:"hourly"`
	Daily   int `yaml:"daily"`
	Weekly  int `yaml:"weekly"`
	Monthly int `yaml:"monthly"`
	Yearly  int `yaml:"yearly"`
}

// ParseRetention parses a policy given as JSON or YAML, e.g.
// {"weekly":8,"daily":7,"last":10}.
func ParseRetention(s string) (Retention, error) {
	var r Retention
	dec := yaml.NewDecoder(strings.NewReader(s))
	dec.KnownFields(true)
	if err := dec.Decode(&r); err != nil {
		if errors.Is(err, io.EOF) {
			return r, errors.New("retention policy is empty")
		}
		return r, fmt.Errorf("invalid retention policy: %w", err)
	}
	for name, n := range map[string]int{
		"last": r.Last, "hourly": r.Hourly, "daily": r.Daily,
		"weekly": r.Weekly, "monthly": r.Monthly, "yearly": r.Yearly,
	} {
		if n < 0 {
			return r, fmt.Errorf("invalid retention policy: %s must not be negative", name)
		}
	}
	if r.IsZero() {
		return r, errors.New("retention policy keeps nothing: set at least one of last, hourly, daily, weekly, monthly, yearly")
	}
	return r, nil
}

// IsZero reports whether the policy keeps nothing.
func (r Retention) IsZero() bool { return r == Retention{} }

// Args returns the policy as restic forget flags.
func (r Retention) Args() []string {
	var args []string
	for _, k := range []struct {
		flag string
		n    int
	}{
		{"--keep-last", r.Last},
		{"--keep-hourly", r.Hourly},
		{"--keep-daily", r.Daily},
		{"--keep-weekly", r.Weekly},
		{"--keep-monthly", r.Monthly},
		{"--keep-yearly", r.Yearly},
	} {
		if k.n > 0 {
			args = append(args, k.flag, strconv.Itoa(k.n))
		}
	}
	return args
}

// Describe explains the policy in plain words, e.g.
// "the last 10 snapshots, one per day for 7 days".
func (r Retention) Describe() string {
	var parts []string
	if r.Last == 1 {
		parts = append(parts, "the last snapshot")
	} else if r.Last > 1 {
		parts = append(parts, fmt.Sprintf("the last %d snapshots", r.Last))
	}
	for _, k := range []struct {
		n         int
		per, unit string
	}{
		{r.Hourly, "hour", "hour"},
		{r.Daily, "day", "day"},
		{r.Weekly, "week", "week"},
		{r.Monthly, "month", "month"},
		{r.Yearly, "year", "year"},
	} {
		if k.n <= 0 {
			continue
		}
		unit := k.unit
		if k.n > 1 {
			unit += "s"
		}
		parts = append(parts, fmt.Sprintf("one per %s for %d %s", k.per, k.n, unit))
	}
	return strings.Join(parts, ", ")
}
