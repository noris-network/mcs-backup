package daemon

import (
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/noris-network/mcs-backup/v2/internal/service"
)

// subscriberBuffer is how many events a client may fall behind before it is dropped.
const subscriberBuffer = 512

// broadcaster delivers events to all subscribers without ever blocking the
// publisher. A subscriber that falls too far behind is closed and has to
// reconnect, which gives it a fresh view of the running operation.
type broadcaster struct {
	mu   sync.Mutex
	subs map[chan service.Event]struct{}
}

func newBroadcaster() *broadcaster {
	return &broadcaster{subs: map[chan service.Event]struct{}{}}
}

func (b *broadcaster) subscribe() (<-chan service.Event, func()) {
	ch := make(chan service.Event, subscriberBuffer)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if _, ok := b.subs[ch]; ok {
			delete(b.subs, ch)
			close(ch)
		}
	}
}

func (b *broadcaster) publish(ev service.Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- ev:
		default:
			delete(b.subs, ch)
			close(ch)
		}
	}
}

// maxLogLines bounds the log kept of a run.
const maxLogLines = 5000

// runLog keeps the log of the latest run of a kind.
type runLog struct {
	start   time.Time
	lines   []string
	dropped int
}

func (l *runLog) add(line string) {
	if len(l.lines) >= maxLogLines {
		l.lines = slices.Delete(l.lines, 0, maxLogLines/10)
		l.dropped += maxLogLines / 10
	}
	l.lines = append(l.lines, line)
}

func (l *runLog) snapshot(kind service.Kind) service.Log {
	out := service.Log{Kind: kind, Start: l.start, Lines: slices.Clone(l.lines)}
	if l.dropped > 0 {
		out.Lines = append([]string{"... " + strconv.Itoa(l.dropped) + " earlier lines dropped ..."}, out.Lines...)
	}
	return out
}
