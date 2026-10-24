# ADR 0009 — Failure detection by worker heartbeats

**Status:** accepted · **Phase:** 3 · **Issue:** #23

## Context

The paper's master pings every worker periodically, and marks a worker
failed if it does not answer (§3.3). It then resets the worker's tasks:
running ones and completed map tasks go back to idle, since map output was
on the failed machine's disk. Completed reduce tasks stay done, because
their output is in the global file system.

This project's coordinator already serves every worker's RPCs, and it has
no need to dial back into them.

## Decision

1. **Workers send heartbeats; the coordinator does not ping.** Each worker
   calls `Heartbeat` every `HeartbeatInterval`, which `RegisterWorker`
   returns. `RequestTask` also counts as a heartbeat. `cmd/coordinator`
   sets the interval to 3 s and `WorkerTimeout` to 10 s, three missed
   beats.
2. **`Server.Monitor` calls `Expire` every interval.** `Registry.Expire`
   removes workers not seen for longer than the timeout. For each one, the
   coordinator logs `WorkerLost` (ADR 0007) and calls
   `TaskTable.ResetWorker`:
   - its running attempts stop, and their tasks become idle;
   - its completed map tasks become idle;
   - its completed reduce tasks stay completed.
3. **A dead worker is not trusted under its old ID.** `RequestTask` for
   an expired ID answers `NotFound`, and the worker registers again and
   gets a new ID. `Heartbeat` answers `reregister` too, but the worker
   does not act on it yet (#121).
4. **Late reports are rejected.** `Complete` and `Fail` accept an attempt
   only if it is still running on the reporting worker. A slow worker's
   report for a reset task is ignored, so a task's counts and output come
   from one accepted attempt.

## Alternatives rejected

- **The coordinator pings workers.** It would need a connection back to
  every worker. With heartbeats it needs none.
- **Treat a failed fetch as the only signal.** A dead worker would go
  unnoticed until a reducer needed its output, and its running task would
  hold its slot until then. The fetch report (ADR 0008) stays as a second
  signal.
- **Trust a returning worker's old ID.** Its attempts were already given
  to other workers. Accepting its reports would need a rule for which of
  two results to keep.

## Consequences

- Detection takes up to `WorkerTimeout` plus one monitor interval. A
  worker that only pauses longer than that is treated as dead, and does
  extra work when it registers again.
- Reduce tasks start only after all map tasks are done, so reset map tasks
  run again before any new reduce task starts. A running reduce task that
  cannot fetch the lost output fails and is retried with new locations.
- The coordinator remembers the last 100 lost workers for the status page.
  They are not rebuilt from the log after a restart.
