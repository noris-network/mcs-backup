package lineio

import (
	"slices"
	"strings"
	"testing"
)

func collect(limit int) (*Writer, *[]string) {
	var got []string
	return &Writer{Max: limit, Fn: func(l []byte) { got = append(got, string(l)) }}, &got
}

func TestWriterSplitsLines(t *testing.T) {
	w, got := collect(0)
	for _, chunk := range []string{"one\ntw", "o\r\n\nthree\n", "last"} {
		if n, err := w.Write([]byte(chunk)); n != len(chunk) || err != nil {
			t.Fatalf("Write = %d, %v", n, err)
		}
	}
	if want := []string{"one", "two", "", "three"}; !slices.Equal(*got, want) {
		t.Errorf("lines = %q, want %q", *got, want)
	}
	w.Flush()
	w.Flush() // nothing is left
	if want := []string{"one", "two", "", "three", "last"}; !slices.Equal(*got, want) {
		t.Errorf("lines after Flush = %q, want %q", *got, want)
	}
}

func TestWriterCutsALineWithoutBreak(t *testing.T) {
	w, got := collect(8)
	_, _ = w.Write([]byte(strings.Repeat("x", 20)))
	if len(w.buf) >= 8 {
		t.Errorf("%d bytes are buffered, more than a line may have", len(w.buf))
	}
	_, _ = w.Write([]byte("\nend\n"))
	if want := []string{"xxxxxxxx", "xxxxxxxx", "xxxx", "end"}; !slices.Equal(*got, want) {
		t.Errorf("lines = %q, want %q", *got, want)
	}
}

func TestWriterWithoutFn(t *testing.T) {
	w := &Writer{}
	if n, err := w.Write([]byte("dropped\n")); n != 8 || err != nil || len(w.buf) != 0 {
		t.Errorf("Write = %d, %v, %d bytes kept", n, err, len(w.buf))
	}
	w.Flush()
}

func TestTail(t *testing.T) {
	tail := &Tail{Max: 6}
	_, _ = tail.Write([]byte("first line\n"))
	_, _ = tail.Write([]byte("second\n"))
	if got := tail.String(); got != "econd" {
		t.Errorf("Tail = %q", got)
	}
}
