package main

import (
	"io"
	"log/slog"
)

// newLogger builds the process logger. Logs are JSON by default;
// MCS_BACKUP_LOG_FORMAT=text selects a human readable format and
// MCS_BACKUP_DEBUG=true enables debug level.
func newLogger(w io.Writer, getenv func(string) string) *slog.Logger {
	opts := &slog.HandlerOptions{Level: slog.LevelInfo}
	if getenv("MCS_BACKUP_DEBUG") == "true" {
		opts.Level = slog.LevelDebug
	}
	if getenv("MCS_BACKUP_LOG_FORMAT") == "text" {
		return slog.New(slog.NewTextHandler(w, opts))
	}
	return slog.New(slog.NewJSONHandler(w, opts))
}
