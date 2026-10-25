# Explainer — Locality (#31)

Network bandwidth was the scarce resource in the paper's clusters. GFS keeps
each input block on three machines, and the master tries to run each map
task on one of them, or failing that on a machine on the same switch, so
that most input is read from local disk (§3.4).

## What happens

| Step | Where |
|------|-------|
| A worker registers with its host and zone | `WorkerInfo` |
| At submit, each split is labelled with the hosts or zones that hold it, and the labels are logged with the splits | `Config.Locate`, `JobSubmitted` |
| A worker asks for work and gets the idle map task nearest to it: host, then zone, then any | `Server.RequestTask`, `TaskTable.Next` |
| If the nearest is on another host, the worker may be told to wait | `Server.holdBack` |
| The attempt's locality is logged with its assignment | `TaskAssigned.locality` |
| The job status counts the completed map tasks that ran on their split's host, or zone | `GetJobStatusResponse` |

## Choices

- **Ranking, not filtering.** A worker always gets the nearest idle task,
  and ties go to the lowest index. Splits with no labels all rank the same,
  so a job without labels is scheduled as before.
- **Delay scheduling.** Picking the nearest task is not enough. A worker
  with no local split asks first and takes another host's split, because it
  is the nearest one it has. With `LocalityWait` set, the job holds back
  such tasks for a short time, in the hope that a worker on the right host
  asks first. The wait is per job, not per task. It starts when the job
  first holds a task back, and it ends when the job starts a task on its
  split's host. So no task waits longer than `LocalityWait` for a host that
  is not there. This idea is from Zaharia et al., "Delay Scheduling"
  (EuroSys 2010), not from the paper.
- **The locality is logged, not worked out again.** Worker IDs change when
  the coordinator restarts, and worker hosts are not logged. So replay could
  not tell where an old attempt ran.
- **Only accepted attempts count.** A task run again after its worker died,
  or a backup, counts once: where its accepted attempt ran.

## Seeing it

`GetJobStatus` counts a job's completed map tasks that ran on their split's
host and in its zone. `mr status` prints them as `map_host_local` and
`map_zone_local`, and `/metrics` has
`mr_job_map_tasks_local{job,level="host"|"zone"}`. Their share of the job's
map tasks is the percentage that ran near their data. A job with no labels
counts none, since no task has a host to be near.

## Testing

- `TestLocality` checks the ranking, and that the counts survive a restart.
- `TestLocalityWait` checks that a task is held back, and then given out.
- `TestClusterLocality` runs word count with 8 splits on 8 hosts.
  - With a worker on every host, at least 90% of map tasks run on their
    split's host. Without the wait, about one in eight does not.
  - With 3 workers, the job still finishes.
  - In both runs, the output equals the golden files.

## Limits

Nothing labels the splits yet outside tests. In the Compose cluster, every
worker reads the same shared volume, so there is no locality to exploit.
`Config.Locate` is the hook for an input format that knows where its data
lives, such as an object store's zones (#41).
