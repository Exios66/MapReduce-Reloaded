//go:build !(linux || darwin)

package main

import "os"

// tempWorkDir makes this worker's work directory under parent (empty: the
// system's temporary directory). Without file locks, the directories of
// dead workers are not found and deleted, as they are on Linux and macOS.
func tempWorkDir(parent string) (string, func() error, error) {
	dir, err := os.MkdirTemp(parent, "mr-worker-")
	if err != nil {
		return "", nil, err
	}
	return dir, func() error { return os.RemoveAll(dir) }, nil
}
