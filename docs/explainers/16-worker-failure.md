# Explainer — When a Worker Dies (#23)

## Noticing

Every worker sends a heartbeat each `HeartbeatInterval` (3 s), and asking
for a task counts as one too. The coordinator checks every interval, and a
worker unheard for `WorkerTimeout` (10 s, three missed beats) is declared
dead (§3.3). The paper's master pings workers; here workers send to the
coordinator, which needs no connection back to them.

## Giving its work back

| The dead worker's…  | Becomes | Why |
|---------------------|---------|-----|
| running tasks       | idle    | they will never report |
| completed map tasks | idle    | their output was on its disk |
| completed reduce tasks | done | their output is in the job's output directory |

Reduce tasks only start once every map task is done, so the reset map
tasks run again before any new reduce. A reduce already running elsewhere
fails when it cannot fetch the lost output (see
[15-shuffle.md](15-shuffle.md)), and its retry gets the new locations.

## Coming back

A worker the coordinator declared dead is not trusted again under its old
ID: `RequestTask` answers `NotFound` and `Heartbeat` answers `reregister`,
and the worker stops its attempt and registers anew. If it was only slow,
any report it sent first is for an attempt the coordinator has stopped
tracking, and is rejected.

## Counting once

Counters are kept per task, from its accepted attempt. A map task that runs
again after its worker died replaces its earlier counts instead of adding
to them, so every record counts once (§4.9).

## No half-written output

A reduce attempt writes `<output>/_temporary/part-r-<r>.attempt-<n>` and
renames it into place only when it succeeds. A worker that dies mid-write
leaves its file in `_temporary`, which readers skip, and the coordinator
removes the directory when the job finishes.

## How we know it works

`TestClusterSurvivesWorkerLoss` kills a worker (its heartbeats stop and
its shuffle server closes) in two places:

- **mid-map:** it dies running map task 0, and another worker runs it;
- **after map, mid-reduce:** it ran every map task, then dies running reduce
  task 0. Its map output is gone, so the map tasks run again, and the reduce
  tasks reading from it fail and run again.

Both runs match the golden output, with each map task's records counted
once.
