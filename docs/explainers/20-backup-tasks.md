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
| The coordinator offers it the in-progress task whose attempt started first, of those with one attempt, running on another worker | `TaskTable.Backup` |
| The backup is an ordinary attempt, with the next attempt number | `TaskTable.Assign` |
| The first attempt to report success wins; the other's report is rejected | `TaskTable.Complete` |

Backups are on in `cmd/coordinator` and can be turned off with
`-backups=false`. `coordinator.Config` leaves them off, so that tests can
count attempts exactly. `-speculation=late` swaps the choice of task for
LATE's, which uses the progress workers report (ADR 0019); this page is
about the paper's rule, the default.

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
- **Oldest first.** The attempt that has run longest is the likeliest
  straggler, so its task gets the first backup. Attempt start times come
  from the log, so a restarted coordinator keeps them.
- **Two winners are safe.** Both attempts of a reduce task rename their
  file to the same `part-r-*`, and the rename is atomic. A deterministic
  reducer writes the same bytes either way (§3.3). A losing map attempt's
  output stays on its worker's disk, and no reducer is told about it.
- **The log is unchanged.** A backup is logged as an ordinary assignment.
  Replay applies the assignment, not the policy that chose it, so a
  coordinator restarted with backups off still recovers a job that had
  some (ADR 0007).

## Seeing it

The coordinator counts the backup attempts it starts and the tasks that a
backup finished first. The status page shows them for each job, `mr status`
prints `backups` and `backups_won`, and `/metrics` has
`mr_job_backups{job,state="started"|"won"}`. `TestClusterBackupTasks`
checks that the stuck task was won by its backup.

## What is not promised

A losing attempt is stopped at its worker's next heartbeat, whose reply
lists it in `cancel` (#121); until then it keeps running. A losing reduce
attempt still running when the job ends fails: the job has removed
`_temporary`, and attempts never create it (`task.Setup`, #29). It leaves
nothing behind. If it finishes before it is stopped, it renames over the
winner's file, so the reducer must be deterministic (§3.3).
