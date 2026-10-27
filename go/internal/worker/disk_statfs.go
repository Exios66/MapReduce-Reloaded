//go:build linux || darwin

package worker

import "syscall"

// freeBytes returns the bytes free to unprivileged users on dir's file
// system.
func freeBytes(dir string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return st.Bavail * uint64(st.Bsize), nil //nolint:unconvert // Bsize is int64 on Linux
}
