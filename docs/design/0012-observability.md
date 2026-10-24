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
   The page refreshes with `<meta http-equiv="refresh">`, so it has no
   JavaScript and no CDN. It reads `Server.Status`, a copy taken under the
   coordinator's lock (ADR 0006).
2. **Metrics are written by hand in the Prometheus text format.**
   `go/internal/promtext` writes the `HELP` and `TYPE` lines and quotes
   label values. There is no client library: the metrics are a few gauges
   and counters, the format is simple, and the dependency stays out of
   `go.mod`.
3. **Job state is gauges; worker activity is counters.** The coordinator's
   `/metrics` has `mr_workers`, `mr_job_tasks{job,kind,state}` and
   `mr_job_counter{job,name}`. A job's counters can go down when a re-run
   map task replaces its earlier counts, so they cannot be counters. Workers,
   with `-http`, serve `mr_worker_tasks_total{kind,result}`, bytes written
   and fetched, and registrations, as atomics in `worker/metrics.go`.
4. **Logs use `log/slog`.** `-log-format json` on the coordinator and the
   worker writes one JSON object per line; the default is text. The worker
   logs every message about an attempt, and its user code through
   `Context.Logger`, with `worker`, `job` and `task` (kind, index, attempt).
5. **Monitoring is opt-in.** `deploy/compose/monitoring` holds Prometheus
   and Grafana in the Compose `monitoring` profile, which `cluster-up` and
   `e2e` do not start. Their ports are bound to `127.0.0.1`, and Grafana
   allows anonymous read-only access only for that reason.

## Alternatives rejected

- **The Prometheus client library.** It would help with histograms, which
  we do not have. For a handful of metrics it adds a dependency
  to replace a writer of about 30 lines.
- **Counters for job counters.** A Prometheus counter that falls looks like
  a reset, and breaks `rate()`.
- **A page that updates with JavaScript.** It would need a script, an
  endpoint for the data, and possibly a CDN, to replace a two-second reload.

## Consequences

- `TestDashboardQueriesServedMetrics` checks that the dashboard queries
  only metrics that are served.
- The page shows no bytes or rates. Workers count them in their own
  `/metrics` but do not report them to the coordinator.
- Per-attempt logs are in each worker's log, found by `job` and `task`.
- Lost workers are kept in memory, the last 100, and are not rebuilt from
  the write-ahead log after a restart.
