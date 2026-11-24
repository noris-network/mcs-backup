// Package lineio handles what a process prints: it passes the output on line
// by line and keeps the end of it for error messages.
package lineio

import (
	"bytes"
	"strings"
)

// DefaultMax is the longest line a Writer passes on in one piece if Max is not set.
const DefaultMax = 64 << 10

// Writer calls Fn for every line written to it, without its line break. A
// process that prints without line breaks (a progress bar, binary data) must
// not fill the memory: a line longer than Max is passed on in pieces. Call
// Flush once the process is done, for a last line that has no line break.
type Writer struct {
	// Fn receives a line, which is only valid during the call. With a nil Fn
	// the output is dropped.
	Fn func(line []byte)
	// Max is the longest line passed on in one piece, DefaultMax if zero.
	Max int

	buf []byte
}

// Write implements io.Writer. It never fails.
func (w *Writer) Write(p []byte) (int, error) {
	if w.Fn == nil {
		return len(p), nil
	}
	limit := w.Max
	if limit <= 0 {
		limit = DefaultMax
	}
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		switch {
		case i >= 0:
			w.Fn(bytes.TrimRight(w.buf[:i], "\r"))
			w.buf = w.buf[i+1:]
		case len(w.buf) >= limit:
			w.Fn(w.buf[:limit])
			w.buf = w.buf[limit:]
		default:
			return len(p), nil
		}
	}
}

// Flush passes on a last line without a line break.
func (w *Writer) Flush() {
	if w.Fn != nil && len(w.buf) > 0 {
		w.Fn(bytes.TrimRight(w.buf, "\r"))
	}
	w.buf = nil
}

// Tail keeps the last Max bytes written to it.
type Tail struct {
	Max int

	buf []byte
}

// Write implements io.Writer. It never fails.
func (t *Tail) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.Max {
		t.buf = t.buf[len(t.buf)-t.Max:]
	}
	return len(p), nil
}

// String returns what was kept, without the white space around it.
func (t *Tail) String() string { return strings.TrimSpace(string(t.buf)) }
