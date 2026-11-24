package clean

import "testing"

func TestClean(t *testing.T) {
	for _, tt := range []struct{ in, text, name string }{
		{"plain ünï", "plain ünï", "plain ünï"},
		{"a\x1b[2Jb", "a\ufffd[2Jb", "a\ufffd[2Jb"},
		{"one\n\ttwo\r", "one\n\ttwo\ufffd", "one\ufffd\ufffdtwo\ufffd"},
		{"bell\a c1\u009b", "bell\ufffd c1\ufffd", "bell\ufffd c1\ufffd"},
	} {
		if got := Text(tt.in); got != tt.text {
			t.Errorf("Text(%q) = %q, want %q", tt.in, got, tt.text)
		}
		if got := Name(tt.in); got != tt.name {
			t.Errorf("Name(%q) = %q, want %q", tt.in, got, tt.name)
		}
	}
}
