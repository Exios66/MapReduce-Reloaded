package logs_test

import (
	"bytes"
	"encoding/json"
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
