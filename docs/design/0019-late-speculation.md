# ADR 0019 — LATE speculation beside the paper's backup tasks

**Status:** accepted, built (#45); evaluation pending (#59) · **Phase:** stretch

## Context

ADR 0010 starts a backup attempt as the paper does (§3.6): when no task of
the current phase is idle, a free worker gets a second attempt of the task
whose attempt started first. That rule has two limits.

- It acts only when nothing is idle. A task that is slow while others are
  still waiting is not backed up until the phase is nearly over.
- It ranks by age, not by how slow. The oldest attempt may be a healthy one
  on a fast worker that was simply handed a big task early. On workers of
  different speeds, a free slow worker can also be given the backup, and
  then the backup is slower than the original.

Zaharia et al., "Improving MapReduce Performance in Heterogeneous
Environments" (OSDI 2008) call this out and propose LATE (Longest
Approximate Time to End): estimate when each running task will finish, back
up the one that finishes last, and keep backups off slow nodes. Workers now
report each attempt's `fraction_done` in their heartbeats (ADR 0009), which
is the input it needs. All Compose workers are identical unless the
experiments throttle them, so the paper's rule was enough for E2; whether
LATE does better on mixed speeds is open (#59).

## Decision

1. **Three modes.** `Config.Speculation`, set by `cmd/coordinator`'s
   `-speculation`, is `paper`, `late` or `off`.
   - `paper` is ADR 0010, unchanged.
   - `off` starts no backups.
   - `late` uses `TaskTable.LateBackup`, below.
   - Empty, the default, keeps the meaning of `-backups`: `paper` if it is
     true, `off` if false. `-backups=false` with `-speculation=paper` or
     `late` is a usage error. Any other value is too. The Go and Python
     coordinators make the same choices.
2. **LATE runs at the same moment as the paper's rule.** `RequestTask`
   tries an idle task first, and calls `LateBackup` only if there is none,
   so no backup delays a task that has not started. A worker held back for
   locality still waits instead.
3. **How progress is measured.** An attempt's fraction is the largest
   `fraction_done` its worker reported: bytes of the split read for a map
   task, bytes of map output read for a reduce task (ADR 0009). The
   coordinator keeps the larger of the stored and the new value, so a
   heartbeat that arrives late cannot lower it. Its rate is
   that fraction divided by the seconds since the coordinator assigned the
   attempt, and its estimated time left is `(1 - fraction) / rate`. A rate
   of 0 counts as an infinite time left. The paper scores a reduce task in
   thirds (copy, sort, reduce); the merge here fetches and reduces in one
   pass, so there are no thirds.
4. **`LateBackup`'s rules,** in order, with the defaults of Zaharia et al.
   except where noted:
   1. *Cap.* If the running backup attempts number at least
      `max(1, floor(0.1 × workers))`, none starts. Workers are those
      registered; the paper's 10% is of task slots, which are the workers
      here.
   2. *Slow-node refusal.* A worker's speed is the mean of `1/duration`
      over the attempts it completed in the job, for the kind of task in the
      current phase. A worker whose speed is below the 25th percentile of
      all workers' speeds gets no backup; a speed equal to it is not
      refused. A worker that has completed none is not refused. When a
      worker is declared lost (ADR 0009), its speeds are forgotten, so that
      dead workers do not count in the percentile. Speeds are kept apart for
      map and reduce tasks. (The paper scores a node by the work it has done;
      completed attempts are simpler and need no extra state.)
   3. *Candidates.* The tasks that could get a backup on this worker (ADR
      0010, decision 2) whose attempt has run at least `MinRuntime` and has
      reported progress once. `MinRuntime` is twice the heartbeat interval,
      6 s with the 3 s heartbeat. It is this project's choice, so that the
      first reports exist and a rate is not a guess from one tick.
   4. *Slow tasks.* Of the candidates, those with a rate at or below the
      25th percentile of the candidates' rates.
   5. *Choice.* Of those, the task with the largest estimated time left;
      ties go to the attempt that started first, then the lowest index.
   - The cap and the two percentiles are `Config` fields
     (`SpeculativeCap`, `SlowTaskPercentile`, `SlowNodePercentile`,
     `MinRuntime`). A zero field means its default. None is a flag.
5. **Percentile.** The linear interpolation between the closest ranks of
   the sorted values, at rank `p/100 × (n-1)`. With four candidates, the
   25th percentile lies between the slowest rate and the second slowest, so
   only the slowest qualifies; with one, that one does.
6. **No new log records.** A LATE backup is an ordinary `TaskAssigned`
   (ADR 0007), and replay applies the assignment, not the policy that made
   it, so a coordinator can be restarted with another `-speculation`. The
   counters are the same: `backup_attempts` and `backup_wins`, on the
   status page, `mr status` and `mr_job_backups` (ADR 0010, decision 7).
7. **After a restart, LATE starts cold.** Fractions and arrival times are
   in memory only. The restart stops every attempt and the workers register
   under new IDs (ADR 0007), so no task is a candidate until a new attempt
   has run `MinRuntime` and reported. Replay does rebuild the durations of
   attempts completed before the restart from the logged times, but they
   are under the old worker IDs, so the restart forgets them (as a lost
   worker's are forgotten). Every worker starts with no history and none is
   refused.

## Alternatives rejected

- **Replace the paper's rule with LATE.** The paper's rule is the faithful
  one (§3.6), and E2's results and the tests that pin it (`TestBackups`,
  `TestClusterBackupTasks`, the chaos runs) depend on it. Keeping both
  modes lets #59 compare them on the same code.
- **Hadoop's rule: a task is slow if its progress is 20% behind the
  average of its kind.** Zaharia et al. show why it fails on heterogeneous
  workers: it marks many tasks at once, and a fixed margin says nothing
  about which will finish last. LATE ranks by time left, and caps the
  copies.
- **A job-level setting.** It would go into the submit request and the
  log's `JobSubmitted`, a contract change (ADR 0003), for a choice the
  operator makes about the cluster, as with `-backups`. The experiments
  restart the coordinator with a Compose override anyway
  (`deploy/compose/speculation-late.yaml`).
- **Flags for the cap and the percentiles.** There are no measurements
  yet to say they need tuning; the `Config` fields are enough for tests.

## Consequences

- Nothing changes unless `-speculation=late` is set. Compose runs `paper`.
- **Not measured.** The unit and in-process cluster tests check which task
  LATE picks (`TestLateBackup*`, `TestSpeculationModes`,
  `TestClusterLateBackupTasks`), not that LATE finishes a job sooner. That
  is #59: experiment E7 in `bench/experiments.sh` runs the paper's rule,
  LATE and no backups on workers throttled to different speeds. It has not
  been run.
- LATE needs progress reports. An attempt whose worker has not reported
  yet is never a candidate.
- On four workers the cap is one backup at a time, and the slowest worker
  is refused backups once it has finished an attempt.
- The estimate assumes the task keeps the average rate it has had so far,
  so a task that starts slow and speeds up looks slower than it is.
