// Package logs builds the logger of the coordinator and worker commands.
package logs

import (
	"fmt"
	"io"
	"log/slog"
	"os"
)

// Level returns the slog level named by name: debug, info, warn or error.
func Level(name string) (slog.Level, error) {
	var l slog.Level
	switch name {
	case "debug":
		l = slog.LevelDebug
	case "info":
		l = slog.LevelInfo
	case "warn":
		l = slog.LevelWarn
	case "error":
		l = slog.LevelError
	default:
		return l, fmt.Errorf("log level %q is not debug, info, warn or error", name)
	}
	return l, nil
}

// New returns a logger that writes to w in format, "text" or "json", records
// at level, "debug", "info", "warn" or "error", and above.
func New(format, level string, w io.Writer) (*slog.Logger, error) {
	l, err := Level(level)
	if err != nil {
		return nil, err
	}
	opts := &slog.HandlerOptions{Level: l}
	switch format {
	case "text":
		return slog.New(slog.NewTextHandler(w, opts)), nil
	case "json":
		return slog.New(slog.NewJSONHandler(w, opts)), nil
	}
	return nil, fmt.Errorf("log format %q is not text or json", format)
}

// Setup sets the default logger to one that writes to stderr in format at
// level.
func Setup(format, level string) error {
	log, err := New(format, level, os.Stderr)
	if err == nil {
		slog.SetDefault(log)
	}
	return err
}
