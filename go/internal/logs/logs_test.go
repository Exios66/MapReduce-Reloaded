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
	log, err := logs.New("json", &b)
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
	if _, err := logs.New("xml", nil); err == nil {
		t.Fatal("no error")
	}
}

func TestText(t *testing.T) {
	var b bytes.Buffer
	log, err := logs.New("text", &b)
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
		if err := logs.Setup(format); err != nil {
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
	if err := logs.Setup("xml"); err == nil || slog.Default() != set {
		t.Fatalf("Setup(xml): %v, and the logger changed: %v", err, slog.Default() != set)
	}
}
