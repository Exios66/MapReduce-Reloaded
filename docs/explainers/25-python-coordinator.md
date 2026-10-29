# Explainer — The Python Coordinator (#36)

The Go coordinator is the one the Compose cluster runs. Python has a second
one, `python -m mapreduce_reloaded.coordinator`, written from the same
contract. It speaks the same protocol, so the Go workers and the `mr`
command cannot tell the two apart. It exists to check that the contract in
`proto/` is enough to build a coordinator from, without reading the Go one
([ADR 0001](../design/0001-languages-and-shared-contract.md)).

It is a coordinator only. There is no Python worker (#37) and no Python host
for user functions (#38); the workers in any cluster are the Go ones.

## What is shared with the Go coordinator

| Part | Python | How it matches Go |
|------|--------|-------------------|
| Messages and RPCs | `gen/`, generated from `proto/mapreduce/v1` | The same `.proto` files; `make proto` regenerates both languages |
| Flags and defaults | `coordinator/__main__.py` | `-addr`, `-http`, `-data-dir`, `-backups`, `-speculation`, `-keep-jobs`, `-max-task-failures`, `-log-format`, with Go's names and timings (3 s heartbeat, 10 s worker timeout) |
| Job validation | `coordinator/wire.py` | The checks and wording of `go/pkg/api` `JobSpec.Validate` |
| Task table | `coordinator/tasktable.py` | Mirrors `go/internal/coordinator/tasktable.go` |
| Worker registry | `coordinator/registry.py` | Mirrors `registry.go`; a worker that is expired registers again under a new ID |
| RPC service | `coordinator/server.py` | Mirrors `server.go`: one lock for all state ([ADR 0006](../design/0006-coordinator-concurrency.md)), one job at a time |
| Recovery | `coordinator/recover.py`, `wal.py` | Mirrors `recover.go` and `go/internal/wal` |
| Status page and metrics | `coordinator/statusweb.py` | Mirrors `go/internal/statusweb` |

## Task table and recovery

`TaskTable` holds each task's state, its running attempts, where a completed
map task's output lives, the records that crashed attempts and the records to
skip (`SKIP_AFTER` is 2, as in Go, [explainer 19](19-bad-records.md)). The
first successful report for a task wins and later ones are rejected
([explainer 13](13-task-table.md)). When a worker is lost, `reset_worker`
makes its running tasks idle, and the completed map tasks whose output lived
there run again, as in [explainer 16](16-worker-failure.md).

With `-data-dir`, the coordinator writes every decision to a write-ahead log
before it replies, as the Go one does ([explainer 18](18-coordinator-recovery.md)).
The log is `coordinator.log` in that directory, under a `coordinator.lock`
that stops a second coordinator from using it. `wal.py` frames records the
way `go/internal/wal` does: a little-endian length, a CRC-32C and the
payload. Its docstring says the aim is that either coordinator can recover the
other's log, and `tests/test_wal.py` pins the bytes of a one-byte record's frame. No test makes one
coordinator recover a log the other wrote, so that is a design aim, not a
checked property.

`recover()` replays the log through the same code the live handlers use,
then logs a `Restarted` record, which stops the attempts that were running.
Workers register again under IDs of the form `rN-wM`. If the log fails to
accept a record, for example on a full disk, every RPC answers `UNAVAILABLE`
and the server rebuilds itself from the log once it takes records again
(`Server._heal`). `-keep-jobs` bounds both the jobs in memory and, through
`Server._compact`, the size of the log.

## Status page

`statusweb.py` serves the page at `/` and the metrics at `/metrics` from a
copy of the coordinator's state, as in [explainer 24](24-status.md). The page
refreshes every 2 seconds and accepts `?theme=dark`. The metrics have the Go
coordinator's names and labels, so the Grafana dashboard reads either
(`mr_workers`, `mr_job_tasks`, `mr_job_backups` and the rest). Only the
coordinator's metrics exist here; the workers' own `/metrics` are the Go
workers'.

## Backup tasks

Both of the coordinator's speculation modes are ported. `TaskTable.backup`
is the paper's rule, the default ([explainer 20](20-backup-tasks.md)).
`TaskTable.late_backup` is LATE ([ADR 0019](../design/0019-late-speculation.md)),
chosen with `-speculation=late`, and uses the progress that Go workers send
in their heartbeats. The defaults are the same as Go's: 10% of the workers,
the 25th percentile for tasks and for workers, and a minimum runtime of twice
the heartbeat interval. `-backups=false` with `-speculation=paper` or `late`
is refused, as in Go.

## What it does not do

- **Run tasks.** Workers run map and reduce tasks; the coordinator never
  touches user code. A job that names a built-in program, or carries a job
  file, is accepted if its spec is valid. `wire.py` cannot load a job file,
  so one that does not compile fails its first attempt on the worker, and
  with it the job (#198).
- **Serve Python programs.** A program written with the Python `@mapper` and
  `@reducer` decorators runs in Python's local mode only. The Go workers
  cannot call it (#37, #38).
- **Find where data lives.** `config()` in `__main__.py` sets no `locate`
  function, so there is no locality source, as in the Compose cluster
  ([explainer 23](23-locality.md)).
- **Run several jobs at once.** A job submitted while another runs is
  rejected, as in Go.

## How `make py-e2e` shows it works

`make py-e2e` runs `python/tests/test_coordinator_e2e.py` with `MR_REQUIRE_GO=1`.
The test builds the Go `worker` and `mr` commands, starts the Python
coordinator as a process (`python -m mapreduce_reloaded.coordinator`) on a free
port, starts three Go workers against it, and drives it with the Go `mr`.
The scenarios are those of `go/internal/e2e/process_test.go`, ported:

| Test | What it shows |
|------|---------------|
| `test_word_count` | Word count over the corpus equals the golden output, with the expected counters; `SIGTERM` stops every process cleanly |
| `test_examples` | Each example program gives the expected output in `testdata/examples/` |
| `test_worker_killed` | A worker killed with `SIGKILL` mid-job does not change the output |
| `test_coordinator_restart` | A coordinator killed mid-job and restarted on the same `-data-dir` finishes the job with the right output |
| `test_backups_finish_a_straggler` | A sleeping map task is finished by a backup that wins |
| `test_bad_records_are_skipped` | A record that panics is skipped after two crashes |

Without Go, these tests skip. `MR_REQUIRE_GO=1` turns a skip into a failure,
and CI's `go` job runs the target, so the check runs on every pull request
([CI](../ci.md)).

## What is not promised

- The end-to-end tests start the coordinator with `-http ""`, so they do not
  exercise the status page. `tests/test_coordinator_status.py` does, without
  a cluster.
- LATE is tested against the task table and the server in
  `tests/test_coordinator_state.py` and `tests/test_coordinator_server.py`.
  No end-to-end test runs it with Go workers, and experiment E7, which
  compares it with the paper's rule, is wired to the Go coordinator and has
  not been run.
- The Python coordinator is not in the Compose project. `make e2e` and the
  experiments use the Go one.
