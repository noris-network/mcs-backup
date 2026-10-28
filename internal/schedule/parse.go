// Package schedule parses cron expressions and decides when operations are due.
package schedule

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

// Schedule is a parsed cron expression.
type Schedule = cron.Schedule

// Parse parses a standard five field cron expression (or a descriptor such as
// @daily) and evaluates it in loc. An expression that carries its own
// CRON_TZ= or TZ= prefix keeps that time zone.
func Parse(expr string, loc *time.Location) (Schedule, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil, errors.New("empty cron expression")
	}
	if loc != nil && !strings.HasPrefix(expr, "TZ=") && !strings.HasPrefix(expr, "CRON_TZ=") {
		expr = "CRON_TZ=" + loc.String() + " " + expr
	}
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
	s, err := parser.Parse(expr)
	if err != nil {
		return nil, fmt.Errorf("invalid cron expression: %w", err)
	}
	return s, nil
}
