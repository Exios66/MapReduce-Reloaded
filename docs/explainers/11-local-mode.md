# Explainer — Local Mode (#12)

## What it is

The whole MapReduce pipeline in one process, one task at a time (§4.7):

```
mr run -local -m 3 -r 4 wordcount out/ 'testdata/corpus/*.txt'
python -m mapreduce_reloaded run --local -m 3 -r 4 wordcount out/ 'testdata/corpus/*.txt'
```

The paper offers it for debugging. We use it for that, and as the
**oracle**: every distributed run must produce the same files.

## How a job runs

1. **Split** each input file into pieces of about `SplitSize` bytes
   (`-m M` picks the size that gives about M pieces).
2. **Map** each split in input order. Each output record goes to one of R
   sorters, chosen by the partitioner (#5). The sorters spill and combine
   (#10, #11), and each writes one sorted MRKV file per reduce task.
3. **Reduce** task r merges file r of every map task, in map task order,
   groups equal keys and calls the reducer. It writes `part-r-<r>`.

Steps 2 and 3 for one task live in `go/internal/task`, which the workers
use too (#16); only the scheduling differs. Each attempt writes its files
under temporary names and renames them once complete, so a failed or
duplicate attempt never leaves a partial file under a final name (§3.3).
A job refuses an output directory that already holds part files, so its
output is never mixed with an earlier job's.

## Why the output never depends on M

Each sort is stable, and the reduce merges map outputs in map task order.
So the values of a key always reach the reducer in input order, however
the input was split. (A combiner, if the job has one, may merge some of
them first.) The golden tests run M = 1, 3 and 7 and expect the
same bytes. A second test uses a reducer that concatenates values, so any
reordering would change its output.

## Flags

Both commands take `-m`, `-r`, `-param` and the record formats
(`-input-format` and `-output-format`, each `text` or `kv`; Python spells
the last three with two dashes, as `--param`). An output written with `kv` can be the input of
the next job with `-input-format kv`.

Two things differ from a cluster job:

- **No bad records are skipped (§3.7).** Skipping needs a task to crash,
  be reported and run again; local mode runs each task once, so the first
  record that makes user code fail fails the job, with the task named in
  the error. `-max-skipped` exists only on `mr submit`.
- **Python's local mode has no codecs.** Of the two, only `mr run`
  takes `-intermediate-codec` and `-output-codec`. Python jobs write uncompressed
  files, though its MRKV reader and writer handle every codec.

## Programs by name

A job on the wire names its program (#4). In Go, a program registers itself
from an `init` function (`api.Register("wordcount", ...)`), and `mr`
imports it. In Python, a name like `wordcount` loads
`mapreduce_reloaded.examples.wordcount`, and `module:attribute` loads any
other Program.

## How we know it works

- **Golden files:** word count over four public-domain books, one German
  (`testdata/corpus`) must match `expected/` byte for byte, in both
  languages, with and without the combiner.
- The expected files come from `scripts/make-corpus-expected.py`, a separate
  program written only with the standard library. It shares no code with
  either implementation.
- Counters agree with it too: 59,372 words, 11,786 distinct.

## Likely review questions

- **Why are the Python examples inside the package, not in `python/examples/`?**
  So `python -m mapreduce_reloaded run wordcount` can import them by name
  without changing `sys.path`.
- **Why one task at a time?** Local mode exists to be simple and
  deterministic. Concurrency comes with the workers in Phase 2.
- **Why does an error in user code keep its Python traceback?** The command
  line only turns setup mistakes (bad program name, missing input) into
  one-line errors. A failing mapper or reducer should show where it failed,
  plus a note naming the task.
