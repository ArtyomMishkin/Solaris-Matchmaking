package logx

import (
	"log/slog"
	"os"
	"strings"
)

// SetupFromEnv configures the default slog logger.
// LOG_LEVEL: debug, info (default), warn, error
// LOG_FORMAT: text (default), json
func SetupFromEnv() {
	level := parseLevel(strings.TrimSpace(os.Getenv("LOG_LEVEL")))
	var handler slog.Handler
	opts := &slog.HandlerOptions{Level: level}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("LOG_FORMAT"))) {
	case "json":
		handler = slog.NewJSONHandler(os.Stdout, opts)
	default:
		handler = slog.NewTextHandler(os.Stdout, opts)
	}
	slog.SetDefault(slog.New(handler))
}

func parseLevel(s string) slog.Leveler {
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
