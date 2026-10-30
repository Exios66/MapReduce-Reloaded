# ADR 0023 — A showcase service that observes the cluster

**Status:** proposed. The architecture below is agreed. What the page shows,
its presentation modes and its test plan are still being designed, and this
ADR is finished before the work merges. The rest of
[ADR 0022](0022-status-page-updates-itself.md) stands.

## Context

The status page answers "where is the job now?", but it is not built to show
the project to an audience. The course demo and the README captures need
something more:

- The most striking moment is a failure, and the status page is served by the
  coordinator, so it goes down when the coordinator does.
- The demo's first job lasts about a second, and the page updates every two
  seconds. Grafana scrapes every five.
- Workers count bytes in their own `/metrics` and do not report them to the
  coordinator, so the page has no rates.
- A capture depends on a live run: a bad take means running the job again.

The page must stay usable through an SSH forward from an offline VM, with both
coordinators, and without changing how the cluster runs.

## Decision

1. **A separate, read-only service.** `showcase` polls `/status.json` and each
   worker's `/metrics`. It cannot change a job, and the cluster runs the same
   with or without it.
2. **Go, with the standard library.** It lives in `go/cmd/showcase` and
   `go/internal/showcase`, and is built into the existing image.
3. **An optional Compose profile.** `make showcase-up` starts the `showcase`
   profile with a 128 MB limit, published only on `127.0.0.1:18781`.
   `cluster-up` and `e2e` do not start it.
4. **Frames.** Every 250 ms by default, the sampler makes one frame: a
   timestamp, what each source returned, and whether it answered. A source that
   does not answer is a gap, never a zero. Workers are found through the
   Compose DNS name `worker`, as Prometheus finds them.
5. **Recordings.** Frames are appended to a new
   `data/showcase/<start-time>.jsonl` each time the service starts, up to a size
   cap. At the cap, recording stops and the page says so; the live view goes on.
6. **Replay.** `showcase -replay <file>` serves a recording with no cluster
   running.
7. **One page, one renderer.** The service serves one self-contained page that
   renders only from frames, so live and replay share their code. As in ADR
   0022, the page loads nothing external.
8. **Short polling.** The page asks every 500 ms for the frames after the last
   one it has, with one request in flight at a time. The server keeps a bounded
   number of recent frames in memory.

## Alternatives rejected

- **Build it into both coordinators.** Events would be exact, but every feature
  would be built twice, byte rates would need a heartbeat change, the history
  would use memory in a 256 MB container, and the page would still fail with
  the coordinator.
- **Polish the Grafana dashboard.** It is the least code, but five-second
  scrapes miss short jobs, and replay would be weak.
- **Server-sent events or WebSockets.** ADR 0022's reason holds: a few readers
  polling do not need a held connection.

ADR 0022 rejected building the page from `/status.json` because it adds a
second renderer to keep in step with the Go and Python ones. This page does
build from data, but it is one renderer outside both coordinators, and it does
not replace their pages.

## Consequences

- There is a new service and a third port to forward over SSH.
- Very brief states can fall between samples.
- A failed scrape shows that a worker did not answer, not that it died, so the
  page must say "unreachable" until the coordinator declares the worker lost.
- The service depends on `/status.json` and the worker metrics, so tests must
  fix the fields it reads for both coordinators.
- If sampling proves too coarse, exact coordinator events can be added later.
  #332 proposes per-attempt history in `/status.json`, which the sampler would
  read as is if it merges.
