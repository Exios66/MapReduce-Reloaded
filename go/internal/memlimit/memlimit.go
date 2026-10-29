// Package memlimit reads the memory limit of the container the process runs
// in, so the Go runtime can keep its heap under it instead of the kernel
// killing the process (#47).
package memlimit

import (
	"os"
	"strconv"
	"strings"
)

// unlimited is cgroup v1's "no limit": a huge, page-aligned number.
const unlimited = 1 << 62

// FromCgroup returns the container's memory limit in bytes, or 0 if it is
// unknown or unlimited. It reads cgroup v2's memory.max, then v1's
// memory/memory.limit_in_bytes.
func FromCgroup() int64 {
	for _, path := range []string{
		"/sys/fs/cgroup/memory.max",
		"/sys/fs/cgroup/memory/memory.limit_in_bytes",
	} {
		if limit, ok := read(path); ok {
			return limit
		}
	}
	return 0
}

// read returns the limit in path, and whether path held a finite one.
func read(path string) (int64, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	text := strings.TrimSpace(string(data))
	if text == "max" { // cgroup v2's "no limit"
		return 0, false
	}
	limit, err := strconv.ParseInt(text, 10, 64)
	if err != nil || limit <= 0 || limit >= unlimited {
		return 0, false
	}
	return limit, true
}
