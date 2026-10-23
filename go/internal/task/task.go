// Package task runs one attempt of a map or reduce task (§3.1). Local mode
// runs every task with it in turn; a worker runs the tasks it is given.
//
// An attempt writes its files under temporary names and renames them only
// once they are complete, so a failed or duplicate attempt never leaves a
// partial file under a final name (§3.3).
package task

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
	"github.com/exios66/mapreduce-reloaded/go/pkg/api"
)

// Framework counters, reported alongside the user's.
const (
	MapInputRecords     = "map.input.records"
	MapOutputRecords    = "map.output.records"
	ReduceOutputRecords = "reduce.output.records"
)

// MapOutput returns the path of map task m's output for partition r in dir.
func MapOutput(dir string, m, r int) string {
	return filepath.Join(dir, fmt.Sprintf("map-%05d-r-%05d.kv", m, r))
}

// Map runs a map task attempt: it maps one split and writes one sorted,
// combined MRKV file per reduce task to dir, named by MapOutput. It returns
// the size of each file and the attempt's counters.
func Map(spec api.JobSpec, id api.TaskID, split formats.Split, dir string) (sizes []int64, counts map[string]int64, err error) {
	in, err := formats.Input(spec.InputFormat)
	if err != nil {
		return nil, nil, err
	}
	ctx := newContext(spec, id)
	p := spec.Program
	partitioner := p.Partitioner
	if partitioner == nil {
		partitioner = api.HashPartitioner
	}

	sorters := make([]*extsort.Sorter, spec.R)
	for r := range sorters {
		cfg := extsort.Config{Dir: dir, Budget: extsort.DefaultBudget / len(sorters)}
		if p.Combiner != nil {
			cfg.Combine = combine.Func(ctx, p.Combiner)
		}
		sorters[r] = extsort.New(cfg)
		defer closeJoin(&err, sorters[r])
	}

	records, err := in.Open(split)
	if err != nil {
		return nil, nil, err
	}
	defer closeJoin(&err, records)
	emit := emitter(func(key, value []byte) error {
		ctx.Count(MapOutputRecords, 1)
		return sorters[partitioner.Partition(key, spec.R)].Add(key, value)
	})
	for records.Next() {
		ctx.Count(MapInputRecords, 1)
		if err := p.Mapper.Map(ctx, records.Key(), records.Value(), emit); err != nil {
			return nil, nil, err
		}
	}
	if err := records.Err(); err != nil {
		return nil, nil, err
	}

	sizes = make([]int64, len(sorters))
	for r, s := range sorters {
		sorted, err := s.Sorted()
		if err != nil {
			return nil, nil, err
		}
		if sizes[r], err = writeKV(MapOutput(dir, int(id.Index), r), id.Attempt, sorted); err != nil {
			return nil, nil, err
		}
	}
	return sizes, ctx.counts, nil
}

// TempDir is the directory under a job's output where reduce attempts write
// before committing. Readers of the output skip it, as they skip every name
// starting with "_".
const TempDir = "_temporary"

// Reduce runs a reduce task attempt over partition id.Index of every map
// task's output, given in map task order, and writes
// <spec.Output>/part-r-<id.Index> through a file in TempDir. It returns the
// attempt's counters.
func Reduce(spec api.JobSpec, id api.TaskID, sources []extsort.Records) (counts map[string]int64, err error) {
	out, err := formats.Output(spec.OutputFormat)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(spec.Output, TempDir), 0o755); err != nil {
		return nil, err
	}
	name := fmt.Sprintf("part-r-%05d", id.Index)
	path := filepath.Join(spec.Output, name)
	tmp := attemptPath(filepath.Join(spec.Output, TempDir, name), id.Attempt)
	w, err := out.Create(tmp)
	if err != nil {
		return nil, err
	}
	defer commit(&err, w, tmp, path)

	ctx := newContext(spec, id)
	emit := emitter(func(key, value []byte) error {
		ctx.Count(ReduceOutputRecords, 1)
		return w.Write(key, value)
	})
	merged := extsort.Merge(sources...) // in map task order, so equal keys stay in input order
	for key, values := range extsort.Groups(merged) {
		if err := spec.Program.Reducer.Reduce(ctx, key, values, emit); err != nil {
			return nil, err
		}
	}
	return ctx.counts, merged.Err()
}

