# ADR 0007 — Logging coordinator decisions under the lock

**Status:** accepted · **Phase:** 3 · **Issue:** #24

## Context

A coordinator that crashes loses its task table, so the job starts over.
The paper's master writes periodic checkpoints (§3.3). Here, every decision
goes to a write-ahead log (`go/internal/wal`), so a restarted coordinator
can replay it and resume the job.

ADR 0006 keeps slow work out of the coordinator's one lock. A synced
append takes milliseconds, and something has to order the appends.

## Decision

1. **The log holds decisions, not state.** It records:
   - a job submitted, with its splits, which are not planned again;
   - an attempt assigned;
   - a worker's report, as received;
   - a worker lost.

   The registry is not logged: workers register again with a restarted
   coordinator. Replay calls the same code as the live handlers
   (`submit`, `Assign`, `report`, `ResetWorker`), so the two cannot drift
   apart. A restart is logged too (`Restarted`), so a second restart
   replays the first. Only decisions are replayed, never the policy that
   made them: a backup attempt is logged as an ordinary assignment, and
   `Assign` accepts it on replay even if the restarted coordinator makes no
   backups.
2. **Appends happen under the lock, before the reply.** A handler changes
   the state, appends the decision, and only then replies. No one sees the
   change before the log holds it, because no one can read the state until
   the lock is released. A submitted job is logged before it is created.
3. **A failed append stops every RPC until the coordinator rebuilds itself
   from its log.** The state is then ahead of the log, such as a job ended
   by a report the log never took, so the log must keep failing (`*wal.Log`
   does) and every RPC, reads included, fails with `Unavailable`. At most
   once a second, an RPC instead reopens the log and recovers from it in
   place, as a restart would: the change the log never took is dropped,
   running attempts stop, and workers register again. A full disk thus
   stops the cluster only until space is freed, with no one restarting
   the coordinator. A coordinator given a log without a path, as in tests,
   stays stopped until restarted.
4. **The log keeps only the jobs the coordinator keeps** (#178). When
   `-keep-jobs` makes the coordinator forget ended jobs, it rewrites the
   log under the lock without their records (`wal.Log.Compact`), and once
   more at startup, after replay. The new log is written to a temporary
   file and renamed over the old one, so a crash leaves one or the other;
   `wal.Open` deletes a temporary file a crash left behind. Every
   `Restarted` record is kept, since worker IDs are numbered by them, and a
   `WorkerLost` record is kept with the job submitted before it, the only
   one it can have applied to. Job IDs are numbered from the highest ID
   replayed, not from a count of jobs, so a compacted log never leads to a
   reused ID. A failed compaction is logged; if it failed after the
   rename, the log refuses appends, as in 3.

## Alternatives rejected

- **Append outside the lock.** Appends would then need their own order, and
  a reply could reveal a change that a crash loses.
- **Snapshots of the whole state.** Snapshots are smaller to replay, but
  each one is a full copy. A job's log is about M + R + retries records,
  so snapshots can wait until logs grow large.
- **bbolt.** bbolt brings a dependency and a B-tree, when an append-only
  log is all this needs.

## Consequences

- This is a second exception to ADR 0006's "no I/O under the lock". Each
  decision costs one fsync while the lock is held, so the coordinator
  handles at most a few hundred decisions per second. That suits this
  project's scale. Batching several appends per fsync would raise the
  limit if needed.
- Logging a report that changes nothing, such as a duplicate, is
  harmless: replaying it changes nothing either.
- The log's size follows `-keep-jobs` (100 by default), not the number of
  jobs ever run. Each compaction rewrites the kept jobs' records with one
  fsync, under the lock, once per job that ends past the limit.
