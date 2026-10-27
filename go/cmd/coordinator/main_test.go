package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunRejectsNegativeKeepJobs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wal")
	err := run("", "", path, true, -1)
	if err == nil || !strings.Contains(err.Error(), "-keep-jobs is -1") {
		t.Fatalf("got %v, want a keep-jobs error", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("invalid configuration created the data directory: %v", err)
	}
}
