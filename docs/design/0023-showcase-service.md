# ADR 0023 — A showcase service that observes the cluster

**Status:** proposed, and accepted once the written design is reviewed.
[ADR 0022](0022-status-page-updates-itself.md) stands for the status page.

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

## The page

One screen, meant to read from across a room:

- **Header:** the running job, or the latest if none runs, with its state,
  elapsed time, ETA and health lights for the sampler, coordinator and workers.
- **Progress:** map and reduce bars combine finished tasks with the reported
  fraction of running tasks. A task with a backup counts once, at its furthest
  attempt.
- **Throughput:** input, map output, shuffle and output rates, with failures
  marked on the chart, in the shape of the paper's Figures 2 and 3.
- **Workers:** one row per worker, with its running tasks and backups and a
  state: healthy, unreachable, lost or joined. An unreachable worker counts up
  in seconds until the coordinator declares it lost.
- **Timeline:** a plain-language feed of changes between frames. Each entry's
  time comes from the first frame that showed it.

The ETA appears once a job has about 10% progress and steady throughput, says
"estimating" before that, and is always marked approximate. Finished jobs sit in
a small strip, and clicking one switches the view. Charts break during an
outage. The page follows the system's light or dark setting.

## Modes

- **Live** is the default.
- **Stage** (`?stage=1`) uses larger type and hides the finished-job strip and
  the event detail.
- **Replay** adds play and pause, 1x, 2x and 4x speed, a scrubber and failure
  markers. It uses the recording's timestamps and is labeled "Replay".
- **Captures** use `?t=<seconds>&autoplay=1`, which opens a replay at a fixed
  moment so that takes repeat.

## Failure handling

| What happens | What the page does |
|---|---|
| Coordinator stops answering | Red light, an outage timer, and the last job state, labeled stale |
| Coordinator returns | The timeline gives the outage's length and says the job resumed; if the job is gone or differs, it says so |
| Worker scrape fails | The worker shows unreachable with a timer; the coordinator still decides whether it is lost |
| Worker returns before it is lost | The worker is healthy again, and the timeline records a brief outage |
| Sampler falls behind | It skips rounds, and the header shows the actual sample rate |
| Showcase service restarts | A new recording starts, and the page says it reconnected and history starts over |
| Browser cannot reach the service | A banner says "No answer from showcase; check the SSH tunnel" and the last frame stays |
| Recording reaches its cap, or a write fails | The live view continues, and the header says recording stopped |
| A recording line is corrupt or cut off | Replay skips it and reports how many lines it skipped |

The service has no write endpoint, and replay opens only the file named on the
command line. Recordings hold job IDs, hosts, counters and error text. They
stay out of git under `data/`, and a recording is checked before it is
published.

## Testing

- **Sampler:** fake HTTP sources and a fake clock check gaps, skipped rounds and
  workers coming and going.
- **Derived state:** table tests over short frame sequences check progress,
  rates, ETA, backups, counter resets and timeline events.
- **Recording and replay:** temporary files check the round trip, the cap, and
  cut-off lines.
- **HTTP server:** `httptest` checks frame requests, the memory bound, and that
  no write endpoint exists.
- **Page:** the `statusweb` style of test checks that it loads nothing external
  and meets the status page's contrast rules.
- **Compatibility:** tests run the sampler against real output from the Go
  coordinator and workers, and the Python coordinator's against the shared key
  list, so a renamed field fails a test.
- **Deployment:** `python/tests/test_deploy.py` checks that the profile
  validates, publishes only on loopback, is off by default and has its memory
  limit documented.
- **Real runs:** `make showcase-e2e` is manual, like `make chaos`. It runs a job,
  kills a worker, replays the recording and compares its final state with the
  live one. A rehearsal on the VM through the SSH forward covers a worker kill, a
  coordinator crash, a capture from replay, the 128 MB limit, and sampling's cost
  to a benchmark.

The first test in S1 settles a risk: `/status.json` names workers by ID and
host, and metrics are found by IP address. If the two cannot be matched
reliably, throughput stays cluster-wide and per-worker scrape status needs
another approach.

The new package gets a coverage floor in `go/cover-floors.txt`. The work is done
when the replayed final state matches the live one, the rehearsal passes, and
the six required checks are green on the final head.

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
- Workers gain one identifying metric, `mr_worker_info{host}`, so a scrape
  target matches its `/status.json` worker whatever its address; job behavior
  is unchanged.
- A failed scrape shows that a worker did not answer, not that it died, so the
  page must say "unreachable" until the coordinator declares the worker lost.
- The service depends on `/status.json` and the worker metrics, so tests must
  fix the fields it reads for both coordinators.
- If sampling proves too coarse, exact coordinator events can be added later.
  #332 proposes per-attempt history in `/status.json`, which the sampler would
  read as is if it merges.
