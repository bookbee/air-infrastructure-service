// Package observability holds the structured logger and the Prometheus metrics.
package observability

import (
	"context"
	"log/slog"
	"os"
	"strings"
)

// NewLogger builds the process logger. render selects the handler: "json" for
// deployed environments, anything else for the human-readable console form the
// local stack uses.
func NewLogger(level, render string, out *os.File) *slog.Logger {
	opts := &slog.HandlerOptions{Level: parseLevel(level)}
	var h slog.Handler
	if render == "json" {
		h = slog.NewJSONHandler(out, opts)
	} else {
		h = slog.NewTextHandler(out, opts)
	}
	return slog.New(h)
}

// parseLevel maps the configured level name to a slog level, defaulting to
// INFO. An unrecognised level logs more rather than less: silence is the worse
// failure for an operator trying to diagnose a boot.
func parseLevel(level string) slog.Level {
	switch strings.ToUpper(strings.TrimSpace(level)) {
	case "DEBUG":
		return slog.LevelDebug
	case "WARN", "WARNING":
		return slog.LevelWarn
	case "ERROR", "CRITICAL":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

type loggerKey struct{}

// WithLogger returns a context carrying log, so a handler can emit lines that
// already carry the request id without threading a logger through every call.
// This is the Go equivalent of the Python's structlog contextvars binding.
func WithLogger(ctx context.Context, log *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey{}, log)
}

// LoggerFrom returns the request-scoped logger, or fallback if the context
// carries none.
func LoggerFrom(ctx context.Context, fallback *slog.Logger) *slog.Logger {
	if log, ok := ctx.Value(loggerKey{}).(*slog.Logger); ok {
		return log
	}
	return fallback
}
