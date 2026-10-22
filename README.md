# MapReduce-Reloaded

A from-scratch re-implementation of Google's MapReduce, as described in
Dean & Ghemawat, [*MapReduce: Simplified Data Processing on Large Clusters*](https://research.google.com/archive/mapreduce-osdi04.pdf)
(OSDI 2004), extended for modern infrastructure: object storage, containers,
gRPC, observability, high availability, and multi-language user code.

- **Go** — primary implementation (coordinator, worker, `mr` CLI).
- **Python** — complete reference implementation that speaks the same
  protocol and file format, so the two can be mixed in one cluster.

## Status

**Phase 0 — foundations.** Repository layout, build and checks only; no
MapReduce functionality yet. See the [roadmap](docs/roadmap.md) for the full
plan and the [paper mapping](docs/paper-mapping.md) for feature-by-feature
progress against the paper.

## Repository layout

```
proto/     gRPC/protobuf contract shared by Go and Python   (Phase 0)
go/        Go implementation
python/    Python reference implementation
docs/      roadmap, paper mapping, file format, design records (ADRs)
deploy/    Docker, Compose, Helm                             (Phase 2+)
bench/     data generators and benchmark harness             (Phase 7)
```

## Development

Requirements: Go 1.24+, Python 3.11+, `golangci-lint`, `ruff`.

```sh
pip install -e "python[dev]"   # install the Python package + dev tools
make hooks                     # once per clone: run `make check` before every push
make check                     # all linters and tests (the project's quality gate)
make fmt                       # auto-format both languages
```

There is no automatic hosted CI. `make check`, run by the pre-push hook, is
the gate. See [ADR 0002](docs/design/0002-local-quality-gate.md).

## Design records

Key decisions are written up as short ADRs in [`docs/design/`](docs/design/).
