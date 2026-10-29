# Testing

`make check` runs everything that needs no Docker: code generation checks,
lint, the Go tests under the race detector, the Python tests, and the
coverage floors. CI runs the same targets on every pull request, split into
four jobs ([CI](ci.md)); `make ci-report` posts the VM's run as a commit
status when no hosted service is available (ADR 0014). The other kinds of
test below are run by hand or by their own target, and CI runs none of the
rows marked "no" (Docker, S3, extended chaos, fuzz, flaky, benchmarks).

| Kind | Command | In `make check` | Where |
|---|---|---|---|
| Unit | `cd go && go test ./...`, `cd python && python -m pytest` | yes | next to each package |
| Golden output, Go against Python | `go test ./cmd/mr -run TestExamples` | yes | `testdata/examples/`, #153 |
| Cluster in one process | `go test ./internal/worker` | yes | `internal/worker/cluster_test.go` |
| Chaos, seeded | `go test ./internal/worker -run TestChaos -chaos.runs=50` | 3 seeds | `TestChaos`, `TestChaosPrograms` (#155) |
| Coordinator model | `go test ./internal/coordinator -run TestModel -model.runs=300` | 15 seeds | `model_test.go` (#154) |
| Processes on localhost | `go test ./internal/e2e` | yes | #151 |
| Python coordinator, Go workers | `make py-e2e` | yes, but they skip if Go cannot build them; `make py-e2e`, run by CI's `go` job, sets `MR_REQUIRE_GO`, so a skip fails | `python/tests/test_coordinator_e2e.py`, #36 |
| Storage contract | `go test ./internal/storage` | yes; `make e2e-s3` adds a real gateway | #156 |
| Fuzz | `make fuzz` (`FUZZTIME`, default 20s a target) | stored seeds only | #152 |
| Flaky tests | `make flaky` | no | #157 |
| Mutation | `make mutate` | no | `go/tools/mutate`, #161 |
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
line were wrong. Mutation testing measures that.

## Mutation testing

`make mutate` runs `go/tools/mutate` over the critical Go packages. It
changes one operator, constant or boolean at a time, runs the package's
tests on each change through `go test -overlay` (the tree is not modified),
and prints the changes no test caught. A mutant that survives is either a
missing test or a change that makes no difference. It takes several minutes
a package; run it on the VM.

Measured on 2026-10-28, on the course VM:

| Package, files | Killed | Survived | Score |
|---|---|---|---|
| `internal/partition`, `hash.go` | 7 | 0 | 100% |
| `internal/extsort`, `merge.go sorter.go` | 71 | 5 | 93.4% |
| `internal/coordinator`, `tasktable.go` | 103 | 4 | 96.3% |
| `internal/task`, `task.go` | 55 | 4 | 93.2% |

The first run left 36 survivors (77.6% to 89.8%). Tests now kill all of
them except the 13 below. These make no difference that a test could see:

- `extsort`: the tuning constants `DefaultBudget` and `recordOverhead`
  (`sorter.go:30,33`); `s.arena[:1]` for `s.arena[:0]` after a spill
  (`sorter.go:182`), since a record's offset is the arena's length; and
  `index <= index` for `<` in the merge's tie break (`merge.go:61`), since
  no two streams share an index.
- `coordinator/tasktable.go`: -2 for the -1 that means "none yet"
  (lines 96 and 125), and a starting best rank of 1 (125), which the first
  candidate overwrites; `stopped := true` (406), which matters only for a
  task in progress with no attempt, a state the table never leaves a task
  in.
- `task/task.go`: `writeKV`'s size on an error (lines 219, 225, 229), which
  its caller discards; and each partition's sort budget,
  `DefaultBudget * R` for `/ R` (85), which changes only memory use, and
  would need a map task of over 64 MB to see.

`make mutate-py` does the same for the Python MRKV format and partition
function with [mutmut](https://github.com/boxed/mutmut), installed by
`pip install -e 'python[dev,mutation]'`; `python/pyproject.toml` names the
files and the tests it runs. It takes about a minute.

| File | Killed | Survived | Score |
|---|---|---|---|
| `formats/kv.py` | 372 | 77 | 82.9% |
| `partition/hash.py` | 11 | 3 | 78.6% |

The first run killed 78.4% of `kv.py`. `tests/test_kv_boundaries.py` now
kills the rest of the mutants that a test could see, at limits no test had
reached: a length of exactly 128, a record, a block and a declared size of
exactly `MAX_BLOCK_SIZE`, a three-byte snappy prefix, and a length prefix
cut off at the end of a block. The one timeout, counted as killed, is a
length encoder made to loop forever. The 80 survivors in the two files
are of three kinds:

- 47 change only the text of an error message. Tests check an error's
  kind, not its wording.
- 18 change the CRC-32C table, which is built when the module is imported,
  before mutmut switches a mutant on. The test vectors written by the Go
  code catch them: making the table's inner loop run 9 times instead of 8
  fails `tests/test_kv.py`.
- 15 make no difference: a value replaced by another that is just as false
  (`None` for `False`, `""` for `None`); zstd's module-level `compress` for
  a `ZstdCompressor`, which writes the same frame; `f.read(2)` for
  `f.read(1)` at the end of the file; and changes that turn one bad block
  into another (a zstd frame that declares 0 bytes; an 11th byte of a
  length prefix; `_field`'s check for a length of 2^64 or more, which the
  check against the payload's end covers).
