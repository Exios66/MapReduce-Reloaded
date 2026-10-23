// Package logs builds the logger of the coordinator and worker commands.
package logs

import (
	"fmt"
	"io"
	"log/slog"
	"os"
)

// New returns a logger that writes to w in format, "text" or "json".
func New(format string, w io.Writer) (*slog.Logger, error) {
	switch format {
	case "text":
		return slog.New(slog.NewTextHandler(w, nil)), nil
	case "json":
		return slog.New(slog.NewJSONHandler(w, nil)), nil
	}
	return nil, fmt.Errorf("log format %q is not text or json", format)
}

// Setup sets the default logger to one that writes to stderr in format.
func Setup(format string) error {
	log, err := New(format, os.Stderr)
	if err == nil {
		slog.SetDefault(log)
	}
	return err
}
