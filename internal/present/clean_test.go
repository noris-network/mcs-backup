package present

import (
	"testing"

	"github.com/noris-network/mcs-backup/v2/internal/service"
)

func TestNoteSuffixIsClean(t *testing.T) {
	// a note is a tag, and a tag may come from another writer of the repository
	if got := NoteSuffix(service.Snapshot{Note: "a\x1b[2Jb\nc"}); got != ", note: a\ufffd[2Jb\ufffdc" {
		t.Errorf("NoteSuffix = %q", got)
	}
}
