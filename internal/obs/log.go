package obs

import (
	"context"
	"io"
	"log/slog"
	"strings"
)

// SetupLogging configures the process logger from the environment and
// installs it as slog's default: MEMORS_LOG_FORMAT text|json (default text),
// MEMORS_LOG_LEVEL debug|info|warn|error (default info). Unknown values fall
// back to the default and are reported once on the new logger.
func SetupLogging(w io.Writer, getenv func(string) string) *slog.Logger {
	level := slog.LevelInfo
	badLevel := ""
	switch v := strings.ToLower(strings.TrimSpace(getenv("MEMORS_LOG_LEVEL"))); v {
	case "", "info":
	case "debug":
		level = slog.LevelDebug
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		badLevel = v
	}
	opts := &slog.HandlerOptions{Level: level}
	var h slog.Handler
	badFormat := ""
	switch v := strings.ToLower(strings.TrimSpace(getenv("MEMORS_LOG_FORMAT"))); v {
	case "", "text":
		h = slog.NewTextHandler(w, opts)
	case "json":
		h = slog.NewJSONHandler(w, opts)
	default:
		badFormat = v
		h = slog.NewTextHandler(w, opts)
	}
	l := slog.New(h)
	slog.SetDefault(l)
	if badLevel != "" {
		l.Warn("unknown MEMORS_LOG_LEVEL, using info", "value", badLevel)
	}
	if badFormat != "" {
		l.Warn("unknown MEMORS_LOG_FORMAT, using text", "value", badFormat)
	}
	return l
}

// MinLevel wraps a handler so records below level are dropped: used for
// chatty third-party loggers (the MCP SDK reports every session at Info).
func MinLevel(h slog.Handler, level slog.Level) slog.Handler { return &minLevel{h: h, level: level} }

type minLevel struct {
	h     slog.Handler
	level slog.Level
}

func (m *minLevel) Enabled(ctx context.Context, l slog.Level) bool {
	return l >= m.level && m.h.Enabled(ctx, l)
}
func (m *minLevel) Handle(ctx context.Context, r slog.Record) error { return m.h.Handle(ctx, r) }
func (m *minLevel) WithAttrs(a []slog.Attr) slog.Handler {
	return &minLevel{h: m.h.WithAttrs(a), level: m.level}
}
func (m *minLevel) WithGroup(g string) slog.Handler {
	return &minLevel{h: m.h.WithGroup(g), level: m.level}
}
