// Package clean makes text that comes from outside safe to show: the output of
// restic and of the scripts, and the names that others wrote into a repository.
package clean

import (
	"strings"
	"unicode"
)

// Text makes text that comes from outside safe to print on a terminal: the
// output of restic and of the scripts, and what they quote. A control
// character, such as the escape that starts a terminal sequence, becomes the
// replacement character. Line breaks and tabs stay.
func Text(s string) string {
	return replace(s, true)
}

// Name does the same for a name that is shown in one line, such as the
// path of a file in a snapshot: a line break or a tab in it is replaced as
// well. Only what is shown is cleaned; requests keep the name as it is.
func Name(s string) string {
	return replace(s, false)
}

func replace(s string, keepBreaks bool) string {
	bad := func(r rune) bool {
		if keepBreaks && (r == '\n' || r == '\t') {
			return false
		}
		return unicode.IsControl(r)
	}
	if !strings.ContainsFunc(s, bad) {
		return s
	}
	return strings.Map(func(r rune) rune {
		if bad(r) {
			return unicode.ReplacementChar
		}
		return r
	}, s)
}
