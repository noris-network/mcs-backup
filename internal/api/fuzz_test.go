package api

import (
	"strings"
	"testing"

	"github.com/noris-network/mcs-backup/v2/internal/service"
)

// FuzzEvents feeds arbitrary streams to the reader of server-sent events: it
// must not panic, and after an error or a refusal of yield it must not call
// yield again.
func FuzzEvents(f *testing.F) {
	for _, s := range []string{
		"event: state\ndata: {\"type\":\"state\"}\n\n",
		": heartbeat\n\ndata: {\"type\":\"log\",\"line\":\"x\"}\n\n",
		"event: error\ndata: {\"code\":\"busy\",\"message\":\"backup running\"}\n\n",
		"data: {not json\n\n",
		"data: {\"type\":\ndata: \"state\"}\n\n",
		"event:\ndata:\n\n\n",
		strings.Repeat("data: x", 1000) + "\n",
	} {
		f.Add(s, 1)
	}
	f.Fuzz(func(t *testing.T, stream string, accept int) {
		calls, ended := 0, false
		_ = readEvents(strings.NewReader(stream), func(_ service.Event, err error) bool {
			if ended {
				t.Fatalf("yield called after the stream ended: %q", stream)
			}
			calls++
			if err != nil || calls > accept {
				ended = true
				return false
			}
			return true
		})
	})
}
