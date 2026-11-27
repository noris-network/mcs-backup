package restic

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// FuzzErrorMessage checks that what restic writes to stderr never panics the
// parsing, and that text without an exit_error line is passed on as it is.
func FuzzErrorMessage(f *testing.F) {
	files, err := filepath.Glob("testdata/err_*.txt")
	if err != nil {
		f.Fatal(err)
	}
	for _, name := range files {
		data, err := os.ReadFile(name) //nolint:gosec // test data
		if err != nil {
			f.Fatal(err)
		}
		f.Add(string(data))
	}
	f.Add(`{"message_type":"exit_error","code":10,"message":"repository does not exist"}`)
	f.Add("Fatal: unable to open config file: Stat: The specified bucket does not exist.\n")
	f.Add("{\"message_type\":\"exit_error\",\"message\":\"\"}\nplain")
	f.Fuzz(func(t *testing.T, stderr string) {
		msg := errorMessage(stderr)
		_ = storageMissing([]byte(stderr))
		if !strings.Contains(stderr, "exit_error") && msg != strings.TrimSpace(stderr) {
			t.Errorf("errorMessage(%q) = %q", stderr, msg)
		}
	})
}
