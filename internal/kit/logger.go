package kit

import (
	"io"
	"log/slog"
	"os"
)

// NewLogger returns a structured logger for a service. In development it writes
// human-readable text to stdout; in other environments it writes JSON.
func NewLogger(env, service string) *slog.Logger {
	opts := &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}
	var handler slog.Handler
	if env == "development" {
		handler = slog.NewTextHandler(os.Stdout, opts)
	} else {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}
	return slog.New(handler).With(slog.String("service", service))
}

// NewTestLogger returns a logger that discards output, useful for tests.
func NewTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
