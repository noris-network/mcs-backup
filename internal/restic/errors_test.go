package restic

import "testing"

func TestErrorMessage(t *testing.T) {
	tests := []struct{ name, stderr, want string }{
		{"plain", "  Fatal: oops\n", "Fatal: oops"},
		{"json exit_error", `{"message_type":"error","error":{"message":"x"}}` + "\n" +
			`{"message_type":"exit_error","code":1,"message":"Fatal: unable to save snapshot"}` + "\n", "Fatal: unable to save snapshot"},
		{"json without exit_error", `{"message_type":"error"}`, `{"message_type":"error"}`},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		if got := errorMessage(tt.stderr); got != tt.want {
			t.Errorf("%s: errorMessage() = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestExitErrorString(t *testing.T) {
	if got := (&ExitError{Command: "prune", Code: 1}).Error(); got != "restic prune: exit code 1" {
		t.Errorf("Error() = %q", got)
	}
	if got := (&ExitError{Command: "prune", Code: 1, Message: "boom"}).Error(); got != "restic prune: exit code 1: boom" {
		t.Errorf("Error() = %q", got)
	}
}
