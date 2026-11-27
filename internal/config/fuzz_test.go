package config

import (
	"slices"
	"strings"
	"testing"
)

// FuzzSplitArgs checks that SplitArgs never panics, that what it accepts is
// given back unchanged by SplitArgs(JoinArgs(...)), and that JoinArgs quotes
// any list of arguments (here separated by NUL in the input) the same way.
func FuzzSplitArgs(f *testing.F) {
	for _, s := range []string{
		``, `--one-file-system`, `--exclude "a b" -x`, `'it'\''s'`, `"a\"b\\c"`, `a\ b`,
		`"unterminated`, `trailing\`, "tab\tand\nnewline", "--exclude=\xe4rger", "a\x00b c\x00'",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if args, err := SplitArgs(s); err == nil {
			back, err := SplitArgs(JoinArgs(args))
			if err != nil || !slices.Equal(back, args) {
				t.Fatalf("SplitArgs(%q) = %q, round trip = %q, %v", s, args, back, err)
			}
		}
		args := strings.Split(s, "\x00")
		back, err := SplitArgs(JoinArgs(args))
		if err != nil || !slices.Equal(back, args) {
			t.Fatalf("JoinArgs(%q) = %q, split again = %q, %v", args, JoinArgs(args), back, err)
		}
	})
}

// FuzzParseValues checks that the parsers of settings never panic and never
// accept a value outside of their range.
func FuzzParseValues(f *testing.F) {
	for _, s := range []string{
		"50", "50%", "12.5 %", "-1", "1e3", "NaN", "inf", "256MiB", "1.5GB", "99999999999999999999T",
		"", `{"daily":7}`, "daily: 7\nweekly: 4", "{", "last: -1", "&a [*a]",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if p, err := parsePercent(s); err == nil && !(p >= 0 && p <= 100) {
			t.Errorf("parsePercent(%q) = %v", s, p)
		}
		if n, err := parseSize(s); err == nil && n < 0 {
			t.Errorf("parseSize(%q) = %d", s, n)
		}
		if r, err := ParseRetention(s); err == nil {
			if r.IsZero() || r.Last < 0 || r.Hourly < 0 || r.Daily < 0 || r.Weekly < 0 || r.Monthly < 0 || r.Yearly < 0 {
				t.Errorf("ParseRetention(%q) = %+v", s, r)
			}
		}
	})
}
