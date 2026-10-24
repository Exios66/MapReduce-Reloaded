// Package task runs one attempt of a map or reduce task (§3.1). Local mode
// runs every task with it in turn; a worker runs the tasks it is given.
//
// An attempt writes its files under temporary names and commits them only
// once they are complete, so a failed or duplicate attempt never leaves a
// partial file under a final name (§3.3).
package task

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/exios66/mapreduce-reloaded/go/internal/combine"
	"github.com/exios66/mapreduce-reloaded/go/internal/extsort"
	"github.com/exios66/mapreduce-reloaded/go/internal/formats"
	"github.com/exios66/mapreduce-reloaded/go/internal/formats/kv"
	"github.com/exios66/mapreduce-reloaded/go/internal/storage"
	"github.com/exios66/mapreduce-reloaded/go/pkg/api"
)

// Framework counters, reported alongside the user's.
const (
	MapInputRecords     = "map.input.records"
	MapInputBytes       = "map.input.bytes" // the split's size
	MapOutputRecords    = "map.output.records"
	MapOutputBytes      = "map.output.bytes" // file sizes, after combining and compression
	ReduceInputRecords  = "reduce.input.records"
	ReduceOutputRecords = "reduce.output.records"
	SkippedRecords      = "skipped.records"
)

// MapOutput returns the path of map task m's output for partition r in dir.
func MapOutput(dir string, m, r int) string {
	return filepath.Join(dir, fmt.Sprintf("map-%05d-r-%05d.kv", m, r))
}

// Map runs a map task attempt: it maps one split and writes one sorted,
// combined MRKV file per reduce task to dir, named by MapOutput. It skips the
// split's records numbered in skip (§4.6). User code logs to log, which
// the caller tags with the task. It returns the size of each file and the
// attempt's counters. It stops with ctx's error when ctx is done.
func Map(ctx context.Context, spec api.JobSpec, id api.TaskID, split formats.Split, dir string, skip []uint64, log *slog.Logger) (sizes []int64, counts map[string]int64, err error) {
	in, err := formats.Input(spec.InputFormat)
	if err != nil {
		return nil, nil, err
	}
	codec, err := kv.ParseCodec(spec.IntermediateCodec)
	if err != nil {
		return nil, nil, err
	}
	tc := newContext(ctx, spec, id, skip, log)
	tc.input = split.Path
	defer tc.commitSide(&err)
	defer recovered(&err)
	p := spec.Program
	partitioner := p.Partitioner
	if partitioner == nil {
		partitioner = api.HashPartitioner
	}

	sorters := make([]*extsort.Sorter, spec.R)
	for r := range sorters {
		cfg := extsort.Config{Dir: dir, Budget: extsort.DefaultBudget / len(sorters), Codec: codec}
		if p.Combiner != nil {
			cfg.Combine = combine.Func(tc, p.Combiner)
		}
		sorters[r] = extsort.New(cfg)
		defer closeJoin(&err, sorters[r])
	}

	records, err := in.Open(ctx, split)
	if err != nil {
		return nil, nil, err
	}
	defer closeJoin(&err, records)
	tc.Count(MapInputBytes, split.End-split.Start)
	emit := emitter(func(key, value []byte) error {
		tc.Count(MapOutputRecords, 1)
		return sorters[partitioner.Partition(key, spec.R)].Add(key, value)
	})
	for n := uint64(0); records.Next(); n++ {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		tc.Count(MapInputRecords, 1)
		if err := tc.call(n, func() error { return p.Mapper.Map(tc, records.Key(), records.Value(), emit) }); err != nil {
			return nil, nil, err
		}
	}
	if err := records.Err(); err != nil {
		return nil, nil, err
	}

	sizes = make([]int64, len(sorters))
	for r, s := range sorters {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		sorted, err := s.Sorted()
		if err != nil {
			return nil, nil, err
		}
		if sizes[r], err = writeKV(MapOutput(dir, int(id.Index), r), id.Attempt, codec, sorted); err != nil {
			return nil, nil, err
		}
		tc.Count(MapOutputBytes, sizes[r])
	}
	return sizes, tc.counts, ctx.Err() // a cancel during the last write commits no side files
}

