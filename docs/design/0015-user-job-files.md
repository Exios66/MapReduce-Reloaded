# ADR 0015 — User job files, interpreted by the workers

**Status:** accepted · **Phase:** course deliverable

## Context

The assignment's client submits "a user-provided file containing map and
reduce functions" with each job. Until now a job named a program compiled
into the image (`wordcount`, `grep`, …; [ADR 0005](0005-programming-api.md)),
so running new code meant rebuilding the image and restarting the cluster.

The workers are static Go binaries in a distroless image: no compiler, no
cgo, no shell.

## Decision

A job file is one Go source file. Workers run it with the
[Yaegi](https://github.com/traefik/yaegi) Go interpreter (v0.16.1), embedded
in the worker binary.

**What a job file looks like.**

```go
package job

import (
	"bytes"
	"strconv"

	"mapreduce/api"
)

// Map is called once per input record. For text input, key is the byte
// offset of the line and value is the line.
func Map(ctx api.Context, key, value []byte, out api.Emitter) error {
	for _, w := range bytes.FieldsFunc(value, isSpace) {
		if err := out.Emit(w, []byte("1")); err != nil {
			return err
		}
	}
	return nil
}

// isSpace is the same word boundary the registered wordcount uses: ASCII
// whitespace only, so both versions give the same output.
func isSpace(r rune) bool { return r == ' ' || '\t' <= r && r <= '\r' }

// Reduce is called once per key. values yields each value in turn; return
// false from the callback to stop early.
func Reduce(ctx api.Context, key []byte, values func(yield func([]byte) bool), out api.Emitter) error {
	var n int64
	values(func(v []byte) bool {
		x, _ := strconv.ParseInt(string(v), 10, 64)
		n += x
		return true
	})
	return out.Emit(key, strconv.AppendInt(nil, n, 10))
}

// Combine is optional. If present, it runs on map output (§4.3) and must
// obey the rules in api.Combiner.
var Combine = Reduce
```

- The package is `job`. `Map` and `Reduce` are required; `Combine` and
  `Partition(key []byte, r uint32) uint32` are optional.
- `mapreduce/api` gives the file `api.Context` and `api.Emitter`, the same
  interfaces compiled programs use. The standard library is available
  through Yaegi's own symbol tables. Nothing else is importable.
- `values` is a plain function type, not `iter.Seq[[]byte]`, for two reasons
  found in a spike on Go 1.26: Yaegi cannot import `iter`, and
  `for v := range values` over a function panics in the interpreter. The
  loader passes the framework's `iter.Seq[[]byte]` straight through, since
  the two types have the same underlying type.

**How the file travels (planned).**

- Once `JobSpec.program_source` is added to the protobuf contract,
  `mr submit … ./wordcount.go …` will read the file on the client's machine
  and send its source in that field. Setting `program` to the file's base
  name for display is also planned.
- The coordinator checks nothing beyond a size limit (1 MiB). It hands the
  `JobSpec` to every task, as it already does, so the write-ahead log's
  `JobSubmitted` record holds the source and a restarted coordinator can
  still run the job ([ADR 0007](0007-write-ahead-log.md)).
- Each worker interprets the source once per job, then uses the same
  `api.Program` for every task of that job. A file that does not compile, or
  lacks `Map` or `Reduce`, must not be treated as an ordinary task failure.
  Today `TaskTable.Fail` makes a failed task idle again, and only a
  bad-record limit (`MaxSkippedRecords`) ends a job, so the task would be
  retried forever. The design (planned) has two parts:
  - The coordinator loads the source with the same interpreter when it
    accepts the submit, and rejects the submit with the interpreter's error
    (`InvalidArgument`). Most errors never reach a worker.
  - `TaskFailure` gets a `fatal` field (a proto change). A worker that fails
    to load the source, which can happen only if the interpreter differs from
    the coordinator's, sets it. The coordinator then ends the job at once
    through the same path as the bad-record limit, with the message shown in
    `mr status`, and does not requeue the task.
- Registered program names still work. `mr` takes a `.go` path or a name in
  the same position, so the examples, the golden tests and the benchmarks
  keep running compiled code.

## Alternatives considered

- **Go plugins (`plugin.Open`).** Needs cgo and a plugin built by exactly the
  worker's toolchain and dependency versions. The static distroless image
  rules it out, and a user cannot build one without our build environment.
- **Compile in the worker.** Puts a Go toolchain (hundreds of MB) in every
  worker image and a compile in front of every job.
- **Python file, streamed to a subprocess** (Hadoop Streaming style). Reuses
  the Python reference API, but adds Python to the image, a process per task
  and a record protocol over pipes. It stays the fallback if Yaegi proves
  unworkable.
- **WebAssembly.** The user would need a wasm toolchain, and calls across the
  boundary for every record cost more than the interpreter.

## Consequences

- Users write ordinary Go against a small API and submit a file. No rebuild
  and no restart.
- Interpreted code is slower than compiled code. The evaluation measures
  word count both ways, so the cost is known rather than guessed.
- Recovery and bad-record skipping ([ADR 0011](0011-bad-records.md)) are
  established for compiled `api.Program` callbacks: `TestBadRecords` in
  `go/internal/task/task_test.go` covers Map and Reduce panics. Once
  interpreted jobs are integrated, add Map and Reduce panic tests to
  establish their recovery behavior.
- The interpreter runs user code with the worker's permissions. That is
  acceptable on a single-user course VM, and it is no worse than the
  compiled-in programs. A shared cluster would need a sandbox.
- Yaegi has had no release since April 2024. The spike shows v0.16.1 works
  on Go 1.26 for the standard library a job file needs, with the restrictions
  above. If a future Go release breaks it, the Python streaming fallback
  needs no change to the coordinator.
