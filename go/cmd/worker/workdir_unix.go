//go:build linux || darwin

package main

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"syscall"
)

// A worker without -work-dir keeps its map output in a temporary directory,
// which it removes when it exits cleanly. A worker that is killed cannot,
// and its restarted process makes a new directory, so the old one's map
// output, which nothing will read again, stays until the disk fills. Each
// worker therefore holds a lock on a file in its directory while it runs,
// and a starting worker deletes every other worker directory whose lock it
// can take: their workers have exited. A directory without the lock file
// is kept; it may belong to a worker that is still starting.

const (
	workDirPattern = "mr-worker-"
	lockName       = ".lock"
)

// tempWorkDir makes this worker's work directory under parent (empty: the
// system's temporary directory), after deleting those of dead workers. The
// returned release removes the directory and drops the lock.
func tempWorkDir(parent string) (string, func() error, error) {
	if parent == "" {
		parent = os.TempDir()
	}
	reapDead(parent)
	dir, err := os.MkdirTemp(parent, workDirPattern)
	if err != nil {
		return "", nil, err
	}
	f, err := lock(filepath.Join(dir, lockName))
	if err == nil && f == nil {
		err = errors.New("locking a new work directory: already locked")
	}
	if err != nil {
		return "", nil, errors.Join(err, os.RemoveAll(dir))
	}
	return dir, func() error { return errors.Join(os.RemoveAll(dir), f.Close()) }, nil
}

// lock opens path and takes an exclusive lock on it, which the kernel drops
// when the process exits. It returns a nil file and no error if another
// open file holds the lock.
func lock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, nil
		}
		return nil, err
	}
	return f, nil
}

// reapDead deletes the work directories under parent whose workers have
// exited. Errors are logged, not returned: a directory left behind is
// tried again by the next worker to start.
func reapDead(parent string) {
	dirs, _ := filepath.Glob(filepath.Join(parent, workDirPattern+"*"))
	for _, dir := range dirs {
		path := filepath.Join(dir, lockName)
		if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() {
			continue
		}
		f, err := lock(path)
		if err != nil || f == nil {
			continue
		}
		err = os.RemoveAll(dir)
		_ = f.Close()
		if err != nil {
			slog.Warn("deleting a dead worker's work directory", "dir", dir, "err", err)
			continue
		}
		slog.Info("deleted a dead worker's work directory", "dir", dir)
	}
}
