# Explainer — When the Coordinator Dies (#24)

The paper's master writes periodic checkpoints, but its implementation
simply aborted the job if the master died (§3.3). Here, a coordinator run
with `-data-dir` logs every decision. A restarted coordinator replays the
log and carries on with the job.

## What is logged

| Record | Written when |
|--------|--------------|
| `JobSubmitted` | a job is accepted, with its splits |
| `TaskAssigned` | an attempt starts on a worker |
| `ReportTaskRequest` | a worker reports an attempt, as received |
| `WorkerLost` | a worker misses its heartbeats |
| `Restarted` | a coordinator starts from the log |

Each record is appended and synced before the reply that depends on it
([ADR 0007](../design/0007-write-ahead-log.md)). The worker registry is not
logged: workers register again.

## What a restart keeps

| State | After a restart |
|-------|-----------------|
| completed reduce tasks | done: their output is in the output directory |
| completed map tasks | done: their output is still on the workers |
| running attempts | stopped, and their tasks run again |
| attempt numbers | kept, so no file name is reused |
| worker IDs | new ones, such as `r2-w1`, so old reports are not trusted |

Only the tasks whose state was lost run again. If a map task's worker also
died, the reducer that cannot fetch its output reports it, and the map task
runs again (see [15-shuffle.md](15-shuffle.md)).

## Workers

A worker that cannot reach the coordinator waits a second, then registers
again. Registering waits until the coordinator is back. A worker that the
restarted coordinator does not know is told to register again, as after an
expiry ([16-worker-failure.md](16-worker-failure.md)).
