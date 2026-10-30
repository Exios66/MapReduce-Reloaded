package logs_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/exios66/mapreduce-reloaded/go/internal/logs"
)

func TestJSON(t *testing.T) {
	var b bytes.Buffer
	log, err := logs.New("json", "info", &b)
	if err != nil {
		t.Fatal(err)
	}
	log.Info("task failed", "job", "job-1")
	var line map[string]any
	if err := json.Unmarshal(b.Bytes(), &line); err != nil || line["msg"] != "task failed" || line["job"] != "job-1" {
		t.Fatalf("got %s (%v)", b.Bytes(), err)
	}
}

func TestUnknownFormat(t *testing.T) {
	if _, err := logs.New("xml", "info", nil); err == nil {
		t.Fatal("no error")
	}
}

func TestText(t *testing.T) {
	var b bytes.Buffer
	log, err := logs.New("text", "info", &b)
	if err != nil {
		t.Fatal(err)
	}
	log.Info("task failed", "job", "job-1")
	if got := b.String(); !strings.Contains(got, "level=INFO") || !strings.Contains(got, `msg="task failed" job=job-1`) {
		t.Fatalf("got %q", got)
	}
}

// Setup replaces the default logger, and leaves it alone for an unknown
// format. Debug records are dropped, as at the default level.
func TestSetup(t *testing.T) {
	old := slog.Default()
	t.Cleanup(func() { slog.SetDefault(old) })
	for _, format := range []string{"text", "json"} {
		slog.SetDefault(old)
		if err := logs.Setup(format, "info"); err != nil {
			t.Fatalf("Setup(%q): %v", format, err)
		}
		if slog.Default() == old {
			t.Fatalf("Setup(%q) kept the old logger", format)
		}
		if slog.Default().Enabled(context.Background(), slog.LevelDebug) || !slog.Default().Enabled(context.Background(), slog.LevelInfo) {
			t.Fatalf("Setup(%q): want info and above", format)
		}
	}
	set := slog.Default()
	if err := logs.Setup("xml", "info"); err == nil || slog.Default() != set {
		t.Fatalf("Setup(xml): %v, and the logger changed: %v", err, slog.Default() != set)
	}
}

// The level drops what is below it, and an unknown level is an error.
func TestLevel(t *testing.T) {
	for name, want := range map[string]slog.Level{"debug": slog.LevelDebug, "info": slog.LevelInfo, "warn": slog.LevelWarn, "error": slog.LevelError} {
		var b bytes.Buffer
		log, err := logs.New("text", name, &b)
		if err != nil {
			t.Fatalf("New(%q): %v", name, err)
		}
		for _, l := range []slog.Level{slog.LevelDebug, slog.LevelInfo, slog.LevelWarn, slog.LevelError} {
			if got := log.Enabled(context.Background(), l); got != (l >= want) {
				t.Errorf("level %q: Enabled(%v) = %v", name, l, got)
			}
		}
	}
	if _, err := logs.New("text", "loud", nil); err == nil {
		t.Fatal("no error for level loud")
	}
}
