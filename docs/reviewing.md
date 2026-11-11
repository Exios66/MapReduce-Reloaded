# Reviewing the code

A path through the repository for an engineer reviewing it: what to read
first, where each part of the system lives, and how to check a claim
yourself. [The report](report.md) is the summary; this page is the map.

## In thirty minutes

1. [design.md](../design.md): the assignment's questions answered, one job
   from submission to completion, and the differences from the 2004 paper.
2. [`proto/mapreduce/v1/coordinator.proto`](../proto/mapreduce/v1/coordinator.proto):
   every RPC between the client, the coordinator and the workers. The
   coordinator never calls a worker; workers ask for tasks and report.
3. [`go/internal/coordinator/tasktable.go`](../go/internal/coordinator/tasktable.go):
   the master's state (paper §3.2), every task's state and worker, and the
   rules for assigning, re-running and backing up tasks.
4. [`go/internal/worker/worker.go`](../go/internal/worker/worker.go): the
   loop a worker runs, from registering to reporting a task.
5. [`go/internal/task/`](../go/internal/task/): one map or reduce attempt,
   the same code in local mode and on the cluster.
6. [`examples/wordcount.go`](../examples/wordcount.go) and
   [`go/pkg/api`](../go/pkg/api/): what a user writes.

## Where each part lives

| Concern | Code | Explainer or record |
|---|---|---|
| Client: submit, status, cat, local runs | `go/cmd/mr` | [Explainer 14](explainers/14-cluster.md) |
| Coordinator state and scheduling | `go/internal/coordinator` (`tasktable.go`, `server.go`, `registry.go`) | [13](explainers/13-task-table.md), [16](explainers/16-worker-failure.md), [20](explainers/20-backup-tasks.md) |
| Coordinator recovery | `go/internal/wal`, `go/internal/coordinator/recover.go` | [18](explainers/18-coordinator-recovery.md), [ADR 0007](design/0007-write-ahead-log.md) |
| Splitting the input | `go/internal/split` | [12](explainers/12-splits.md) |
| Map, sort, combine, partition | `go/internal/task`, `extsort`, `combine`, `partition` | [9](explainers/09-external-sort.md), [10](explainers/10-combiner.md), [5](explainers/05-partition-hash.md) |
| Shuffle between workers | `go/internal/shuffle` | [15](explainers/15-shuffle.md) |
| Output commit, local disk or S3 | `go/internal/storage` | [ADR 0013](design/0013-commit-on-object-storage.md) |
| Job files interpreted by the workers | `go/internal/jobfile` | [ADR 0015](design/0015-user-job-files.md) |
| File format (MRKV) | `go/internal/formats/kv` | [File format](file-format.md) |
| Status page and metrics | `go/internal/statusweb`, `promtext` | [24](explainers/24-status.md) |
| Python reference: API, local mode, coordinator | `python/mapreduce_reloaded` | [25](explainers/25-python-coordinator.md), [ADR 0001](design/0001-languages-and-shared-contract.md) |

The [design records](README.md#design-records) give each decision with its
alternatives; the [paper mapping](paper-mapping.md) names the code and the
test for each of the paper's features F1–F24.

## Checking a claim yourself

| Claim | How to check it |
|---|---|
| The tests pass | `make check`: protobuf, lint, Go and Python tests, coverage floors. On a pull request, CI runs the jobs whose inputs it touches; the other required jobs pass after a skip note ([CI](ci.md)) |
| The cluster's output is correct | `make e2e`: word count on the Compose cluster, compared byte for byte with `testdata/corpus/expected` |
| Failures do not change the output | `make chaos RUNS=5 SEED=1`: seeded worker kills, pauses and coordinator crashes; a failing seed replays with `scripts/chaos.sh 1 SEED` |
| Go and Python agree | [`testdata/`](../testdata/README.md): the file format, hash, message and example-output vectors both test suites read; `make py-e2e` runs the Python coordinator with Go workers |
| The paper mapping is accurate | `(cd go && go test -count=1 ./internal/papermap)` checks each feature's status and fails if the mapping names a file or test that does not exist; the named code and tests are the evidence that a feature is built |
| The evaluation's numbers | `bench/evaluation/*/` holds each run's CSVs, `machine.json` with the commit, and logs; `python3 bench/plot-experiments.py bench/evaluation OUT` redraws every figure and prints every table |
| The tests check results, not only run code | `make mutate` mutates core packages (the partition hash, the external sort, and more) and reports the survivors ([Testing](testing.md)) |

## Known limits

Stated where they apply, collected here:

- **Four workers on one VM.** Fixed costs (startup, heartbeats, the
  10-second failure timeout) are a large share of a 10-second job, so
  failures cost more here than in the paper ([evaluation](evaluation.md)).
- **Backups start when no task of a phase is idle**, because the paper
  gives no threshold; with R equal to the workers, that is every job's last
  reduce tasks ([E2](../bench/README.md#evaluation-e0e7)).
- **LATE speculation** is built as an opt-in mode
  ([ADR 0019](design/0019-late-speculation.md)) but not yet evaluated.
- **Python has no worker**: Python runs local mode and the coordinator; Go
  workers run every cluster task ([future work](report.md#future-work)).
- **A job file is interpreted**, 2.3× slower than the compiled program when
  it uses the `api` helpers ([E5](../bench/README.md#evaluation-e0e7)).
