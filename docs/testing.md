# Testing

`make check` runs everything that needs no Docker: code generation checks,
lint, the Go tests under the race detector, the Python tests, and the
coverage floors. The VM's run of it is the merge gate (ADR 0014). The other
kinds of test below are run by hand or by their own target.

| Kind | Command | In `make check` | Where |
|---|---|---|---|
| Unit | `cd go && go test ./...`, `cd python && python -m pytest` | yes | next to each package |
| Golden output, Go against Python | `go test ./cmd/mr -run TestExamples` | yes | `testdata/examples/`, #153 |
| Cluster in one process | `go test ./internal/worker` | yes | `internal/worker/cluster_test.go` |
| Chaos, seeded | `go test ./internal/worker -run TestChaos -chaos.runs=50` | 3 seeds | `TestChaos`, `TestChaosPrograms` (#155) |
| Coordinator model | `go test ./internal/coordinator -run TestModel -model.runs=300` | 15 seeds | `model_test.go` (#154) |
| Processes on localhost | `go test ./internal/e2e` | yes | #151 |
| Storage contract | `go test ./internal/storage` | yes; `make e2e-s3` adds a real gateway | #156 |
| Fuzz | `make fuzz` (`FUZZTIME`, default 20s a target) | stored seeds only | #152 |
| Flaky tests | `make flaky` | no | #157 |
| Compose cluster | `make e2e`, `make e2e-s3`, `make chaos` | no | `scripts/e2e.sh`, `scripts/chaos.sh` |
| Benchmarks | `make perf` against `bench/baseline.txt` | no | #160, `bench/README.md` |
| Memory and size limits | `go test ./internal/extsort ./internal/task ./internal/formats/kv` | yes | #160 |

A failing seeded test names its seed; `-chaos.seed=N -chaos.runs=1` or
`-model.seed=N -model.runs=1` makes the same choices again.

## Coverage

`make check` fails if a package's statement coverage falls under its floor
in `go/cover-floors.txt` or `python/cover-floors.txt`
(`scripts/cover.py`). A Go package counts only its own tests; a package
with no floor fails too, so a new one must be given one. Each floor is one
point under the value measured when it was set, rounded down. `make cover`
runs the tests and prints each package's coverage beside its floor.

Measured on 2026-10-27:

- Go: 91.7% of 2,725 statements in 29 checked packages. The lowest are
  `cmd/worker` (84.8%), `internal/wal` (86.0%), `cmd/coordinator` and
  `cmd/mr` (86.8%). Unchecked: the generated protobuf code, the S3 test
  server, `examples/internal/run` (a test helper) and `examples/wordcount`,
  which the cluster, local and golden tests run as their reference.
- Python: 100% of 708 statements, the generated protobuf code left out.
  `tests/test_edges.py` covers the last error paths: the MRKV writer's
  `MAX_BLOCK_SIZE` checks, a bad Snappy length prefix, a write that
  accepts no bytes, and others.

Statement coverage says that a line ran, not that a test would fail if the
line were wrong. Mutation testing of the core packages is the rest of #161.