// OpenSources opens MRKV files as reduce sources. The returned function
// closes them.
func OpenSources(paths []string) ([]extsort.Records, func() error, error) {
	files := make([]*os.File, 0, len(paths))
	closeAll := func() error {
		var errs []error
		for _, f := range files {
			errs = append(errs, f.Close())
		}
		return errors.Join(errs...)
	}
	sources := make([]extsort.Records, len(paths))
	for i, path := range paths {
		f, err := os.Open(path)
		if err != nil {
			return nil, nil, errors.Join(err, closeAll())
		}
		files = append(files, f)
		sources[i] = kv.NewReader(f)
	}
	return sources, closeAll, nil
}

// writeKV writes records to a new MRKV file at path and returns its size.
func writeKV(path string, attempt uint32, records extsort.Records) (size int64, err error) {
	tmp := attemptPath(path, attempt)
	f, err := os.Create(tmp)
	if err != nil {
		return 0, err
	}
	defer commit(&err, f, tmp, path)
	w := kv.NewWriter(f)
	for records.Next() {
		if err := w.Write(records.Key(), records.Value()); err != nil {
			return 0, err
		}
	}
	if err := errors.Join(records.Err(), w.Close()); err != nil {
		return 0, err
	}
	return f.Seek(0, io.SeekCurrent)
}

// CheckOutput returns an error if output already holds part files, so that
// a job's output is never mixed with an earlier job's.
func CheckOutput(output string) error {
	parts, err := filepath.Glob(filepath.Join(output, "part-r-*"))
	if err == nil && len(parts) > 0 {
		err = fmt.Errorf("task: %s already holds job output", output)
	}
	return err
}

// Cleanup removes what reduce attempts left in a finished job's output: the
// files of attempts that failed or died before committing.
func Cleanup(output string) error {
	return os.RemoveAll(filepath.Join(output, TempDir))
}

func attemptPath(path string, attempt uint32) string {
	return fmt.Sprintf("%s.attempt-%d", path, attempt)
}

// commit closes c, then renames tmp to path if the attempt succeeded, or
// removes tmp if it failed. Use it with defer.
func commit(err *error, c io.Closer, tmp, path string) {
	if *err = errors.Join(*err, c.Close()); *err == nil {
		*err = os.Rename(tmp, path)
	}
	if *err != nil {
		_ = os.Remove(tmp) // best effort: the attempt has already failed
	}
}

// closeJoin closes c, adding any error to *err. Use it with defer.
func closeJoin(err *error, c io.Closer) { *err = errors.Join(*err, c.Close()) }

// taskContext is the api.Context of one attempt. An attempt runs on one
// goroutine, so its counters need no locking.
type taskContext struct {
	task   api.TaskID
	params map[string]string
	counts map[string]int64
	logger *slog.Logger
}

func newContext(spec api.JobSpec, id api.TaskID) *taskContext {
	return &taskContext{
		task:   id,
		params: spec.Params,
		counts: map[string]int64{},
		logger: slog.Default().With("task", fmt.Sprintf("%v-%d.%d", id.Kind, id.Index, id.Attempt)),
	}
}

func (c *taskContext) Task() api.TaskID               { return c.task }
func (c *taskContext) Param(name string) string       { return c.params[name] }
func (c *taskContext) Count(name string, delta int64) { c.counts[name] += delta }
func (c *taskContext) Logger() *slog.Logger           { return c.logger }

type emitter func(key, value []byte) error

func (f emitter) Emit(key, value []byte) error { return f(key, value) }
