# ADR 0011 — Skipping bad records after two crashes

**Status:** accepted · **Phase:** 3 · **Issue:** #26

## Context

A bug in user code that crashes on one record fails its task on every
attempt, and the job never finishes. The paper lets a job skip such records
(§4.6). Each worker installs a signal handler. When user code crashes, the
handler sends the record's sequence number to the master in a last UDP
packet. If the master sees more than one failure on the same record, it
tells the next attempt to skip it.

Go can catch a panic with `recover`, so no signal handler or second channel
is needed.

## Decision

1. **The task runner numbers records.** `task.Map` numbers input records in
   a split, and `task.Reduce` numbers key groups, both from 0. A split
   always yields the same records in the same order, and a reduce task
   always sees the same sorted keys, so a record keeps its number across
   attempts.
2. **Only panics count.** `taskContext.call` recovers a panic in user code
   and returns a `*task.CrashError` naming the record. A returned error
   may be the framework's, such as a full disk, so it says nothing about
   the record.
3. **The failure report carries the record.** `Worker.report` sets
   `TaskFailure.failed_record` in `ReportTask`. No other channel is used.
4. **Two crashes add the record to the skip list.** `TaskTable.Fail`
   counts crashes per record, and at `SkipAfter` (2) appends it to the
   task's list. `Server.assign` sends `TaskTable.Skip` as `skip_records`
   with every later attempt. A skipped record adds 1 to `skipped.records`.
5. **A limit is optional.** A job may set `max_skipped_records` (`mr submit
   -max-skipped N`). When `TaskTable.Skipped` exceeds it, the job fails
   with the reason in `error`. By default there is no limit, as in the
   paper. Separately, the coordinator's `-max-task-failures` (default 8)
   fails a job whose task fails that many times; it must exceed
   `SkipAfter`, so a bad record is always skipped before the cap is
   reached ([ADR 0015](0015-user-job-files.md)).

## Alternatives rejected

- **Skip after one crash.** One crash can have another cause, such as a
  machine out of memory, and a good record would be lost.
- **Count returned errors too.** The error may come from the framework, not
  from the record.
- **Fail the job at the limit by the `skipped.records` counter.** The
  counter changes only when an attempt skips the record, so the job would
  run another attempt first. The skip lists count as soon as the crash is
  reported.

## Consequences

- Crash reports are in the write-ahead log, so a restarted coordinator
  rebuilds the same skip lists (ADR 0007).
- A panic outside any record, such as in the combiner during the final
  spill, fails the attempt without a record, and is never skipped. A
  combiner panic during an earlier spill happens inside the mapper's call,
  so it is blamed on the record being emitted.
- Output depends on which records were skipped. The `skipped.records`
  counter shows the job's owner that it happened.
