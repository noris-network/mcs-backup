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
	s, err := parse(expr)
	if err != nil {
		return nil, fmt.Errorf("invalid cron expression: %w", err)
	}
	return s, nil
}

// parse calls the parser of robfig/cron, which panics on some input, such as a
// time zone prefix without an expression after it ("TZ=Europe/Berlin"). The
// schedule file is read again while the service runs, so such a line must be
// an error, not a crash.
func parse(expr string) (s Schedule, err error) {
	defer func() {
		if r := recover(); r != nil {
			s, err = nil, errors.New("the expression cannot be parsed")
		}
	}()
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
	return parser.Parse(expr)
}
