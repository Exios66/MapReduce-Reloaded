# ADR 0005 — The programming API: bytes in, bytes out

**Status:** accepted · **Phase:** 1 · **Issue:** #7

## Context

Users write a map function and a reduce function (§2); the framework runs
them. The API has to work identically in Go and Python, so that either
language's workers can run either language's data, and it has to stay out
of the way of the shuffle, which sorts, partitions and moves records it
does not understand.

The main question is what a key and a value are: typed objects (Go
generics, Python objects) or raw bytes.

## Decision

1. **Keys and values are bytes** (`[]byte` in Go, `bytes` in Python). User
   code encodes and decodes its own data, inside Map and Reduce. This
   matches the paper, whose interface is strings, and the file format,
   which sorts and hashes raw bytes ([file-format.md](../file-format.md)).
   Go and Python then agree on order and partitioning with no extra rules.
2. **One-method interfaces with function adapters in Go.** `Mapper`,
   `Reducer` and `Partitioner` are interfaces, so a stateful implementation
   is possible; `MapFunc`, `ReduceFunc` and `PartitionFunc` let the common
   case be a plain function. `Combiner` is an alias of `Reducer` (§4.3).
3. **Reduce values are streamed**: `iter.Seq[[]byte]` in Go, a one-pass
   iterable in Python. A key's values may not fit in memory (§4.1).
4. **Output errors reach user code.** Go's `Emitter.Emit` returns an error
   (for example, a full disk), which the user returns. Python functions
   yield or return pairs, and the framework checks they are bytes.
5. **Context** gives user code its task ID, the job's params, counters
   (§4.9) and a logger. It does not carry cancellation yet; backup tasks
   (#25) can add it, and only the framework implements Context.
6. **`Program` is the code, `JobSpec` is code plus data.** The wire
   `JobSpec` names a registered program, so the in-process `JobSpec` holds
   a `Program` and otherwise mirrors the wire fields. M is not set directly:
   it follows from the split size, as in the paper (§3.1) and on the wire.

## Alternatives rejected

- **Generic, typed keys** (`Mapper[K, V]`): each job would need a codec
  registered in both languages, the shuffle would need to call it to sort,
  and cross-language jobs would need matching codecs. Typed helpers can be
  layered on top of the byte API later without changing it.
- **Python `ctx.emit()`** alongside yield: two ways to do one thing. A
  function that builds a list can return it.

## Consequences

- Word count is under 20 lines in each language (see `go/pkg/api` and
  `python/tests/test_api.py`).
- User code converts numbers to and from bytes itself, as the paper's
  `ParseInt` and `AsString` do.
