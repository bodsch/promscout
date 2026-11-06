// Package logging provides structured logging based on Go's slog package.
package logging

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

// Logger wraps slog.Logger to expose a simplified interface.
type Logger struct {
	*slog.Logger
}

// New creates a new structured logger writing to stdout.
//
// Parameters:
//   - format: "text" for logfmt-style output; anything else yields JSON.
//   - level: "debug", "info" or "error" (case-insensitive); anything else
//     yields info.
//
// Returns the configured logger.
func New(format, level string) *Logger {
	return NewWithWriter(os.Stdout, format, level)
}

// NewWithWriter creates a new structured logger writing to w.
//
// It exists so tests can read back what the service logs; production code
// uses New.
//
// Parameters:
//   - w: destination of the log records.
//   - format: "text" for logfmt-style output; anything else yields JSON.
//   - level: "debug", "info" or "error" (case-insensitive); anything else
//     yields info.
//
// Returns the configured logger.
func NewWithWriter(w io.Writer, format, level string) *Logger {
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
		Level:       lvl,
		ReplaceAttr: durationAsString,
	}

	var handler slog.Handler

	if format == "text" {
		handler = slog.NewTextHandler(w, opts)
	} else {
		handler = slog.NewJSONHandler(w, opts)
	}

	return &Logger{
		Logger: slog.New(handler),
	}
}

// durationAsString renders time.Duration attributes in Go duration syntax
// ("1m0s", "800ms").
//
// slog's JSON handler writes a duration as integer nanoseconds
// ("interval":60000000000), which nobody reads at a glance and which differs
// from both the text handler and the configuration syntax. Applying this to
// every attribute keeps the two formats identical, so a value can be copied
// from the log into the config unchanged.
//
// Parameters:
//   - groups: the enclosing attribute groups (unused).
//   - a: the attribute being written.
//
// Returns a with a string value if it held a duration, otherwise a unchanged.
func durationAsString(_ []string, a slog.Attr) slog.Attr {
	if a.Value.Kind() == slog.KindDuration {
		a.Value = slog.StringValue(a.Value.Duration().String())
	}
	return a
}
