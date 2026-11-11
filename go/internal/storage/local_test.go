package storage

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"
)

// Directories that MkdirAll creates are writable by everyone, so the host
// user can delete output the containers wrote (ADR 0016); ones that exist
// keep their mode.
func TestLocalMkdirAllMode(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "a", "b")
	if err := (Local{}).MkdirAll(t.Context(), dir); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{filepath.Join(root, "a"), dir} {
		if info, err := os.Stat(d); err != nil || info.Mode().Perm() != 0o777 {
			t.Errorf("%s: %v, %v; want mode 0777", d, info.Mode(), err)
		}
	}
	if info, _ := os.Stat(root); info.Mode().Perm() != 0o700 {
		t.Errorf("existing directory's mode changed to %v", info.Mode())
	}
	if err := (Local{}).MkdirAll(t.Context(), dir); err != nil {
		t.Errorf("existing directory: %v", err)
	}
	file := filepath.Join(root, "f")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := (Local{}).MkdirAll(t.Context(), filepath.Join(file, "x")); err == nil {
		t.Error("a path through a file: no error")
	}
}

// A directory name is not a pattern: Escape lets List find the files of one
// named with glob characters.
func TestEscape(t *testing.T) {
	dir := filepath.Join(t.TempDir(), `out[ab]*?\`)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	part := filepath.Join(dir, "part-r-00000")
	if err := os.WriteFile(part, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := List(t.Context(), Join(Escape(dir), "part-r-*"))
	if err != nil || len(got) != 1 || got[0] != part {
		t.Fatalf("List = %q, %v; want %q", got, err, part)
	}
	if got, _ := List(t.Context(), Join(dir, "part-r-*")); len(got) != 0 {
		t.Fatalf("without Escape the directory still matched itself: %q", got)
	}
}

// A file created in a directory after os.RemoveAll has read its last
// entries, as a losing backup attempt does in a finished job's TempDir, does
// not leave the directory behind (#345).
func TestLocalDeleteAllLateFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "_temporary")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	calls := 0
	removeAll = func(path string) error {
		calls++
		if calls == 1 { // the late file appears just before the directory goes
			if err := os.WriteFile(filepath.Join(path, "part-r-00001.attempt-1"), nil, 0o644); err != nil {
				t.Fatal(err)
			}
			return &fs.PathError{Op: "unlinkat", Path: path, Err: syscall.ENOTEMPTY}
		}
		return os.RemoveAll(path)
	}
	t.Cleanup(func() { removeAll = os.RemoveAll })
	if err := (Local{}).DeleteAll(t.Context(), dir); err != nil {
		t.Fatalf("DeleteAll: %v", err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("%s is still there after DeleteAll: %v", dir, err)
	}
}

// DeleteAll removes a directory that others keep creating files in, as
// running attempts do, until it is gone; then their creates fail.
func TestLocalDeleteAllConcurrentCreates(t *testing.T) {
	for range 20 {
		dir := filepath.Join(t.TempDir(), "_temporary")
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		for w := range 4 {
			wg.Go(func() {
				for i := range 200 {
					f, err := os.Create(filepath.Join(dir, fmt.Sprintf("w%d-%d", w, i)))
					if err != nil {
						return // dir is gone
					}
					_ = f.Close()
				}
			})
		}
		err := (Local{}).DeleteAll(t.Context(), dir)
		wg.Wait()
		if err != nil {
			t.Fatalf("DeleteAll: %v", err)
		}
		if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("%s is still there after DeleteAll: %v", dir, err)
		}
	}
}

// DeleteAll outlasts writers that win its race many times in a row, as busy
// ones do on a loaded machine: a fixed count of tight retries ran out there
// (TestLocalDeleteAllConcurrentCreates failed in CI).
func TestLocalDeleteAllOutlastsBusyWriters(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "_temporary")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	calls := 0
	removeAll = func(path string) error {
		calls++
		if calls <= 150 {
			return &fs.PathError{Op: "unlinkat", Path: path, Err: syscall.ENOTEMPTY}
		}
		return os.RemoveAll(path)
	}
	t.Cleanup(func() { removeAll = os.RemoveAll })
	if err := (Local{}).DeleteAll(t.Context(), dir); err != nil {
		t.Fatalf("DeleteAll after %d tries: %v", calls, err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("%s is still there after DeleteAll: %v", dir, err)
	}
}

// A directory that stays busy past deleteFor gives up with the last error.
func TestLocalDeleteAllGivesUp(t *testing.T) {
	removeAll = func(path string) error {
		return &fs.PathError{Op: "unlinkat", Path: path, Err: syscall.ENOTEMPTY}
	}
	saved := deleteFor
	deleteFor = 20 * time.Millisecond
	t.Cleanup(func() { removeAll, deleteFor = os.RemoveAll, saved })
	if err := (Local{}).DeleteAll(t.Context(), t.TempDir()); !errors.Is(err, syscall.ENOTEMPTY) {
		t.Fatalf("DeleteAll = %v; want ENOTEMPTY", err)
	}
}

// A cancelled caller stops DeleteAll's retries instead of waiting out all of
// them.
func TestLocalDeleteAllStopsWhenCancelled(t *testing.T) {
	dir := t.TempDir()
	calls := 0
	removeAll = func(path string) error {
		calls++
		return &fs.PathError{Op: "unlinkat", Path: path, Err: syscall.ENOTEMPTY}
	}
	t.Cleanup(func() { removeAll = os.RemoveAll })
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := (Local{}).DeleteAll(ctx, dir)
	if !errors.Is(err, context.Canceled) || !errors.Is(err, syscall.ENOTEMPTY) {
		t.Fatalf("DeleteAll = %v; want both context.Canceled and ENOTEMPTY", err)
	}
	if calls != 1 {
		t.Fatalf("removeAll called %d times after cancellation; want 1", calls)
	}
}
