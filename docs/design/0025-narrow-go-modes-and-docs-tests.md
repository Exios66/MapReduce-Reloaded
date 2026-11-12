# ADR 0025: Narrow go parts for docs and shell scripts, documentation tests in lint

Date: 2026-11-12. Status: accepted. Amends ADR 0024 (the go job's narrow
mode). Every check still runs on the files that can break it.

## Context

After ADR 0024 the go job had two modes: the full mode, and a narrow mode
for PRs whose Go-side inputs were only `python/`. Three kinds of PR still
ran more than their files could break:

- **A README or paper-mapping change ran the whole Go suite.** Only two Go
  packages read those files: `cmd/mr` (its usage text must match the
  README) and `papermap` (it reads the mapping table). Both are in
  `make go-python-test`.
- **A shell-script change ran the whole Go suite.** `.sh` files and the git
  hook mapped to `run-go` so that `make sh-lint` would run, but no Go test
  reads a shell script.
- **A documentation change ran both Python jobs.** `test_documentation.py`
  compares guides with the configuration they describe (Compose limits, the
  CI job table, the examples list). It imports nothing from the package, but
  it lived in `python/tests`, so every guide it reads mapped to
  `run-python`.

The race node was also the slowest by far, partly because of two 80 MB heap
measurements (`TestSortMemoryBounded`, `TestReduceMemoryBounded`) that took
34 s and 17.5 s under the race detector against 3 s and 1.8 s without it.
Each runs in a single goroutine, so the detector has nothing to find in them.

## Decision

1. **Two more narrow parts.** `scripts/ci-params.py` gains `run-go-docs`
   (README.md, docs/paper-mapping.md) and `run-sh-lint` (`*.sh`,
   `.githooks/**`). Both are covered by `run-go`, as `run-go-python` is.
   `scripts/ci-go-job.sh narrow` reads the parts from `MR_CI_GO_NARROW`
   and runs `make go-python-test` on node 0 for python or docs, and on
   node 2 `make sh-lint` for sh and `make py-e2e` for python. A sh-only
   PR checks out the repository and installs only shellcheck. Narrow mode
   with no part is an error.
2. **Guard tests keep the narrow parts honest.**
   `go/internal/papermap/cimodes_test.go` fails if a Go test outside
   `cmd/mr` and `papermap` starts to read the README or `docs/`, or if any
   Go test reads a shell script. `python/tests/test_ci_config.py` checks
   that exactly one branch of the go job runs for every selection.
3. **The documentation tests move to `docs/tests`** and run in the lint
   job with `make docs-test` (pytest and PyYAML only). `docs/tests/conftest.py`
   fails if they read a tracked file that does not map to `run-lint`, as
   `python/tests/conftest.py` does for `run-python`. `make lint` and so
   `make check` run them.
4. **The heap measurements skip under the race detector** through a
   `raceDetector` constant set by build tags, and run in the coverage pass.
   `TestOnlyTheHeapMeasurementsSkipUnderTheRaceDetector` fails if another
   test uses the switch or the coverage pass gains `-race` or `-short`.

## A/B check of the go nodes

The question was whether the race node, after #358's e2e speedup, had
become shorter than the others. Measured on one machine pinned to two CPUs,
warm caches, `main` at `605430b` (A) against this branch (B):

| Part | A | B |
|---|---|---|
| `go-test-race` (node 0) | 203 s, 180 s | 156 s, 136 s |
| `go-test-cover` (node 1) | 103 s, 100 s | 111 s, 100 s |
| `go-lint` + `py-e2e` (node 2) | | 23 s + 49 s |

The race node is about 45 s faster and still the longest, about 40 s ahead
of the coverage node. The hypothesis is rejected: the nodes are not
unbalanced the other way, and no part needs to move.

## Consequences

- A README, paper-mapping or shell-script PR runs one or two short make
  targets in the go job instead of the whole suite; a guide-only PR runs
  the lint job instead of the Python jobs.
- The heap measurements run once per pipeline instead of twice. Their code
  still runs under the race detector through the packages' other tests.
- `make check` runs the same checks as before, plus the guard tests.

## Alternatives rejected

- **Dropping the shell scripts from the go job.** shellcheck must still run
  on them; the sh part keeps it in the same required check.
- **Keeping the documentation tests in `python/tests`.** Every guide would
  stay mapped to two Python jobs that test nothing it changes.
- **Rebalancing the go nodes.** The A/B run shows node 0 is still the
  longest and is one make target; splitting it is left to ADR 0024's
  alternative.
