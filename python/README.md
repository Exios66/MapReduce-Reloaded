# Python reference implementation

The reference for the programming API, the MRKV file format and local mode.
For the same uncompressed job, it writes the same bytes as the Go
implementation. It does not run on a cluster: the coordinator and workers are
Go ([ADR 0001](../docs/design/0001-languages-and-shared-contract.md)).

```sh
pip install -e ".[dev]"       # the package and the dev tools (from this directory)
python3 -m pytest             # the tests (make py-test runs these)
python3 -m mapreduce_reloaded run --local -m 3 -r 4 wordcount /tmp/wc '../testdata/corpus/*.txt'
```

Usage mirrors `mr run -local`:

```
python -m mapreduce_reloaded run --local [-m M] [-r R] [--param NAME=VALUE]...
    [--input-format text|kv] [--output-format text|kv] PROGRAM OUTPUT_DIR INPUT...
```

`PROGRAM` is an example name such as `wordcount`, or `module:attribute` naming
your own `Program`. The Python local mode has no codec flags: jobs write
uncompressed files, though the MRKV reader and writer handle every codec
(zstd and Snappy need the optional `zstandard` and `python-snappy` packages).

## Layout (`mapreduce_reloaded/`)

| Module | Role |
|---|---|
| `api.py` | `Program`, `JobSpec`, the `@mapper`, `@reducer` and `@combiner` decorators, and the task context |
| `local.py` | Local mode, with the same counters as Go |
| `split.py`, `extsort.py`, `combine.py` | Input splits, external sort, the combiner |
| `coordinator/` | The coordinator's task table, worker registry and gRPC service, ported from `go/internal/coordinator` (#36; the write-ahead log comes next) |
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
