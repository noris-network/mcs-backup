package config

import (
	"fmt"
	"strconv"
	"strings"
)

// parsePercent parses "50", "50%" or "12.5 %" into a value between 0 and 100.
func parsePercent(s string) (float64, error) {
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "%"))
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid percentage %q", s)
	}
	if v < 0 || v > 100 {
		return 0, fmt.Errorf("percentage %v out of range 0..100", v)
	}
	return v, nil
}

// parseSize parses a byte size such as "1048576", "256MiB" or "1.5GB". The
// suffixes KiB, MiB, GiB, TiB and the short forms K, M, G, T are binary (1024),
// KB, MB, GB, TB are decimal (1000).
func parseSize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	i := strings.IndexFunc(s, func(r rune) bool { return (r < '0' || r > '9') && r != '.' })
	num, unit := s, ""
	if i >= 0 {
		num, unit = s[:i], strings.TrimSpace(s[i:])
	}
	v, err := strconv.ParseFloat(num, 64)
	if err != nil || v < 0 {
		return 0, fmt.Errorf("invalid size %q", s)
	}
	var mult float64
	switch strings.ToLower(unit) {
	case "", "b":
		mult = 1
	case "k", "kib":
		mult = 1 << 10
	case "m", "mib":
		mult = 1 << 20
	case "g", "gib":
		mult = 1 << 30
	case "t", "tib":
		mult = 1 << 40
	case "kb":
		mult = 1e3
	case "mb":
		mult = 1e6
	case "gb":
		mult = 1e9
	case "tb":
		mult = 1e12
	default:
		return 0, fmt.Errorf("invalid size unit %q", unit)
	}
	return int64(v * mult), nil
}
