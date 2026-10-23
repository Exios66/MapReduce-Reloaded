package api

import (
	"fmt"
	"io"
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
// key and value are valid only during the call and must not be modified.
type Mapper interface {
	Map(ctx Context, key, value []byte, out Emitter) error
}

// Reducer processes one intermediate key and all of its values (§2). Values
// arrive in no particular order and may not fit in memory, so they are
// streamed: values can be ranged over once, and each value is valid only
// until the next iteration. key is valid only during the call. Neither may be
// modified: the framework reads them to find where the key's values end.
type Reducer interface {
	Reduce(ctx Context, key []byte, values iter.Seq[[]byte], out Emitter) error
}

// Combiner partially merges a map task's output before it is sent to
// reducers (§4.3). It has the same signature as a Reducer and is typically
// the same code. The framework may apply it zero, one or more times to any
// subset of a key's values, so it must:
//   - be commutative and associative, like a sum or a maximum;
//   - emit only the key it was given;
//   - leave the job's result unchanged: reducing its output must give what
//     reducing its input would.
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
	// Input returns the path of the file a map task's split is from, or ""
	// in a reduce task.
	Input() string
	// Count adds delta to a named counter (§4.9). Counts from failed or
	// duplicate attempts are discarded.
	Count(name string, delta int64)
	// Logger returns a logger tagged with the task.
	Logger() *slog.Logger
	// SideOutput creates a file called name in the job's output directory
	// and returns a writer to it (§4.5); later calls in the same attempt
	// return the same writer. The file appears only if the
	// attempt succeeds, so a failed attempt leaves no partial copy. name is
	// a plain file name not starting with ".", "_" or "part-". Include the
	// task index in it, so tasks don't overwrite each other's files. As in the
	// paper, there is no atomicity across files: if two attempts of a task
	// succeed, the later one's file wins, so every attempt must write the
	// same bytes.
	SideOutput(name string) (io.Writer, error)
}

// TaskKind says whether a task is a map or a reduce task.
type TaskKind uint8

// Task kinds.
const (
	MapTask TaskKind = iota + 1
	ReduceTask
)

func (k TaskKind) String() string {
	switch k {
	case MapTask:
		return "map"
	case ReduceTask:
		return "reduce"
	}
	return fmt.Sprintf("TaskKind(%d)", uint8(k))
}

// TaskID identifies one attempt at one task. A task may be attempted more
// than once, after a failure or as a backup (§3.6).
type TaskID struct {
	Kind    TaskKind
	Index   uint32 // 0..M-1 for map tasks, 0..R-1 for reduce tasks
	Attempt uint32
}

// LogValue logs id as a group of its kind, index and attempt.
func (id TaskID) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("kind", id.Kind.String()),
		slog.Uint64("index", uint64(id.Index)),
		slog.Uint64("attempt", uint64(id.Attempt)),
	)
}
