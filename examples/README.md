# Examples

This directory holds the two files the documentation runs, not the compiled
example programs (those live in `go/examples/`).

- [`wordcount.go`](wordcount.go): word count as a job file, `package job`
  with `Map`, `Reduce` and `Combine`. Its source is sent with the job and
  interpreted by the workers (ADR 0015); no compiled binary is uploaded.
  Submit it on the cluster with `bin/mr submit`, or run it in one process
  with `bin/mr run -local`; the file's header comment shows the submit command.
  Its output is the compiled-in `wordcount` program's, byte for byte. The
  quick start in the [README](../README.md#quick-start) and the
  [demo](../docs/demo.md) both use it.
- [`demo.ipynb`](demo.ipynb): the demo walkthrough from the runbook (§5),
  cell by cell ([`docs/runbook.md`](../docs/runbook.md)). It needs the running
  cluster (`make cluster-up mr`), the example data (`make upload-example`)
  and the `notebook` extra (`pip install -e 'python[notebook]'`). It submits
  the compiled `wordcount` and `urlfreq` programs by name and this
  directory's job file, kills a worker during a 1 GiB sort
  (`KILL_WORKER = False` skips it), and ends by checking local mode, the
  cluster and `testdata/corpus/expected` against each other, byte for byte.
  It runs from any directory in the repository, in about a minute. The
  [demo](../docs/demo.md) is the script for a live review, with timings.

To run either, start with the commands in the README's
[quick start](../README.md#quick-start).

The compiled example programs — `grep`, `distsort`, `papersort`, `wordcount`,
`invertedindex`, `reverselinks`, `termvector`, `urlfreq` — are registered
programs in `go/examples/` and run with `mr run` or on the cluster by name.
