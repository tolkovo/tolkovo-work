// Package logging builds the process-wide slog logger.
package logging

import (
	"io"
	"log/slog"
)

// New returns a logger writing JSON (production) or text (local development).
func New(w io.Writer, level slog.Level, format string) *slog.Logger {
	opts := &slog.HandlerOptions{Level: level}
	if format == "text" {
		return slog.New(slog.NewTextHandler(w, opts))
	}
	return slog.New(slog.NewJSONHandler(w, opts))
}
