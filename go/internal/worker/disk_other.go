//go:build !(linux || darwin)

package worker

import "errors"

// freeBytes cannot read free space here, so workers never wait for it.
func freeBytes(string) (uint64, error) { return 0, errors.ErrUnsupported }
