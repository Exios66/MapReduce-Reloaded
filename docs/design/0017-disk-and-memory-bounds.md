# ADR 0017 — Workers delete ended jobs' map output; disk, memory and logs are bounded

**Status:** accepted · **Phase:** course deliverable

## Context

A worker kept every job's map output in its work directory until the worker
exited. Under Compose that directory is inside the container, so a cluster
left running grew without limit: during experiment E3 on the course VM, four
workers held 14 GB of map output from earlier runs and filled the 33 GB root
filesystem, which broke the experiment and could break other coursework on
the same VM. The paper deletes intermediate files once a job is done
(§3.1 step 7). Other things grew too: the coordinator kept every job it had
run in memory, the job-file cache emptied itself completely when full,
Docker kept every container log, and Prometheus kept 15 days of samples.

## Decision

**Ended jobs' map output is deleted.** Each `HeartbeatResponse` carries
`active_jobs`, the jobs that have not ended. A worker deletes each `job-N`
directory in its work directory that is not active, unless it was written
after the heartbeat was sent, or less than 3 s before it (some file systems
keep times in whole seconds): that directory may belong to a job submitted
after the coordinator answered. An unset `active_jobs` (an older
coordinator) deletes nothing. The delete runs outside the heartbeat loop, one
at a time, so a large one cannot delay a heartbeat. It counts
`mr_worker_pruned_jobs_total` and `mr_worker_pruned_bytes_total`. A
coordinator restarted without its log has forgotten the job, so its map
output goes too, which is right: nothing can read it any more.

**A worker low on disk stops taking tasks.** Before each task request it
checks the work directory's free space; under `-min-free-mib` (default
1024) it waits, still sending heartbeats so it is not declared dead, and
counts `mr_worker_disk_waits_total`. While waiting, it also polls
`RequestTask` with `paused=true`, which returns wait or shutdown without
assigning work, so coordinator shutdown remains reachable. A nearly full disk then stalls the
job instead of failing task after task. The check is made before a task,
not while it runs, so the margin must hold one task's output (a map task's
spills or a reduce task's fetched input); a task that still runs out of
space fails and is retried, on this worker once space is back or on
another. `mr_worker_disk_free_bytes` shows the space.

**The coordinator forgets old jobs.** It keeps the `-keep-jobs` (default
100) most recent ended jobs for `mr status` and the status page; older ones
are forgotten, oldest first; 0 keeps all of them, and a negative value is
rejected. Job IDs come from a counter, so a forgotten
job's ID is never used again.

**The job-file cache is an LRU** of 16 programs, instead of emptying itself
when full.

**A dead worker's directory is deleted by the next worker to start.** A
worker without `-work-dir` keeps its map output in a new temporary
directory, removed when it exits cleanly. A killed worker cannot remove it,
and its restarted process makes another, so pruning, which looks only in
its own directory, never sees the old one: on the course VM, nine E3 kills
left 1.9 GB in one worker container. Each worker now holds a lock
(`flock`) on a file in its directory while it runs; a starting worker
deletes every `mr-worker-*` directory whose lock it can take, since the
kernel drops a lock when its process dies. A directory without the lock
file is kept: its worker may still be starting. Nothing reads a dead
worker's output, since the coordinator reruns its map tasks (§3.3).

**Experiments prune what they leave.** `bench/experiments.sh` moves an
experiment's earlier results to `bench/results/previous/`, keeping the last
three, so a rerun does not overwrite them, and deletes from the output
volume the outputs of its interrupted runs and the inputs no run of the
current size reads. Results that are reported are copied to
`bench/evaluation/` and committed.

**Compose bounds logs and metrics.** Every container's log rotates at
10 MB, three files kept, and Prometheus has a 512 MB retention target.
WAL and head chunks may temporarily exceed that target, so operators must
reserve additional free space ([Prometheus storage](https://prometheus.io/docs/prometheus/latest/storage/)).
`make clean-output CLEAN_OUTPUTS='completed-directory'` deletes only
explicitly selected completed output directories from the output volume.
Inputs, the write-ahead log, and unselected outputs stay; active job outputs
must remain unselected.

## Consequences

- A long-running cluster, such as the demo, uses disk for the current job
  only, plus job output, which stays until the user deletes it.
- Map output of an ended job cannot be inspected afterwards. Tests that
  read it set `Worker.KeepEndedJobs`.
- The write-ahead log (`-data-dir`, on by default in Compose) holds the
  `-keep-jobs` jobs and the running one: forgetting a job drops its
  records (ADR 0007, #178). One log record per task event is small next
  to job data, and the log is deleted with the output volume.
- If every worker is short of disk while a job runs, the job waits until
  space is freed. The worker's warning log and its metrics say why.

## Alternatives considered

- **Restart workers between jobs.** A graceful stop already deletes the
  work directory. It works for the benchmark harness, which does it before
  each run, but a cluster in use should not need restarts.
- **Delete on the worker when its reduce tasks finish.** A worker does not
  know when every reduce task of the job has read its output; only the
  coordinator does.
- **Evict the oldest map output of running jobs when the disk is full.** The
  coordinator would then rerun the lost map tasks (§3.3), which is correct
  but turns a full disk into repeated work. Waiting is simpler and visible.
