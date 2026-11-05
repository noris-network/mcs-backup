package redact

import (
	"context"
	"log/slog"
)

// Handler returns a slog.Handler that hides the secrets in the message and in
// the string and error values of every record before h sees it.
func (r *Redactor) Handler(h slog.Handler) slog.Handler { return &handler{h: h, r: r} }

type handler struct {
	h slog.Handler
	r *Redactor
}

func (h *handler) Enabled(ctx context.Context, level slog.Level) bool { return h.h.Enabled(ctx, level) }

func (h *handler) Handle(ctx context.Context, rec slog.Record) error {
	out := slog.NewRecord(rec.Time, rec.Level, h.r.String(rec.Message), rec.PC)
	for a := range rec.Attrs {
		out.AddAttrs(h.attr(a))
	}
	return h.h.Handle(ctx, out)
}

func (h *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		out[i] = h.attr(a)
	}
	return &handler{h: h.h.WithAttrs(out), r: h.r}
}

func (h *handler) WithGroup(name string) slog.Handler {
	return &handler{h: h.h.WithGroup(name), r: h.r}
}

// attr hides the secrets in a string or an error, also inside groups.
func (h *handler) attr(a slog.Attr) slog.Attr {
	a.Value = a.Value.Resolve()
	switch a.Value.Kind() {
	case slog.KindString:
		a.Value = slog.StringValue(h.r.String(a.Value.String()))
	case slog.KindGroup:
		group := a.Value.Group()
		out := make([]slog.Attr, len(group))
		for i, g := range group {
			out[i] = h.attr(g)
		}
		a.Value = slog.GroupValue(out...)
	case slog.KindAny:
		if err, ok := a.Value.Any().(error); ok {
			a.Value = slog.StringValue(h.r.String(err.Error()))
		}
	}
	return a
}
