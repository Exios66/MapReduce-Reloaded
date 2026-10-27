// Package split divides a job's input into the splits of its map tasks
// (§3.1) and picks default task counts (§3.5). Local mode and the
// coordinator share it, so both cut the same input the same way.
package split

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math"
	"math/bits"
	"slices"

	"github.com/exios66/mapreduce-reloaded/go/internal/formats"
	"github.com/exios66/mapreduce-reloaded/go/internal/storage"
	"github.com/exios66/mapreduce-reloaded/go/pkg/api"
)

// DefaultSize is the split size used when a job sets none. The paper uses
// 16 to 64 MB (§3.1).
const DefaultSize = 64 << 20

// ReduceTasksPerWorker sets the default R: the paper makes R "a small
// multiple of the number of worker machines" (§3.5).
const ReduceTasksPerWorker = 2

// Plan expands patterns and cuts every file into splits of about size bytes;
// 0 means DefaultSize. Splits come in file order, and M is their number.
func Plan(ctx context.Context, in formats.InputFormat, patterns []string, size uint64) ([]formats.Split, error) {
	files, err := Expand(ctx, patterns)
	if err != nil {
		return nil, err
	}
	if size == 0 {
		size = DefaultSize
	}
	var splits []formats.Split
	for _, f := range files {
		s, err := in.Splits(ctx, f, int64(min(size, math.MaxInt64)))
		if err != nil {
			return nil, err
		}
		if splits = append(splits, s...); len(splits) > api.MaxMapTasks {
			return nil, errTooManySplits(len(splits))
		}
	}
	return splits, nil
}

// errTooManySplits reports a plan past api.MaxMapTasks, which the planners
// stop at instead of building the rest of the list.
func errTooManySplits(n int) error {
	return fmt.Errorf("split: the input splits into more than %d map tasks (%d so far); raise the split size", api.MaxMapTasks, n)
}

// ForSpec returns the splits of spec's input: PlanM with spec.M if set,
// otherwise Plan with spec.SplitSize.
func ForSpec(ctx context.Context, in formats.InputFormat, spec api.JobSpec) ([]formats.Split, error) {
	if spec.M > 0 {
		return PlanM(ctx, in, spec.Inputs, spec.M)
	}
	return Plan(ctx, in, spec.Inputs, spec.SplitSize)
}

// PlanM expands patterns and cuts the input into m splits (ADR 0016), shared
// out among the non-empty files by Allocate. A file whose format
// cannot be cut that finely, a small file or a kv file, gets fewer; the
// caller reports the number of splits returned as the job's M.
func PlanM(ctx context.Context, in formats.InputFormat, patterns []string, m int) ([]formats.Split, error) {
	if m < 1 {
		return nil, errors.New("split: m must be at least 1")
	}
	all, err := Expand(ctx, patterns)
	if err != nil {
		return nil, err
	}
	var files []string
	var sizes []int64
	for _, f := range all {
		size, err := storage.Size(ctx, f)
		if err != nil {
			return nil, err
		}
		if size > 0 {
			files, sizes = append(files, f), append(sizes, size)
		}
	}
	var splits []formats.Split
	for i, k := range Allocate(sizes, m) {
		size := (sizes[i] + int64(k) - 1) / int64(k)
		s, err := in.Splits(ctx, files[i], size)
		if err != nil {
			return nil, err
		}
		if splits = append(splits, s...); len(splits) > api.MaxMapTasks {
			return nil, errTooManySplits(len(splits))
		}
	}
	return splits, nil
}

// Allocate divides m splits among files of the given positive sizes, in
// proportion to size by largest remainder (ties to the earlier file), then
// gives a file left with none one split, taken from the file with the most.
// The result sums to max(m, len(sizes)), or is empty if sizes is.
func Allocate(sizes []int64, m int) []int {
	alloc := make([]int, len(sizes))
	if len(sizes) == 0 {
		return alloc
	}
	if m <= len(sizes) {
		for i := range alloc {
			alloc[i] = 1
		}
		return alloc
	}
	var total uint64
	for _, s := range sizes {
		total += uint64(s)
	}
	// quota_i = m * size_i / total: the whole part goes to the file, and
	// the remainders decide who gets the leftovers.
	rems := make([]uint64, len(sizes))
	given := 0
	for i, s := range sizes {
		q, r := bits128Div(uint64(m), uint64(s), total)
		alloc[i], rems[i] = int(q), r
		given += int(q)
	}
	order := make([]int, len(sizes))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int { return cmp.Compare(rems[b], rems[a]) })
	for _, i := range order[:m-given] {
		alloc[i]++
	}
	for i := range alloc {
		if alloc[i] == 0 {
			alloc[i] = 1
			alloc[slices.Index(alloc, slices.Max(alloc))]--
		}
	}
	return alloc
}

// bits128Div returns a*b/c and a*b%c without overflow; a*b/c must fit in
// 64 bits, which holds when b <= c.
func bits128Div(a, b, c uint64) (q, r uint64) {
	hi, lo := bits.Mul64(a, b)
	return bits.Div64(hi, lo, c)
}

// SizeFor returns the split size that cuts the input into at most m splits
// per file, and about m in total.
func SizeFor(ctx context.Context, patterns []string, m int) (uint64, error) {
	if m < 1 {
		return 0, errors.New("split: m must be at least 1")
	}
	files, err := Expand(ctx, patterns)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, f := range files {
		size, err := storage.Size(ctx, f)
		if err != nil {
			return 0, err
		}
		total += size
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
func Expand(ctx context.Context, patterns []string) ([]string, error) {
	var files []string
	for _, p := range patterns {
		matches, err := storage.List(ctx, p)
		if err != nil {
			return nil, fmt.Errorf("split: %q: %w", p, err)
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("split: no input matches %q", p)
		}
		files = append(files, matches...)
	}
	return files, nil
}
