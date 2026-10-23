# Explainer — Status Page and Metrics (#32)

The paper's master runs an HTTP server with pages for people: how many
tasks are done and in progress, bytes in and out, processing rates, and
which workers failed while running which tasks (§4.8). The authors used
them to predict how long a job would take and to find slow machines.

## What is served

`coordinator -http :8080` (the default) serves:

| Path | What | Where |
|------|------|-------|
| `/` | Jobs with map and reduce progress, counters and errors; live workers and what each runs; lost workers and the tasks that had to run again. It refreshes every 2 seconds. | `statusweb/status.html` |
| `/metrics` | `mr_workers`, `mr_job_tasks{job,kind,state}` and `mr_job_counter{job,name}`, in the Prometheus text format | `statusweb/metrics.go` |

Both read `coordinator.Server.Status`, a copy of the coordinator's state
taken under its lock. Nothing in `statusweb` touches the lock.

In the Compose cluster both are at `127.0.0.1:18780`. `make e2e` checks that
`/metrics` shows the job's reduce tasks completed.

## Choices

- **No dependencies.** The metrics are a few gauges, and the text format
  takes about 30 lines to write. The Prometheus client library would be
  worth adding for histograms, not for this.
- **Gauges, not counters.** A job's counters can go down when a lost map
  task's counts are replaced by its re-run's (#27), so they are not
  Prometheus counters.
- **A refreshing page, not a script.** `<meta http-equiv="refresh">` keeps
  the page free of JavaScript and of any CDN.
- **Lost workers since the process started.** They are kept in memory, the
  last 100, and are not rebuilt from the log after a restart.

## Limits

- Bytes and rates are not shown, since workers do not report them yet.
- Per-attempt logs are not shown either.
- Workers serve no metrics of their own.
