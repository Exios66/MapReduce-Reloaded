# Python reference implementation

The reference for the programming API, the MRKV file format and local mode.
For the same uncompressed job, it writes the same bytes as the Go
implementation. Its coordinator can also run a cluster of Go workers (#36);
the workers are Go only so far
([ADR 0001](../docs/design/0001-languages-and-shared-contract.md)).

```sh
pip install -e ".[dev]"       # the package and the dev tools (from this directory)
python3 -m pytest             # the tests (make py-test runs these)
python3 -m mapreduce_reloaded run --local -m 3 -r 4 wordcount /tmp/wc '../testdata/corpus/*.txt'
```

Usage mirrors `mr run -local`:

```sh
python -m mapreduce_reloaded run --local [-m M] [-r R] [--param NAME=VALUE]...
    [--input-format text|kv] [--output-format text|kv] PROGRAM OUTPUT_DIR INPUT...
```

`PROGRAM` is an example name such as `wordcount`, or `module:attribute` naming
your own `Program`. The Python local mode has no codec flags: jobs write
uncompressed files, though the MRKV reader and writer handle every codec
(zstd and Snappy need the optional `zstandard` and `python-snappy` packages).

## The coordinator

`python -m mapreduce_reloaded.coordinator` serves a coordinator as
`go/cmd/coordinator` does, with the same flags and defaults. Go workers and
`mr` work with it unchanged:

```sh
python -m mapreduce_reloaded.coordinator [-addr :7070] [-http :8080]
    [-data-dir dir] [-backups=false] [-keep-jobs n] [-max-task-failures n]
    [-log-format text|json]
```

With `-data-dir` it logs every decision there and resumes from that log
when restarted. With `-http` it serves the status page at `/` and
Prometheus metrics at `/metrics`, under the Go coordinator's metric names,
so the Grafana dashboard reads either. `tests/test_coordinator_e2e.py` runs
the Go end-to-end scenarios against it: word count, every example, a
killed worker and a restarted coordinator.

## Layout (`mapreduce_reloaded/`)

| Module | Role |
|---|---|
| `api.py` | `Program`, `JobSpec`, the `@mapper`, `@reducer` and `@combiner` decorators, and the task context |
| `local.py` | Local mode, with the same counters as Go |
| `split.py`, `extsort.py`, `combine.py` | Input splits, external sort, the combiner |
| `coordinator/` | The coordinator, ported from `go/internal/coordinator` and `go/cmd/coordinator` (#36): task table, worker registry, gRPC service, recovery from its write-ahead log, the status page and `/metrics` (`statusweb.py`), and the entry point (`__main__.py`) |
| `wal.py` | The write-ahead log, in the same file format as `go/internal/wal` |
| `formats/` | The text format and the MRKV reader and writer |
| `partition/` | The partition hash, checked against shared test vectors |
| `examples/` | The seven example programs |
| `gen/` | Python code generated from [`proto/`](../proto/); committed, regenerate with `make proto` |

## Writing a program

```python
from mapreduce_reloaded.api import Program, mapper, reducer


@mapper
def count_map(ctx, offset, line):
    for word in line.split():
        yield word, b"1"


@reducer
def count_reduce(ctx, word, counts):
    yield word, str(sum(int(c) for c in counts)).encode()


program = Program(mapper=count_map, reducer=count_reduce)
```

Save it as `mycount.py` and run it with `--local ... mycount:program`. Keys
and values are `bytes`. See
[the programming API](../docs/explainers/07-programming-api.md).
