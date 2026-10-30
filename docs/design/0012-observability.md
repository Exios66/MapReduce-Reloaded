# ADR 0012 — Status page, hand-written metrics and structured logs

**Status:** accepted · **Phase:** 4 · **Issue:** #32

## Context

The paper's master runs an HTTP server with status pages for people: tasks
done and in progress, bytes, rates, and which workers failed while running
which tasks (§4.8). The authors used them to predict a job's duration and to
find slow machines. A cluster today also expects metrics a monitoring
system can scrape, and logs it can parse.

## Decision

1. **The coordinator serves an HTML status page.** `coordinator -http`
   (default `:8080`) serves `/` from `statusweb/status.html`: jobs with
   progress and counters, live workers and their tasks, and lost workers.
   The page is complete HTML. One small inline script (revised in #312)
   fetches the page again every two seconds and swaps in the new content,
   with a pause button and a notice when the coordinator stops answering;
   without it, `<meta http-equiv="refresh">` inside `<noscript>` reloads the
   page. There is no CDN and no external file. `/status.json` serves the
   same status as data. The page reads `Server.Status`, a copy taken under the
   coordinator's lock (ADR 0006).
2. **Metrics are written by hand in the Prometheus text format.**
   `go/internal/promtext` writes the `HELP` and `TYPE` lines and quotes
   label values. There is no client library: the metrics are a few gauges
   and counters, the format is simple, and the dependency stays out of
   `go.mod`.
3. **Job state is gauges; worker totals are counters.** The coordinator's
   `/metrics` has `mr_workers`, `mr_job_tasks{job,kind,state}`,
   `mr_job_duration_seconds{job}`, `mr_job_backups{job,state}` (ADR 0010),
   `mr_job_map_tasks_local{job,level}` and `mr_job_counter{job,name}`. A job's
   duration runs from its submission to its end, or to the scrape while it
   runs; the times come from the write-ahead log, so a restart keeps them.
   A job's counters can go down when a re-run
   map task replaces its earlier counts, so they cannot be counters. Workers,
   with `-http`, serve the counters `mr_worker_tasks_total{kind,result}`,
   bytes written and fetched, and registrations, and the gauge
   `mr_worker_task_running{kind}`, as atomics in `worker/metrics.go`.
4. **Logs use `log/slog`.** `-log-format json` on the coordinator and the
   worker writes one JSON object per line; the default is text. The worker
   logs every message about an attempt, and its user code through
   `Context.Logger`, with `worker`, `job` and `task` (kind, index, attempt).
   `-log-level debug|info|warn|error` (default `info`) sets the lowest level.
   The coordinator logs each worker registered, job submitted, task
   assigned (with `backup` and `locality`), attempt reported and job ended,
   and the worker each attempt's start and end with its duration, so a
   run's log tells its story without the write-ahead log. Recovery replays
   the write-ahead log silently.
5. **Monitoring is opt-in.** `deploy/compose/monitoring` holds Prometheus
   and Grafana in the Compose `monitoring` profile, which `cluster-up` and
   `e2e` do not start. Their ports are bound to `127.0.0.1`, and Grafana
   allows anonymous read-only access only for that reason.

## Alternatives rejected

- **The Prometheus client library.** It would help with histograms, which
  we do not have. For a handful of metrics it adds a dependency
  to replace a writer of about ten lines.
- **Counters for job counters.** A Prometheus counter that falls looks like
  a reset, and breaks `rate()`.
- **A page built in the browser from `/status.json`.** It would need a
  second renderer, kept in step with the Go and Python ones. The script
  instead fetches the server-rendered page, so there is one renderer per
  language and no template in JavaScript. (The first version of this ADR
  rejected scripting altogether; reloads lost scroll position, open
  counters and the reader's pause.)

## Consequences

- `TestDashboardQueriesServedMetrics` checks that the dashboard queries
  only metrics that are served.
- The page shows no bytes or rates. Workers count them in their own
  `/metrics` but do not report them to the coordinator.
- Per-attempt logs are in each worker's log, found by `job` and `task`.
- Lost workers are kept in memory, the last 100, and are not rebuilt from
  the write-ahead log after a restart.
