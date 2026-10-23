// Package local runs a job in one process, one task at a time (§4.7).
//
// Its output depends only on the job and its input, never on M or on timing:
// splits run in input order, and equal keys keep that order through the
// sort. That makes it the oracle the distributed runs are checked against.
package local

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/exios66/mapreduce-reloaded/go/internal/combine"
	"github.com/exios66/mapreduce-reloaded/go/internal/extsort"
	"github.com/exios66/mapreduce-reloaded/go/internal/formats"
	"github.com/exios66/mapreduce-reloaded/go/internal/formats/kv"
	"github.com/exios66/mapreduce-reloaded/go/internal/split"
	"github.com/exios66/mapreduce-reloaded/go/pkg/api"
)

// Framework counters, reported alongside the user's.
const (
	MapInputRecords     = "map.input.records"
	MapOutputRecords    = "map.output.records"
	ReduceOutputRecords = "reduce.output.records"
)

// Run executes spec. Intermediate files go in workDir, which the caller
// removes. Output file r is written to <Output>/part-r-<r>. Run returns the
// job's counters.
func Run(spec api.JobSpec, workDir string) (map[string]int64, error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	in, err := formats.Input(spec.InputFormat)
	if err != nil {
		return nil, err
	}
	out, err := formats.Output(spec.OutputFormat)
	if err != nil {
		return nil, err
	}
	splits, err := split.Plan(in, spec.Inputs, spec.SplitSize)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(spec.Output, 0o755); err != nil {
		return nil, err
	}

	j := &job{spec: spec, workDir: workDir, counts: map[string]int64{}}
	for i, s := range splits {
		if err := j.runMap(i, in, s); err != nil {
			return nil, fmt.Errorf("map task %d: %w", i, err)
		}
	}
	for r := range spec.R {
		if err := j.runReduce(r, len(splits), out); err != nil {
			return nil, fmt.Errorf("reduce task %d: %w", r, err)
		}
	}
	return j.counts, nil
}

type job struct {
	spec    api.JobSpec
	workDir string
	counts  map[string]int64
}

// runMap runs map task i: it maps one split and writes one sorted, combined
// file per reduce task.
func (j *job) runMap(i int, in formats.InputFormat, split formats.Split) (err error) {
	ctx := j.context(api.MapTask, i)
	p := j.spec.Program
	partitioner := p.Partitioner
	if partitioner == nil {
		partitioner = api.HashPartitioner
	}

	sorters := make([]*extsort.Sorter, j.spec.R)
	for r := range sorters {
		cfg := extsort.Config{Dir: j.workDir, Budget: extsort.DefaultBudget / len(sorters)}
		if p.Combiner != nil {
			cfg.Combine = combine.Func(ctx, p.Combiner)
		}
		sorters[r] = extsort.New(cfg)
		defer closeJoin(&err, sorters[r])
	}

	records, err := in.Open(split)
	if err != nil {
		return err
	}
	defer closeJoin(&err, records)
	emit := emitter(func(key, value []byte) error {
		ctx.Count(MapOutputRecords, 1)
		return sorters[partitioner.Partition(key, j.spec.R)].Add(key, value)
	})
	for records.Next() {
		ctx.Count(MapInputRecords, 1)
		if err := p.Mapper.Map(ctx, records.Key(), records.Value(), emit); err != nil {
			return err
		}
	}
	if err := records.Err(); err != nil {
		return err
	}

	for r, s := range sorters {
		sorted, err := s.Sorted()
		if err != nil {
			return err
		}
		if err := writeKV(j.mapOutput(i, r), sorted); err != nil {
			return err
		}
	}
	return nil
}

// runReduce runs reduce task r over the outputs of all m map tasks.
func (j *job) runReduce(r uint32, m int, out formats.OutputFormat) (err error) {
	ctx := j.context(api.ReduceTask, int(r))
	runs := make([]extsort.Records, m)
	for i := range m {
		var f *os.File
		if f, err = os.Open(j.mapOutput(i, int(r))); err != nil {
			return err
		}
		defer closeJoin(&err, f)
		runs[i] = kv.NewReader(f)
	}

	w, err := out.Create(filepath.Join(j.spec.Output, fmt.Sprintf("part-r-%05d", r)))
	if err != nil {
		return err
	}
	defer closeJoin(&err, w)
	emit := emitter(func(key, value []byte) error {
		ctx.Count(ReduceOutputRecords, 1)
		return w.Write(key, value)
	})

	merged := extsort.Merge(runs...) // in map task order, so equal keys stay in input order
	for key, values := range extsort.Groups(merged) {
		if err := j.spec.Program.Reducer.Reduce(ctx, key, values, emit); err != nil {
			return err
		}
	}
	return merged.Err()
}

// closeJoin closes c, adding any error to *err. Use it with defer.
func closeJoin(err *error, c io.Closer) { *err = errors.Join(*err, c.Close()) }

func (j *job) mapOutput(i, r int) string {
	return filepath.Join(j.workDir, fmt.Sprintf("map-%05d-r-%05d.kv", i, r))
}

// writeKV writes records to a new MRKV file at path.
func writeKV(path string, records extsort.Records) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	w := kv.NewWriter(f)
	for records.Next() {
		if err := w.Write(records.Key(), records.Value()); err != nil {
			return errors.Join(err, f.Close())
		}
	}
	return errors.Join(records.Err(), w.Close(), f.Close())
}

func (j *job) context(kind api.TaskKind, index int) api.Context {
	return &taskContext{
		task:   api.TaskID{Kind: kind, Index: uint32(index)},
		job:    j,
		logger: slog.Default().With("task", fmt.Sprintf("%v-%d", kind, index)),
	}
}

// taskContext is the api.Context of a local task. Tasks run one at a time,
// so counters need no locking.
type taskContext struct {
	task   api.TaskID
	job    *job
	logger *slog.Logger
}

func (c *taskContext) Task() api.TaskID               { return c.task }
func (c *taskContext) Param(name string) string       { return c.job.spec.Params[name] }
func (c *taskContext) Count(name string, delta int64) { c.job.counts[name] += delta }
func (c *taskContext) Logger() *slog.Logger           { return c.logger }

type emitter func(key, value []byte) error

func (f emitter) Emit(key, value []byte) error { return f(key, value) }