// TempDir is the directory under a job's output where attempts write
// before committing. Readers of the output skip it, as they skip every name
// starting with "_". The job creates it with Setup and removes it with
// Cleanup; attempts never create it, so one that runs after the job has
// finished fails and leaves nothing behind.
const TempDir = "_temporary"

// Reduce runs a reduce task attempt over partition id.Index of every map
// task's output, given in map task order, and writes
// <spec.Output>/part-r-<id.Index> through a file in TempDir. It skips the key
// groups numbered in skip (§4.6). User code logs to log, which the caller
// tags with the task. It returns the attempt's counters. It stops with ctx's
// error when ctx is done.
func Reduce(ctx context.Context, spec api.JobSpec, id api.TaskID, sources []extsort.Records, skip []uint64, log *slog.Logger) (counts map[string]int64, err error) {
	codec, err := kv.ParseCodec(spec.OutputCodec)
	if err != nil {
		return nil, err
	}
	out, err := formats.Output(spec.OutputFormat, codec)
	if err != nil {
		return nil, err
	}
	name := fmt.Sprintf("part-r-%05d", id.Index)
	path := storage.Join(spec.Output, name)
	tmp := attemptPath(storage.Join(spec.Output, TempDir, name), id.Attempt)
	w, err := out.Create(ctx, tmp)
	if err != nil {
		return nil, err
	}
	defer commit(ctx, &err, w, tmp, path)

	tc := newContext(ctx, spec, id, skip, log)
	defer tc.commitSide(&err) // before the part file, which marks the task done
	defer recovered(&err)
	emit := emitter(func(key, value []byte) error {
		tc.Count(ReduceOutputRecords, 1)
		return w.Write(key, value)
	})
	merged := extsort.Merge(sources...) // in map task order, so equal keys stay in input order
	n := uint64(0)
	for key, values := range extsort.Groups(counted{merged, tc}) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := tc.call(n, func() error { return spec.Program.Reducer.Reduce(tc, key, values, emit) }); err != nil {
			return nil, err
		}
		n++
	}
	if err := merged.Err(); err != nil {
		return nil, err
	}
	return tc.counts, ctx.Err() // a cancel during the last group commits nothing
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

