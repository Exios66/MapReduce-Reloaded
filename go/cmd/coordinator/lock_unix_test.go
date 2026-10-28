//go:build unix

package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// A second coordinator on a -data-dir in use is refused, whatever its
// -addr, before it touches the log. The lock is released when the first
// stops, and when recovery fails.
func TestRunLocksDataDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "wal")
	o := options{addr: "127.0.0.1:0", dataDir: dir}
	_, _, stop := serve(t, o)
	logPath := filepath.Join(dir, "coordinator.log")
	before, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("-data-dir %s is in use by another coordinator (pid %d)", dir, os.Getpid())
	if err := run(t.Context(), o, nil); err == nil || err.Error() != want {
		t.Fatalf("second run: %v; want %q", err, want)
	}
	if after, _ := os.ReadFile(logPath); !bytes.Equal(after, before) {
		t.Error("the second coordinator changed the log")
	}
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	_, _, stop = serve(t, o)
	if err := stop(); err != nil {
		t.Fatal(err)
	}

	// Recovery fails on a log that is a directory, and still unlocks.
	bad := options{addr: "127.0.0.1:0", dataDir: filepath.Join(t.TempDir(), "bad")}
	if err := os.MkdirAll(filepath.Join(bad.dataDir, "coordinator.log"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), bad, nil); err == nil {
		t.Fatal("run with a directory for a log: no error")
	}
	f, err := lock(bad.dataDir)
	if err != nil {
		t.Fatalf("lock after a failed recovery: %v", err)
	}
	_ = f.Close()
}

// A lock held by a process that wrote no pid says so, and a lock file
// that cannot be opened is reported.
func TestLockErrors(t *testing.T) {
	dir := t.TempDir()
	f, err := os.Create(filepath.Join(dir, "coordinator.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	if _, err := lock(dir); err == nil || !strings.HasSuffix(err.Error(), "(pid unknown)") {
		t.Errorf("lock held without a pid: %v", err)
	}
	if _, err := lock(filepath.Join(dir, "missing")); err == nil {
		t.Error("lock in a missing directory: no error")
	}
}
