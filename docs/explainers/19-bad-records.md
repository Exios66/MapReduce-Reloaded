# Explainer — Skipping Bad Records (#26)

A bug in user code that crashes on one rare record would fail its task on
every attempt, and the job would never finish. Fixing the bug is not always
possible, for example when it is in a third-party library, and losing a
few records is often acceptable in a large job. So the paper lets a job
skip the records that crash it (§4.6).

## What happens

| Step | Where |
|------|-------|
| The task runner numbers each record: an input record's number in its split, or a key group's number in a reduce task, counting from 0 | `task.Map`, `task.Reduce` |
| A panic in the user's code fails the attempt with a `CrashError` naming the record | `taskContext.call` |
| The worker reports that record as `failed_record` | `Worker.report` |
| Once `SkipAfter` (2) attempts have crashed on a record, the coordinator adds it to the task's skip list | `TaskTable.Fail` |
| Every later attempt of the task gets the list as `skip_records`, and the task runner skips those records | `Server.assign`, `taskContext.call` |

Skipped records are counted in `skipped.records`, which shows in the job's
counters.

## Choices

- **Only panics count.** A returned error may be the framework's, such as a
  full disk when emitting, so it says nothing about the record.
- **Two crashes, not one.** A single crash could have another cause, such as
  a machine running out of memory. Skipping after one would lose a good
  record.
- **The numbering must repeat.** A record keeps its number from one attempt
  to the next only because a split always yields the same records in the
  same order, and a reduce task always sees the same sorted keys.
- **Recovery needs nothing new.** Crash reports are in the write-ahead log,
  so a restarted coordinator rebuilds the same skip lists (ADR 0007).

The paper's worker catches the crash with a signal handler, and sends the
record's number in a last UDP packet before it dies. In Go, `recover` lets
the worker catch the panic and report it like any other failure.

## A limit

The paper sets no limit on how many records a job may skip, and by default
neither does this one. A job may set `max_skipped_records` (`mr submit
-max-skipped N`). Once more than N records are on its tasks' skip lists,
the job fails: its status becomes `FAILED`, with the reason in `error`, and
no more of its tasks are handed out. The check counts skip lists rather
than the `skipped.records` counter, so the job fails as soon as the crash
is reported, before any attempt skips the record.
