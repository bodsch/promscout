// Package logging provides structured logging based on Go's slog package.
package logging

import (
	"log/slog"
	"os"
	"strings"
)

// Logger wraps slog.Logger to expose a simplified interface.
type Logger struct {
	*slog.Logger
}

// New creates a new structured logger based on format and level.
func New(format, level string) *Logger {
	var lvl slog.Level

	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{
		Level: lvl,
	}

	var handler slog.Handler

	if format == "text" {
		handler = slog.NewTextHandler(os.Stdout, opts)
	} else {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}

	return &Logger{
		Logger: slog.New(handler),
	}
}
