//go:build !(linux || darwin)

package worker

import "errors"

// freeBytes returns zero and errors.ErrUnsupported on this platform,
// causing workers to skip waiting for disk space.
func freeBytes(string) (uint64, error) { return 0, errors.ErrUnsupported }
