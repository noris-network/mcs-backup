package config

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/noris-network/mcs-backup/v2/internal/schedule"
)

func parseSchedule(expr string, loc *time.Location) (schedule.Schedule, error) {
	return schedule.Parse(expr, loc)
}

// reservedLabels are label names that mcs-backup uses on its own metrics.
func reservedLabels() []string {
	return []string{"version", "repository_id", "kind", "state", "type", "operation"}
}

var labelName = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// parseLabels parses a YAML or JSON mapping into Prometheus label values.
func parseLabels(s string) (map[string]string, error) {
	var raw map[string]any
	if err := yaml.Unmarshal([]byte(s), &raw); err != nil {
		return nil, fmt.Errorf("invalid label mapping: %w", err)
	}
	labels := make(map[string]string, len(raw))
	for k, v := range raw {
		switch {
		case !labelName.MatchString(k) || strings.HasPrefix(k, "__"):
			return nil, fmt.Errorf("invalid label name %q", k)
		case slices.Contains(reservedLabels(), k):
			return nil, fmt.Errorf("label name %q is reserved", k)
		}
		switch v.(type) {
		case map[string]any, []any:
			return nil, fmt.Errorf("label %q: value must be a scalar", k)
		}
		labels[k] = fmt.Sprint(v)
	}
	return labels, nil
}
