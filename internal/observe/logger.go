// Package observe configures structured logging and (later) tracing.
package observe

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

// NewLogger builds a slog.Logger writing to stderr with the requested
// level/format. stdout is reserved for the stdio MCP transport, so logging must
// never touch stdout.
func NewLogger(level, format string) *slog.Logger {
	return NewLoggerTo(os.Stderr, level, format)
}

// NewLoggerTo builds a logger writing to w.
func NewLoggerTo(w io.Writer, level, format string) *slog.Logger {
	lvl := parseLevel(level)
	opts := &slog.HandlerOptions{Level: lvl}
	var h slog.Handler
	if strings.EqualFold(format, "text") {
		h = slog.NewTextHandler(w, opts)
	} else {
		h = slog.NewJSONHandler(w, opts)
	}
	return slog.New(h)
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// AuditSink returns the writer to use for audit records: a file at path, or the
// provided fallback (typically stderr) when path is empty.
func AuditSink(path string, fallback io.Writer) io.Writer {
	if path == "" {
		return fallback
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fallback
	}
	return f
}
