# ADR 0008 — Map output stays on the map worker and is pulled

**Status:** accepted · **Phase:** 2 · **Issue:** #18

## Context

A map task writes R partition files (§3.1 step 4). Each reduce task needs
its partition from every map task, so M × R files move between workers. The
paper's reducers read them from the map workers' local disks with remote
procedure calls (§3.1 step 5), and a reducer that cannot read one tells the
master, which runs that map task again (§3.3).

This project needs the same shape. It also needs the data path to survive
a slow or dead worker without a reducer waiting forever.

## Decision

1. **Map output stays on the disk of the worker that ran the map task.**
   Only locations go through the coordinator, as `PartitionLocation`
   messages in the map report and in each reduce assignment. No data does.
2. **Every worker runs `shuffle.Server`, and reducers pull.** The
   server-streaming RPC `FetchPartition(job, map, partition, offset)` sends
   the file from `offset` in `ChunkSize` (64 KiB) messages. `shuffle.Reader`
   is an `io.Reader` over that stream, so `kv.NewReader` reads from it
   unchanged. One chunk is in flight at a time. A job ID that could leave
   the work directory is rejected with `InvalidArgument`.
3. **A broken fetch resumes from the byte reached.** A stream that
   receives nothing for `IdleTimeout` (30 s) is cancelled and counts as
   `DeadlineExceeded`. After that or `Unavailable`, `ResourceExhausted` or
   `Aborted`, the reader reconnects with `Offset` set to what it has,
   waiting 100 ms, 200 ms, 400 ms, 800 ms. A chunk that arrives resets the
   count; after `MaxRetries` (4) in a row, the next failure ends the fetch.
   Other errors, such as `NotFound`, end it at once.
4. **A lost output is reported.** If a fetch ends with `Unavailable`,
   `NotFound` or `DeadlineExceeded`, the reduce attempt fails with
   `TaskFailure.lost_output` set. `TaskTable.Fail` makes that map task idle,
   but only if its output is still at the address reported.
5. **Shared storage is a second path.** With `worker -listen ""` and a
   shared `-work-dir`, a location has no `worker_address`, and the reducer
   opens `PartitionLocation.uri` as a file.

## Alternatives rejected

- **Map workers push to reducers.** The map task would need to know where
  each reduce task runs before it starts, and reduce tasks start only after
  all map tasks are done.
- **Send the data through the coordinator.** It would carry every byte of
  the shuffle and become the bottleneck that §3.2 avoids by passing only
  locations.
- **Restart a failed fetch from byte 0.** Resuming at the offset keeps one
  contiguous stream, so the file's block checksums and footer still check
  everything the reducer read, and a long fetch does not start over.

## Consequences

- A map worker that dies takes its output with it. The coordinator runs its
  completed map tasks again (ADR 0009), and a reducer that finds the output
  gone reports it, so neither path depends on the other.
- Memory is bounded on both sides: one chunk in flight, plus one MRKV block
  in the reader.
- Map output is removed only when the worker exits without `-work-dir`.
  Removing a job's files when it ends needs a message from the coordinator
  to the workers, which does not exist yet.
