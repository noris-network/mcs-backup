package clean

import (
	"strings"
	"testing"
	"unicode"
)

// FuzzText checks that no control character gets through, apart from the
// line breaks and tabs that Text keeps, and that clean text is left alone.
func FuzzText(f *testing.F) {
	for _, s := range []string{"", "plain", "\x1b[31mred\x1b[0m", "a\nb\tc\rd", "\u009b2J", "\x00\x7f", "\xff\xfe", "ä€😀"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		text, name := Text(s), Name(s)
		if i := strings.IndexFunc(text, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' }); i >= 0 {
			t.Errorf("Text(%q) = %q keeps a control character at %d", s, text, i)
		}
		if i := strings.IndexFunc(name, unicode.IsControl); i >= 0 {
			t.Errorf("Name(%q) = %q keeps a control character at %d", s, name, i)
		}
		if !strings.ContainsFunc(s, unicode.IsControl) && name != s {
			t.Errorf("Name(%q) = %q changed a clean name", s, name)
		}
	})
}
