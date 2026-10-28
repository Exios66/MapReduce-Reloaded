package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pb "github.com/exios66/mapreduce-reloaded/go/gen/mapreduce/v1"
	"google.golang.org/grpc"
)

// idleCoordinator answers the paused RequestTask polls that waitForDisk
// makes with an empty response; an unpaused poll could be handed a task the
// worker would not run, so it panics, as does any other call.
type idleCoordinator struct{ pb.CoordinatorServiceClient }

func (idleCoordinator) RequestTask(_ context.Context, req *pb.RequestTaskRequest, _ ...grpc.CallOption) (*pb.RequestTaskResponse, error) {
	if !req.GetPaused() {
		panic("waitForDisk polled the coordinator without pausing")
	}
	return &pb.RequestTaskResponse{}, nil
}

// prune deletes only job directories that are not active and were not
// written since the heartbeat was sent.
func TestPrune(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().Add(-time.Minute)
	for _, name := range []string{"job-1", "job-mh3x9k-4", "job-2", "job-3", "notes"} {
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
	for name, want := range map[string]bool{"job-1": false, "job-mh3x9k-4": false, "job-2": true, "job-3": true, "notes": true} {
		if _, err := os.Stat(filepath.Join(dir, name)); (err == nil) != want {
			t.Errorf("%s: kept = %v, want %v", name, err == nil, want)
		}
	}
	if w.stats.prunedJobs.Load() != 2 || w.stats.prunedBytes.Load() != 2 {
		t.Errorf("pruned %d jobs, %d bytes; want 2, 2", w.stats.prunedJobs.Load(), w.stats.prunedBytes.Load())
	}
}

func TestWaitForDiskBoundary(t *testing.T) {
	for _, tc := range []struct {
		name          string
		free, minimum uint64
		err           error
	}{
		{name: "disabled", free: 0, minimum: 0},
		{name: "exactly enough", free: 100, minimum: 100},
		{name: "above minimum", free: 101, minimum: 100},
		{name: "unreadable", minimum: 100, err: errors.New("statfs unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := New(nil, t.TempDir(), nil)
			w.MinFreeDisk = tc.minimum
			calls := 0
			w.freeBytes = func(dir string) (uint64, error) {
				calls++
				if dir != w.workDir {
					t.Errorf("checked %q, want %q", dir, w.workDir)
				}
				return tc.free, tc.err
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := w.waitForDisk(ctx); err != nil {
				t.Fatal(err)
			}
			if calls != 1 || w.stats.diskWaits.Load() != 0 || w.stats.diskFree.Load() != tc.free {
				t.Fatalf("calls=%d, waits=%d, free=%d", calls, w.stats.diskWaits.Load(), w.stats.diskFree.Load())
			}
		})
	}
}

func TestWaitForDiskRecovery(t *testing.T) {
	t.Cleanup(SetDiskPoll(time.Millisecond))
	w := New(idleCoordinator{}, t.TempDir(), nil)
	w.MinFreeDisk = 100
	// Several low-space polls are a single wait episode. A later episode
	// increments the counter again; the free-space gauge follows the last poll.
	readings := []uint64{0, 99, 99, 100, 99, 101}
	calls := 0
	w.freeBytes = func(string) (uint64, error) {
		if calls >= len(readings) {
			return 0, errors.New("unexpected extra poll")
		}
		free := readings[calls]
		calls++
		return free, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for episode, wantFree := range []uint64{100, 101} {
		if err := w.waitForDisk(ctx); err != nil {
			t.Fatal(err)
		}
		if w.stats.diskWaits.Load() != uint64(episode+1) || w.stats.diskFree.Load() != wantFree {
			t.Fatalf("episode %d: waits=%d, free=%d", episode, w.stats.diskWaits.Load(), w.stats.diskFree.Load())
		}
	}
	if calls != len(readings) {
		t.Fatalf("polled %d times, want %d", calls, len(readings))
	}
	metrics := string(w.stats.metrics())
	for _, want := range []string{
		"# TYPE mr_worker_disk_free_bytes gauge\n", "mr_worker_disk_free_bytes 101\n",
		"# TYPE mr_worker_disk_waits_total counter\n", "mr_worker_disk_waits_total 2\n",
	} {
		if !strings.Contains(metrics, want) {
			t.Errorf("metrics missing %q", want)
		}
	}
}

func TestWaitForDiskCancellation(t *testing.T) {
	w := New(idleCoordinator{}, t.TempDir(), nil)
	w.MinFreeDisk = 100
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.freeBytes = func(string) (uint64, error) { cancel(); return 99, nil }
	if err := w.waitForDisk(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
	if w.stats.diskWaits.Load() != 1 || w.stats.diskFree.Load() != 99 {
		t.Fatalf("waits=%d, free=%d", w.stats.diskWaits.Load(), w.stats.diskFree.Load())
	}
}

// Cleanup must preserve unrelated names, ordinary files, symlinks, active
// jobs, and directories whose timestamp equals the heartbeat's timestamp.
func TestPruneSafeguards(t *testing.T) {
	dir := t.TempDir()
	sent := time.Unix(1_700_000_000, 0)
	for _, name := range []string{"job-1", "job-2", "job-3", "job-6", "job-x", "job-4-extra"} {
		path := filepath.Join(dir, name)
		if err := os.Mkdir(path, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "map"), []byte("abc"), 0o644); err != nil {
			t.Fatal(err)
		}
		at := sent.Add(-time.Minute)
		switch name {
		case "job-3":
			at = sent
		case "job-6": // inside pruneSlack: it may still be written
			at = sent.Add(-pruneSlack / 2)
		}
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "job-4"), []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	external := t.TempDir()
	if err := os.WriteFile(filepath.Join(external, "keep"), []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(dir, "job-5")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	w := New(nil, dir, nil)
	w.prune([]string{"job-2"}, sent)
	waitForPrune(t, w)
	for _, name := range []string{"job-2", "job-3", "job-6", "job-x", "job-4-extra", "job-4", "job-5"} {
		if _, err := os.Lstat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s removed: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "job-1")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("ended job remains: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(external, "keep")); err != nil || string(data) != "outside" {
		t.Fatalf("external output changed: %q, %v", data, err)
	}
	if w.stats.prunedJobs.Load() != 1 || w.stats.prunedBytes.Load() != 3 {
		t.Fatal("incorrect prune counters")
	}
	// Repeating a heartbeat cannot count the same output twice.
	w.prune([]string{"job-2"}, sent)
	waitForPrune(t, w)
	if w.stats.prunedJobs.Load() != 1 || w.stats.prunedBytes.Load() != 3 {
		t.Fatal("prune was counted twice")
	}
	for _, want := range []string{"mr_worker_pruned_jobs_total 1\n", "mr_worker_pruned_bytes_total 3\n"} {
		if !strings.Contains(string(w.stats.metrics()), want) {
			t.Errorf("metrics missing %q", want)
		}
	}
}

func waitForPrune(t *testing.T, w *Worker) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for w.pruning.Load() {
		if time.Now().After(deadline) {
			t.Fatal("prune did not finish")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestPruneKeepsDebugOutput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "job-1")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	w := New(nil, dir, nil)
	w.KeepEndedJobs = true
	w.prune(nil, time.Now().Add(time.Hour))
	waitForPrune(t, w)
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	if w.stats.prunedJobs.Load() != 0 || w.stats.prunedBytes.Load() != 0 {
		t.Fatal("debug output counted as pruned")
	}
}

func TestDirSize(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{"map": "abc", "nested/spill": "12345", "empty": ""} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(dir, "map"), filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if got := dirSize(dir); got != 8 {
		t.Fatalf("dirSize=%d, want 8", got)
	}
	if got := dirSize(filepath.Join(dir, "missing")); got != 0 {
		t.Fatalf("missing dir size=%d", got)
	}
}
