# ADR 0006 — Coordinator state behind one mutex

**Status:** accepted · **Phase:** 2 · **Issue:** #15

## Context

The coordinator's state (§3.2) is the task table, the worker registry and
the job. Many goroutines touch it: one per RPC (RequestTask, ReportTask,
Heartbeat, GetJobStatus) plus a timer that expires silent workers. A single
RPC often reads and changes several parts at once: reporting a map task
checks that the attempt still runs on the reporting worker, then updates
the table.

There are two usual ways to make this safe: a lock, or an actor loop that
owns the state and receives requests on a channel.

## Decision

1. **One `sync.Mutex` guards all coordinator state.** Each RPC handler
   locks it, does its work, and unlocks before replying.
2. **`TaskTable` and `Registry` do no locking themselves.** They are plain
   data structures, documented as such; the coordinator owns the lock.
   With only one lock there is no lock order to get wrong.
3. **Nothing slow happens under the lock**: no I/O, no RPCs, no waiting.
   Every operation is a few map and slice updates. The one exception is
   removing a finished job's `_temporary` directory (#23): it runs once per
   job, on at most R files, and doing it under the lock means the job is
   never reported done before its output is clean. ADR 0007 adds a
   second: the write-ahead log's fsync.

## Alternatives rejected

- **An actor loop.** Equally safe, but every operation needs a request
  type, a reply channel and a case in the loop. For short, non-blocking
  updates it adds code without adding throughput.
- **A lock per structure.** An RPC that touches both the table and the
  registry would need both locks, in a fixed order, and could still see
  them change between the two.

## Consequences

- Handlers are easy to read and to test: the table and registry are tested
  directly, with no goroutines, and the race detector checks the handlers.
- All changes to the state are serialised. SubmitJob plans its splits
  before it takes the lock. It checks and creates the output directory
  under the lock, so that no other job can finish in between. At this project's scale (a handful
  of workers, thousands of tasks), each critical section takes
  microseconds, so this is not a bottleneck. The paper's master is also a single process (§3.2).
