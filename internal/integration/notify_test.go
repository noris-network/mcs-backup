//go:build integration

package integration

import (
	"encoding/json/v2"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/noris-network/mcs-backup/v2/internal/service"
)

// events returns the lines of the notification log once it has n of them.
func (i *inst) events(path string, n int) []string {
	i.t.Helper()
	var lines []string
	i.waitFor("notifications", 20*time.Second, func() bool {
		b, err := readFile(path)
		if err != nil {
			return false
		}
		lines = strings.Fields(strings.ReplaceAll(b, "\n", " "))
		return len(lines) >= n
	})
	return lines
}

func TestNotificationsGoToTheCommand(t *testing.T) {
	i := newInst(t, "NOTIFY_ON=all")
	log := filepath.Join(i.home, "events.log")
	stdin := filepath.Join(i.home, "stdin.json")
	cmd := i.script("notify.sh", `echo "$MCS_BACKUP_EVENT" >> `+log+`; cat > `+stdin)
	flag := filepath.Join(i.home, "break")
	pre := i.script("pre.sh", `test ! -e `+flag)
	i.set("NOTIFY_COMMAND="+cmd, "PRE_BACKUP_SCRIPT="+pre)
	i.write("a.txt", "a")
	i.start()

	i.mustBackup("", false)
	if got := i.events(log, 1); got[0] != "backup.succeeded" {
		t.Fatalf("events = %v", got)
	}

	writeFile(t, flag, "")
	if res := i.backup("", false); res.Result != service.ResultFailure {
		t.Fatalf("result = %s", res.Result)
	}
	if got := i.events(log, 2); got[1] != "backup.failed" {
		t.Fatalf("events = %v", got)
	}

	if err := removeFile(flag); err != nil {
		t.Fatal(err)
	}
	i.mustBackup("", false)
	if got := i.events(log, 3); got[2] != "backup.recovered" {
		t.Fatalf("events = %v", got)
	}

	// the event is also on stdin, as JSON
	var ev struct {
		Event  string `json:"event"`
		Kind   string `json:"kind"`
		Result string `json:"result"`
		Host   string `json:"host"`
	}
	i.waitFor("the JSON event", 10*time.Second, func() bool {
		b, err := readFile(stdin)
		return err == nil && json.Unmarshal([]byte(b), &ev) == nil && ev.Event == "backup.recovered"
	})
	if ev.Kind != "backup" || ev.Host != "mcs-backup" {
		t.Errorf("event = %+v", ev)
	}
}

func TestABrokenNotificationCommandChangesNothing(t *testing.T) {
	i := newInst(t, "NOTIFY_ON=all", "NOTIFY_COMMAND=/nonexistent/notify")
	i.write("a.txt", "a")
	i.start()
	if res := i.mustBackup("", false); res.Result != service.ResultSuccess {
		t.Fatalf("result = %s", res.Result)
	}
	_ = i.d.NotifyTest(i.ctx()) // queued or refused, either way nothing else may change
	i.mustBackup("", false)
}

func TestOnlyFailuresAreReportedByDefault(t *testing.T) {
	i := newInst(t)
	log := filepath.Join(i.home, "events.log")
	cmd := i.script("notify.sh", `echo "$MCS_BACKUP_EVENT" >> `+log)
	flag := filepath.Join(i.home, "break")
	pre := i.script("pre.sh", `test ! -e `+flag)
	i.set("NOTIFY_COMMAND="+cmd, "PRE_BACKUP_SCRIPT="+pre)
	i.write("a.txt", "a")
	i.start()

	i.mustBackup("", false) // success: not reported by default
	writeFile(t, flag, "")
	i.backup("", false)
	if got := i.events(log, 1); got[0] != "backup.failed" {
		t.Fatalf("events = %v", got)
	}
	time.Sleep(time.Second)
	if b, _ := readFile(log); strings.Count(b, "\n") != 1 {
		t.Errorf("more events than the failure:\n%s", b)
	}
}
