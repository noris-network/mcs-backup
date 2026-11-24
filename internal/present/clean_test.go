package present

import "testing"

func TestClean(t *testing.T) {
	for _, tt := range []struct{ in, text, name string }{
		{"plain ünï", "plain ünï", "plain ünï"},
		{"a\x1b[2Jb", "a�[2Jb", "a�[2Jb"},
		{"one\n\ttwo\r", "one\n\ttwo�", "one��two�"},
		{"bell\a c1\u009b", "bell� c1�", "bell� c1�"},
	} {
		if got := Clean(tt.in); got != tt.text {
			t.Errorf("Clean(%q) = %q, want %q", tt.in, got, tt.text)
		}
		if got := CleanName(tt.in); got != tt.name {
			t.Errorf("CleanName(%q) = %q, want %q", tt.in, got, tt.name)
		}
	}
}
