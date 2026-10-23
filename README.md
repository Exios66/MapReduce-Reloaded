# MapReduce-Reloaded

A from-scratch re-implementation of Google's MapReduce, as described in
Dean & Ghemawat, [*MapReduce: Simplified Data Processing on Large Clusters*](https://research.google.com/archive/mapreduce-osdi04.pdf)
(OSDI 2004), extended for modern infrastructure: object storage, containers,
gRPC, observability, high availability, and multi-language user code.

- **Go** — primary implementation (coordinator, worker, `mr` CLI).
- **Python** — complete reference implementation that speaks the same
  protocol and file format, so the two can be mixed in one cluster.

## Status

**Phase 3 — fault tolerance.** Local mode runs jobs in Go and Python, and
the Go coordinator and workers run them over gRPC, re-running the work of
workers that die (§3.3). See the [roadmap](docs/roadmap.md) for the full
plan, [the roadmap explained](docs/roadmap-explained.md) for a plain-language
walkthrough and the 6-week schedule, and the [paper mapping](docs/paper-mapping.md) for feature-by-feature
progress against the paper. From #21 the cluster runs in containers on a
single 8 GB VM; see [cluster VM](docs/cluster-vm.md) for setup.

## Repository layout

```
proto/     gRPC/protobuf contract shared by Go and Python
scripts/   code generation and other helper scripts
go/        Go implementation
python/    Python reference implementation
docs/      roadmap, paper mapping, file format, design records (ADRs)
deploy/    Docker, Compose, Helm                             (Phase 2+)
bench/     data generators and benchmark harness             (Phase 7)
```

## Development

Requirements: Go 1.26+, Python 3.11+, `golangci-lint` v2 (built with Go 1.26+).

```sh
pip install -e "python[dev]"   # install the Python package + dev tools
make tools                     # install pinned code generators into .tools/bin
make hooks                     # once per clone: run `make check` before every push
make check                     # all linters and tests (the project's quality gate)
make fmt                       # auto-format both languages
make proto                     # regenerate code after editing proto/
```

`make` runs Python as `python3`. To use another interpreter, e.g. a
virtualenv's `python`, pass `PYTHON=...` (`make check PYTHON=python`).

There is no automatic hosted CI. `make check`, run by the pre-push hook, is
the gate. See [ADR 0002](docs/design/0002-local-quality-gate.md).

## Design records

Key decisions are written up as short ADRs in [`docs/design/`](docs/design/).
