// Package local runs a job in one process, one task at a time (§4.7).
//
// Its output depends only on the job and its input, never on M or on timing:
// splits run in input order, and equal keys keep that order through the
// sort. That makes it the oracle the distributed runs are checked against.
package local

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/exios66/mapreduce-reloaded/go/internal/formats"
	"github.com/exios66/mapreduce-reloaded/go/internal/split"
	"github.com/exios66/mapreduce-reloaded/go/internal/task"
	"github.com/exios66/mapreduce-reloaded/go/pkg/api"
)

// Run executes spec. Intermediate files go in workDir, which the caller
// removes. Output file r is written to <Output>/part-r-<r>, r in at least five digits. Run returns the
// job's counters.
func Run(spec api.JobSpec, workDir string) (map[string]int64, error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	in, err := formats.Input(spec.InputFormat)
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	splits, err := split.Plan(ctx, in, spec.Inputs, spec.SplitSize)
	if err != nil {
		return nil, err
	}
	if len(splits) == 0 {
		return nil, errors.New("local: the input is empty")
	}
	if err := task.CheckOutput(ctx, spec.Output); err != nil {
		return nil, err
	}
	if err := task.Setup(ctx, spec.Output); err != nil {
		return nil, err
	}

	counts := map[string]int64{}
	add := func(c map[string]int64) {
		for name, n := range c {
			counts[name] += n
		}
	}
	for i, s := range splits {
		id := api.TaskID{Kind: api.MapTask, Index: uint32(i)}
		_, c, err := task.Map(ctx, spec, id, s, workDir, nil, slog.Default().With("task", id))
		if err != nil {
			return nil, fmt.Errorf("map task %d: %w", i, err)
		}
		add(c)
	}
	for r := range spec.R {
		c, err := reduce(spec, r, len(splits), workDir)
		if err != nil {
			return nil, fmt.Errorf("reduce task %d: %w", r, err)
		}
		add(c)
	}
	return counts, task.Cleanup(ctx, spec.Output)
}

// reduce runs reduce task r over the outputs of all m map tasks in dir.
func reduce(spec api.JobSpec, r uint32, m int, dir string) (map[string]int64, error) {
	paths := make([]string, m)
	for i := range paths {
		paths[i] = task.MapOutput(dir, i, int(r))
	}
	sources, closeSources, err := task.OpenSources(paths)
	if err != nil {
		return nil, err
	}
	id := api.TaskID{Kind: api.ReduceTask, Index: r}
	counts, err := task.Reduce(context.Background(), spec, id, sources, nil, slog.Default().With("task", id))
	return counts, errors.Join(err, closeSources())
}
