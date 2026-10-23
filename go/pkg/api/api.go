package api

import (
	"iter"
	"log/slog"

	"github.com/exios66/mapreduce-reloaded/go/internal/partition"
)

// Emitter receives the records a Mapper or Reducer outputs. The framework may
// retain key and value only for the duration of the call, so callers may
// reuse their buffers afterwards. An error means the output could not be
// stored; user code should return it.
type Emitter interface {
	Emit(key, value []byte) error
}

// Mapper processes one input record (§2). For text input, key is the byte
// offset of the line and value is the line.
//
// key and value are valid only during the call.
type Mapper interface {
	Map(ctx Context, key, value []byte, out Emitter) error
}

// Reducer processes one intermediate key and all of its values (§2). Values
// arrive in no particular order and may not fit in memory, so they are
// streamed: values can be ranged over once, and each value is valid only
// until the next iteration.
type Reducer interface {
	Reduce(ctx Context, key []byte, values iter.Seq[[]byte], out Emitter) error
}

// Combiner partially merges a map task's output before it is sent to
// reducers (§4.3). It has the same signature as a Reducer and is typically
// the same code; it must be safe to apply zero or more times.
type Combiner = Reducer

// Partitioner chooses the reduce task, in 0..r-1, for an intermediate key
// (§4.1).
type Partitioner interface {
	Partition(key []byte, r uint32) uint32
}

// HashPartitioner is the default partitioner: fnv1a64(key) mod r, as
// specified in docs/file-format.md.
var HashPartitioner Partitioner = PartitionFunc(partition.Partition)

// MapFunc adapts a function to a Mapper.
type MapFunc func(ctx Context, key, value []byte, out Emitter) error

// Map calls f.
func (f MapFunc) Map(ctx Context, key, value []byte, out Emitter) error {
	return f(ctx, key, value, out)
}

// ReduceFunc adapts a function to a Reducer or Combiner.
type ReduceFunc func(ctx Context, key []byte, values iter.Seq[[]byte], out Emitter) error

// Reduce calls f.
func (f ReduceFunc) Reduce(ctx Context, key []byte, values iter.Seq[[]byte], out Emitter) error {
	return f(ctx, key, values, out)
}

// PartitionFunc adapts a function to a Partitioner.
type PartitionFunc func(key []byte, r uint32) uint32

// Partition calls f.
func (f PartitionFunc) Partition(key []byte, r uint32) uint32 { return f(key, r) }

// Context gives user code access to its task and job. The framework provides
// the implementation.
type Context interface {
	// Task identifies the running task attempt, e.g. for naming side files
	// (§4.5).
	Task() TaskID
	// Param returns a job parameter, or "" if it is not set.
	Param(name string) string
	// Count adds delta to a named counter (§4.9). Counts from failed or
	// duplicate attempts are discarded.
	Count(name string, delta int64)
	// Logger returns a logger tagged with the task.
	Logger() *slog.Logger
}

// TaskKind says whether a task is a map or a reduce task.
type TaskKind uint8

// Task kinds.
const (
	MapTask TaskKind = iota + 1
	ReduceTask
)

// TaskID identifies one attempt at one task. A task may be attempted more
// than once, after a failure or as a backup (§3.6).
type TaskID struct {
	Kind    TaskKind
	Index   uint32 // 0..M-1 for map tasks, 0..R-1 for reduce tasks
	Attempt uint32
}
