//go:build linux || darwin

package main

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// A starting worker deletes the work directory of a worker that exited
// without removing it, and keeps those of live or starting workers.
func TestTempWorkDirReapsDeadWorkers(t *testing.T) {
	parent := t.TempDir()
	mk := func(name string, lockFile bool) string {
		dir := filepath.Join(parent, name)
		if err := os.MkdirAll(filepath.Join(dir, "job-1"), 0o755); err != nil {
			t.Fatal(err)
		}
		if lockFile {
			if err := os.WriteFile(filepath.Join(dir, lockName), nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}
	dead := mk("mr-worker-dead", true)
	starting := mk("mr-worker-starting", false)
	other := mk("not-a-worker", true)
	live, release, err := tempWorkDir(parent)
	if err != nil {
		t.Fatal(err)
	}

	// A second worker sees the first one's lock held, and the dead one gone.
	second, releaseSecond, err := tempWorkDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	for dir, want := range map[string]bool{dead: false, starting: true, other: true, live: true, second: true} {
		if _, err := os.Stat(dir); (err == nil) != want {
			t.Errorf("%s: exists = %v, want %v", filepath.Base(dir), err == nil, want)
		}
	}

	if err := release(); err != nil {
		t.Fatal(err)
	}
	if err := releaseSecond(); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{live, second} {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("%s not removed on release: %v", filepath.Base(dir), err)
		}
	}
}

// Workers that start at the same moment each get a work directory that
// survives the others' sweeps: a sweep may take a new directory's lock
// between its creation and the owner's locking it, and the owner then makes
// another.
func TestTempWorkDirConcurrentStarts(t *testing.T) {
	parent := t.TempDir()
	var wg sync.WaitGroup
	errs := make(chan error, 16*50*2)
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				dir, release, err := tempWorkDir(parent)
				if err != nil {
					errs <- err
					return
				}
				if _, err := os.Stat(dir); err != nil {
					errs <- err
				}
				if err := release(); err != nil {
					errs <- err
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