// writeKV writes records to a new MRKV file at path, compressed with codec,
// and returns its size.
func writeKV(path string, attempt uint32, codec kv.Codec, records extsort.Records) (size int64, err error) {
	tmp := attemptPath(path, attempt)
	f, err := os.Create(tmp)
	if err != nil {
		return 0, err
	}
	defer commit(context.Background(), &err, f, tmp, path)
	w := kv.NewWriterCodec(f, codec)
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
func CheckOutput(ctx context.Context, output string) error {
	parts, err := storage.List(ctx, storage.Join(output, "part-r-*"))
	if err == nil && len(parts) > 0 {
		err = fmt.Errorf("task: %s already holds job output", output)
	}
	return err
}

// Setup creates the job's TempDir.
func Setup(ctx context.Context, output string) error {
	return storage.MkdirAll(ctx, storage.Join(output, TempDir))
}

// Cleanup removes a finished job's TempDir, with the files of attempts that
// failed or died before committing.
func Cleanup(ctx context.Context, output string) error {
	return storage.DeleteAll(ctx, storage.Join(output, TempDir))
}

func attemptPath(path string, attempt uint32) string {
	return fmt.Sprintf("%s.attempt-%d", path, attempt)
}

// commit closes c, then commits tmp as path if the attempt succeeded, or
// removes tmp if it failed. Use it with defer.
func commit(ctx context.Context, err *error, c io.Closer, tmp, path string) {
	if *err = errors.Join(*err, c.Close()); *err == nil {
		*err = storage.Commit(ctx, tmp, path)
	}
	if *err != nil {
		_ = storage.Delete(context.WithoutCancel(ctx), tmp) // best effort: the attempt has already failed
	}
}

// closeJoin closes c, adding any error to *err. Use it with defer.
func closeJoin(err *error, c io.Closer) { *err = errors.Join(*err, c.Close()) }

// CrashError is the error of an attempt whose user code panicked on a
// record: for a map task, the input record's number in its split; for a
// reduce task, the key group's number. Both count from 0 (§4.6).
type CrashError struct {
	Record uint64
	Panic  any
}

func (e *CrashError) Error() string {
	return fmt.Sprintf("task: user code panicked on record %d: %v", e.Record, e.Panic)
}

// taskContext is the api.Context of one attempt. An attempt runs on one
// goroutine, so its counters need no locking.
type taskContext struct {
	ctx    context.Context // the attempt's, for its side files
	task   api.TaskID
	params map[string]string
	input  string // the split's file, in a map task
	counts map[string]int64
	logger *slog.Logger
	output string
	side   map[string]*sideFile
	skip   map[uint64]bool
}

func newContext(ctx context.Context, spec api.JobSpec, id api.TaskID, skip []uint64, log *slog.Logger) *taskContext {
	c := &taskContext{
		ctx:    ctx,
		task:   id,
		params: spec.Params,
		output: spec.Output,
		side:   map[string]*sideFile{},
		counts: map[string]int64{},
		logger: log,
		skip:   map[uint64]bool{},
	}
	for _, n := range skip {
		c.skip[n] = true
	}
	return c
}

// call runs f, the user's code for record n, unless n is to be skipped. A
// panic in f becomes a *CrashError, so the worker can report the record.
func (c *taskContext) call(n uint64, f func() error) (err error) {
	if c.skip[n] {
		c.Count(SkippedRecords, 1)
		return nil
	}
	defer func() {
		if v := recover(); v != nil {
			err = &CrashError{Record: n, Panic: v}
		}
	}()
	return f()
}

// recovered turns a panic in user code outside any one record, such as a
// combiner's in the final spill, into the attempt's error, so that the
// attempt fails and commits nothing.
func recovered(err *error) {
	if v := recover(); v != nil {
		*err = fmt.Errorf("task: panic: %v", v)
	}
}

func (c *taskContext) Task() api.TaskID               { return c.task }
func (c *taskContext) Param(name string) string       { return c.params[name] }
func (c *taskContext) Input() string                  { return c.input }
func (c *taskContext) Count(name string, delta int64) { c.counts[name] += delta }
func (c *taskContext) Logger() *slog.Logger           { return c.logger }

func (c *taskContext) SideOutput(name string) (io.Writer, error) {
	if name != filepath.Base(name) || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") ||
		strings.HasPrefix(name, "part-") {
		return nil, fmt.Errorf("task: bad side output name %q", name)
	}
	if s := c.side[name]; s != nil {
		return s, nil
	}
	tmp := attemptPath(storage.Join(c.output, TempDir, fmt.Sprintf("%s.%v-%d", name, c.task.Kind, c.task.Index)), c.task.Attempt)
	f, err := storage.Create(c.ctx, tmp)
	if err != nil {
		return nil, err
	}
	s := &sideFile{Writer: bufio.NewWriter(f), f: f, tmp: tmp, path: storage.Join(c.output, name)}
	c.side[name] = s
	return s, nil
}

// commitSide commits the attempt's side files if *err is nil, and removes
// them otherwise. Use it with defer.
func (c *taskContext) commitSide(err *error) {
	for _, s := range c.side {
		commit(c.ctx, err, s, s.tmp, s.path)
	}
}

// sideFile is a side output being written under its temporary name.
type sideFile struct {
	*bufio.Writer
	f         io.Closer
	tmp, path string
}

func (s *sideFile) Close() error { return errors.Join(s.Flush(), s.f.Close()) }

// counted counts the records read through it as ReduceInputRecords.
type counted struct {
	extsort.Records
	ctx *taskContext
}

func (c counted) Next() bool {
	if !c.Records.Next() {
		return false
	}
	c.ctx.Count(ReduceInputRecords, 1)
	return true
}

type emitter func(key, value []byte) error

func (f emitter) Emit(key, value []byte) error { return f(key, value) }
