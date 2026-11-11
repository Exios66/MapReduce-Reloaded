# ADR 0024: A faster CI pipeline that runs the same checks

Date: 2026-11-11. Status: accepted. Amends ADR 0014 and ADR 0020 (the go
job's layout and the generator cache). Nothing they require is dropped.

## Context

A PR's full pipeline (`run-all`) took about 6.5 minutes, and the `go` job
was all of it. On #352's final run (head `db396e8`, CircleCI jobs 620-625)
the jobs started together and took:

| Job | Took | Longest step |
|---|---|---|
| `go` | 395 s | `make go-lint go-test py-e2e`, 366 s |
| `proto` | 180 s | `make tools`, 151 s |
| `python-3.13` | 103 s | `make py-lint py-test`, 69 s |
| `python-3.11` | 98 s | `make py-lint py-test`, 79 s |
| `lint` | 18 s | `make md-lint`, 9 s |
| `mapping-coverage` | 7 s | |

The earlier full runs (#342's jobs 590-595, #348's 598) look the same: `go`
between 383 and 395 s, `proto` between 158 and 180 s. Over 30 days the go job
was about 55 credits a run and `proto` about 17 (CircleCI Insights).

Two things made it slow:

- **The go job ran its parts in series.** Its one step was `make go-lint
  go-test py-e2e`: gofmt, vet and golangci-lint, then `go test -race` over
  the module, then the coverage pass, then the Python coordinator with the
  Go commands. Each part is bounded by its slowest package (internal/e2e
  takes 65 s under the race detector and 78 s with coverage; py-e2e takes
  62 s), and none needs another's result. Timed on the course VM pinned to
  two CPUs, the race pass took about 150 s, the coverage pass 87 s and
  py-e2e 45 s.
- **`proto` rebuilt buf from source on every run.** The cache restored
  `.tools/bin` with the right binaries, but `make tools` ran `go install` for
  each pin, and go install decides whether a binary is current from Go's
  build cache, which the proto job does not restore. So it downloaded and
  compiled buf (and fetched the Go 1.26.11 toolchain buf needs) every time:
  120-150 s of the job.

The rule from AGENTS.md and ADR 0014 stands: save credits by running CI less
often, never by running less of it. Every job, check and make target stays.

## Decision

1. **The go job runs on three nodes** (`parallelism: 3`). CircleCI starts
   three containers of the one job and reports them as the one `go` check,
   which passes only when all three pass, so the six required check names
   and branch protection do not change. `scripts/ci-go-job.sh` gives each
   node its part of the same make targets:

   | Node | Full mode | Narrow mode (only `python/` changed) |
   |---|---|---|
   | 0 | `make go-test-race` | `make go-python-test` |
   | 1 | `make go-test-cover` | nothing |
   | 2 | `make sh-lint go-lint py-e2e` | `make py-e2e` |

   `make go-test` is now `make go-test-race` then `make go-test-cover`, the
   same two passes in the same order, so `make check` is unchanged. On one
   node the script runs every part in the old order; on any node count but
   1 and 3 it fails, so a change to the parallelism cannot drop a part.
   `python/tests/test_ci_config.py` runs the script for every node with a
   stand-in make and checks that the nodes together run exactly the old
   targets, each once.

2. **Each node keeps its own Go build cache**, keyed by
   `CIRCLE_NODE_INDEX`, because each compiles different things
   (race-instrumented packages, coverage-instrumented packages, vet and
   lint). With one shared key the first node to finish would save its cache
   and the others would compile from cold every week.

3. **`make tools` keeps a generator built from its pin.**
   `scripts/install-tool.sh` reads the cached binary with `go version -m` and
   keeps it only if its main package and module version are the pin's;
   anything else (missing, another version, another package, a local build,
   an unreadable file) is rebuilt with `go install`, as before.

## Consequences

- The critical path is the slowest go node plus its setup, instead of all
  four parts in series. [Pipeline time](../ci.md#pipeline-time) in
  docs/ci.md has the before and after times.
- `proto` is no longer the second-longest job: its generator step drops from
  about 150 s to a few seconds on a cache hit, which saves about 25 credits a
  full run.
- The go job sets up three times (checkout, Go, python[dev], caches, about
  30 s each), which costs about 10 more credits a full run than one node did;
  the proto saving more than covers it. A run where `go` is not implicated
  starts three skip-note containers instead of one, a few seconds each.
- The coverage report `go/cover.out` is stored by node 1, which is the node
  that writes it.
- Locally nothing changes: `make check`, `make go-test` and the pre-push hook
  run the same commands as before.

## Alternatives rejected

- **Separate jobs for the race, coverage and lint parts.** New check names,
  and keeping `go` as the required check would need a gate job that
  `requires` the parts, which runs after them and lengthens the path it was
  meant to shorten.
- **A large resource class.** docs/ci.md keeps the pipeline to the Free
  plan's `small` and `medium` classes, and more CPUs would not shorten the
  parts' slowest packages (internal/e2e alone takes over a minute in each
  pass), which bound each part however many CPUs run it.
- **Splitting the race pass across two more nodes by package.** The race
  node is still the longest (about 220 s); splitting it would bring the
  pipeline near the coverage node's 140 s, for another node's setup and a
  package list to keep complete. Left for later, if the time matters.
- **pytest-xdist in the Python jobs.** They run on `small` (1 CPU) and are
  not on the critical path.
