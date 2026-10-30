//go:build unix

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// lock takes the lock of dataDir, which its coordinator holds while it
// runs, and returns the open lock file; closing it, or the process ending,
// releases the lock. A second coordinator on the directory, whatever its
// -addr, would truncate the log under the first and remove its compaction.
// The file holds the holder's pid, for the error a second one reports. The
// Python coordinator takes the same lock, so neither runs beside the other.
func lock(dataDir string) (*os.File, error) {
	f, err := os.OpenFile(filepath.Join(dataDir, "coordinator.lock"), os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		b, _ := os.ReadFile(f.Name())
		_ = f.Close()
		holder := strings.TrimSpace(string(b))
		if holder == "" {
			holder = "unknown"
		}
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("-data-dir %s is in use by another coordinator (pid %s)", dataDir, holder)
		}
		return nil, err
	}
	if err := f.Truncate(0); err != nil {
		_ = f.Close()
		return nil, err
	}
	if _, err := f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}
