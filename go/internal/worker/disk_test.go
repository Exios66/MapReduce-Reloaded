package worker

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// prune deletes only job directories that are not active and were not
// written since the heartbeat was sent.
func TestPrune(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().Add(-time.Minute)
	for _, name := range []string{"job-1", "job-2", "job-3", "notes"} {
		if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name, "m-0-0"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if name != "job-3" { // job-3 was written after the heartbeat
			if err := os.Chtimes(filepath.Join(dir, name), old, old); err != nil {
				t.Fatal(err)
			}
		}
	}
	w := New(nil, dir, nil)
	w.prune([]string{"job-2"}, time.Now().Add(-time.Second))
	for w.pruning.Load() {
		time.Sleep(time.Millisecond)
	}
	for name, want := range map[string]bool{"job-1": false, "job-2": true, "job-3": true, "notes": true} {
		if _, err := os.Stat(filepath.Join(dir, name)); (err == nil) != want {
			t.Errorf("%s: kept = %v, want %v", name, err == nil, want)
		}
	}
	if w.stats.prunedJobs.Load() != 1 || w.stats.prunedBytes.Load() != 1 {
		t.Errorf("pruned %d jobs, %d bytes; want 1, 1", w.stats.prunedJobs.Load(), w.stats.prunedBytes.Load())
	}
}
