# Explainer — Backup Tasks (#25)

A job is only as fast as its slowest task. A machine with a bad disk, a
busy CPU or a misconfigured cache can take far longer than the others to
finish its share, and the whole job waits for it. The paper calls such a
machine a straggler. Near the end of each phase, it runs a second copy of
the tasks still in progress, and takes the result of whichever finishes
first (§3.6). Disabling backups made one of the paper's sort jobs 44%
slower.

## What happens

| Step | Where |
|------|-------|
| A worker asks for work and no task of the current phase is idle | `Server.RequestTask` |
| The coordinator offers it the first in-progress task that has one attempt, running on another worker | `TaskTable.Backup` |
| The backup is an ordinary attempt, with the next attempt number | `TaskTable.Assign` |
| The first attempt to report success wins; the other's report is rejected | `TaskTable.Complete` |

Backups are on in `cmd/coordinator` and can be turned off with
`-backups=false`. `coordinator.Config` leaves them off, so that tests can
count attempts exactly.

## Choices

- **"Near the end" means "nothing idle".** The paper does not say when a
  phase is near its end. Here it is when every task of the phase has been
  handed out, so a worker that would otherwise wait does a backup instead.
  Backups never delay a task that has not started.
- **One backup at a time.** A task runs on at most two workers, so one
  straggler costs one extra attempt. If either attempt fails, the other
  carries on, and the task may get a new backup.
- **Never on the same worker.** A second attempt on the slow machine would
  not help.
- **Two winners are safe.** Both attempts of a reduce task rename their
  file to the same `part-r-*`, and the rename is atomic. A deterministic
  reducer writes the same bytes either way (§3.3). A losing map attempt's
  output stays on its worker's disk, and no reducer is told about it.
- **The log is unchanged.** A backup is logged as an ordinary assignment.
  Replay applies the assignment, not the policy that chose it, so a
  coordinator restarted with backups off still recovers a job that had
  some (ADR 0007).

## What is not promised

A losing reduce attempt keeps running until it finishes (#121). If it is
still running when the job ends, it fails: the job has removed `_temporary`,
and attempts never create it (`task.Setup`, #29). It leaves nothing behind.
If it finishes first, it renames over the winner's file, so the reducer must
be deterministic (§3.3).
