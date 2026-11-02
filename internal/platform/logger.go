package platform

import (
	"log/slog"
	"os"
	"strings"
)

// NewLogger builds a structured logger. Production uses JSON, development uses
// human-readable text with debug level.
func NewLogger(env, service, version string) *slog.Logger {
	level := slog.LevelDebug
	if strings.EqualFold(env, "production") {
		level = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: level}
	var h slog.Handler
	if strings.EqualFold(env, "production") {
		h = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		h = slog.NewTextHandler(os.Stdout, opts)
	}
	return slog.New(h).With("service", service, "version", version, "env", env)
}
