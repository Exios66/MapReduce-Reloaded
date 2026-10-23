// Package split divides a job's input into the splits of its map tasks
// (§3.1) and picks default task counts (§3.5). Local mode and the
// coordinator share it, so both cut the same input the same way.
package split

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"

	"github.com/exios66/mapreduce-reloaded/go/internal/formats"
)

// DefaultSize is the split size used when a job sets none. The paper uses
// 16 to 64 MB (§3.1).
const DefaultSize = 64 << 20

// ReduceTasksPerWorker sets the default R: the paper makes R "a small
// multiple of the number of worker machines" (§3.5).
const ReduceTasksPerWorker = 2

// Plan expands patterns and cuts every file into splits of about size bytes;
// 0 means DefaultSize. Splits come in file order, and M is their number.
func Plan(in formats.InputFormat, patterns []string, size uint64) ([]formats.Split, error) {
	files, err := Expand(patterns)
	if err != nil {
		return nil, err
	}
	if size == 0 {
		size = DefaultSize
	}
	var splits []formats.Split
	for _, f := range files {
		s, err := in.Splits(f, int64(min(size, math.MaxInt64)))
		if err != nil {
			return nil, err
		}
		splits = append(splits, s...)
	}
	return splits, nil
}

// SizeFor returns the split size that cuts the input into at most m splits
// per file, and about m in total.
func SizeFor(patterns []string, m int) (uint64, error) {
	if m < 1 {
		return 0, errors.New("split: m must be at least 1")
	}
	files, err := Expand(patterns)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, f := range files {
		info, err := os.Stat(f)
		if err != nil {
			return 0, err
		}
		total += info.Size()
	}
	return uint64(max((total+int64(m)-1)/int64(m), 1)), nil
}

// DefaultR returns the default number of reduce tasks for a cluster of the
// given number of workers.
func DefaultR(workers int) uint32 {
	return uint32(max(workers, 1) * ReduceTasksPerWorker)
}

// Expand resolves file names and glob patterns to files, sorted within each
// pattern. A pattern that matches nothing is an error.
func Expand(patterns []string) ([]string, error) {
	var files []string
	for _, p := range patterns {
		matches, err := filepath.Glob(p)
		if err != nil {
			return nil, fmt.Errorf("split: %q: %w", p, err)
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("split: no input matches %q", p)
		}
		slices.Sort(matches)
		files = append(files, matches...)
	}
	return files, nil
}
