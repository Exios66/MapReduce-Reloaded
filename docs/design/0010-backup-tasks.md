# ADR 0010 — Backup tasks when nothing is idle

**Status:** accepted · **Phase:** 3 · **Issue:** #25

## Context

A job waits for its slowest task. The paper calls a machine that is slow to
finish its last tasks a straggler. When a phase is "close to completion",
the master starts backup executions of the tasks still in progress, and a
task completes when either copy does (§3.6). The paper does not say how
close "close" is.

## Decision

1. **A backup starts when no task of the current phase is idle.**
   `Server.RequestTask` first looks for an idle task. If there is none and
   `Config.Backups` is set, it calls `TaskTable.Backup(worker)`. A worker
   that would otherwise be told to wait gets a second attempt instead, and
   no backup ever delays a task that has not started. A worker held back
   from an idle map task for locality (`Config.LocalityWait`, explainer
   23) waits instead; it does not get a backup.
2. **One backup at a time, never on the same worker.** `canBackUp` accepts
   a task that is in progress with exactly one running attempt, on another
   worker. A task runs on at most two workers. If one attempt fails, the
   other carries on and the task can get a new backup.
3. **The oldest attempt is backed up first.** Of the tasks that can get a
   backup, `TaskTable.Backup` picks the one whose running attempt started
   first: it has run longest, so it is the likeliest straggler. This
   matters when fewer workers are free than tasks remain. Each attempt's
   start time is the time of its `TaskAssigned` record, so replay restores
   it.
4. **A backup is an ordinary attempt.** `TaskTable.Assign` gives it the next
   attempt number, and the write-ahead log records an ordinary assignment.
   Replay accepts it whatever the coordinator's `Backups` setting is.
5. **The first accepted completion wins.** `TaskTable.Complete` rejects a
   report for a task that is already completed. Both reduce attempts rename
   their file to the same `part-r-*`, and a rename is atomic. A losing map
   attempt's output stays on its worker, and no reducer is told about it.
6. **Backups are a setting.** `cmd/coordinator` enables them by default and
   `-backups=false` turns them off. `-speculation` chooses the policy
   (ADR 0019); empty, it follows `-backups`, so this decision stands. `coordinator.Config` leaves them off,
   so tests can count attempts exactly.
7. **Backups are counted.** The task table counts the backup attempts it
   starts and the tasks a backup completes first. `GetJobStatusResponse`
   reports them (`backup_attempts`, `backup_wins`), as do the status page,
   `mr status` and `mr_job_backups{job,state}` on `/metrics`, so the cost
   and the effect of backups can be measured (§5.3).

## Alternatives rejected

- **A threshold such as the last 5% of tasks.** It needs a number the paper
  does not give and that depends on the job's size. "Nothing idle" needs no
  tuning, and by then the free workers have nothing better to do.
- **Back up on measured slowness.** It needs a rule for what counts as
  slow, and the paper's mode does not have one: how long an attempt has run
  is the measure it uses. Workers report each attempt's `fraction_done` in
  their heartbeats (ADR 0009), and a LATE-style rule built on it is
  available as a separate opt-in mode, `-speculation=late` (ADR 0019, #45).
  The rule above stays the default and is what `-backups` means.
- **Back up the first task by index.** It was the rule until the start
  times were logged. It can back up a task that has just started while an
  older one waits.
- **Allow several backups per task.** One straggler would cost many extra
  attempts, and a second attempt on a healthy worker already bounds the
  delay.

## Consequences

- Idle workers do duplicate work at the end of a phase. That costs
  capacity that would otherwise be idle, but not time.
- A losing attempt is stopped at its worker's next heartbeat, whose reply
  lists it in `cancel`; the worker checks between records and groups, and
  does not report it. Until then it keeps running. A losing reduce attempt
  still running when the job ends fails, because the job has removed
  `_temporary` and attempts never create it, so it leaves nothing. A losing
  map attempt writes to its worker's work directory and can finish there,
  leaving partition files that no reducer reads.
- Output must be deterministic for both attempts to agree (§3.3).
