# Examples

This directory holds the two files the documentation runs, not the compiled
example programs (those live in `go/examples/`).

- [`wordcount.go`](wordcount.go): the Go job file used in the quick start
  ([README](../README.md#quick-start)) and the demo. Its source is sent with the
  job and interpreted by the workers (ADR 0015); no compiled binary is
  uploaded.
- [`demo.ipynb`](demo.ipynb): the demo walkthrough from the runbook (§5),
  cell by cell.

The compiled example programs — `grep`, `distsort`, `wordcount`,
`invertedindex`, `reverselinks`, `termvector`, `urlfreq` — are registered
programs in `go/examples/` and run with `mr run` or on the cluster by name.
