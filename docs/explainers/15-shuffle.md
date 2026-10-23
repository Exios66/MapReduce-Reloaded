# Explainer — The Shuffle (#18)

## The idea

A map task's output stays on the worker that ran it (§3.1 step 4). Each
reduce task then fetches its partition from every map task's worker over
RPC (step 5). Only locations go through the coordinator, never data.

```
worker A: map 0 ──► A's disk: map-00000-r-00000.kv, map-00000-r-00001.kv …
worker B: reduce 1 ──FetchPartition(job, map 0, partition 1)──► A
```

## The pieces

- **`shuffle.Server`** (every worker): `FetchPartition` opens
  `<work-dir>/<job>/map-<m>-r-<r>.kv` and streams it from the requested
  offset in 64 KiB chunks. Job IDs that could leave the work directory, such
  as `../x`, are rejected.
- **`shuffle.Reader`** (reducers): an `io.Reader` over that stream, so the
  usual `kv.NewReader` reads records from it. Memory is bounded on both
  ends: one chunk in flight, plus one MRKV block in the reader.
- **The worker** advertises its shuffle address at registration and puts it
  in every `PartitionLocation` it reports. A reducer fetches from
  `worker_address`, or reads `uri` from shared storage when it is empty
  (`worker -listen ""` with a shared `-work-dir`).

## When a fetch breaks

On a transient error (`Unavailable`, `ResourceExhausted`, `Aborted`) the
reader reconnects and asks for the file from the byte it got to, waiting
100 ms, 200 ms, 400 ms, 800 ms. Any chunk that arrives resets that budget;
once four retries in a row make no progress, the next failure ends the
fetch. Resuming keeps one
contiguous stream from byte 0, so the file's block checksums and footer
still check everything the reducer read. Errors that retrying cannot fix,
such as `NotFound`, end the fetch at once.

The reduce task then fails, and the coordinator runs it again. Telling a
dead worker from a slow one, and re-running its map tasks, is #23.

## Cleaning up

A worker without `-work-dir` uses a temporary directory and removes it,
with all its map output, when it exits. Removing one job's files as soon as
that job finishes needs the coordinator to tell workers, and comes later.
