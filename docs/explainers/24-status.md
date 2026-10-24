# Explainer — Status Page and Metrics (#32)

The paper's master runs an HTTP server with pages for people: how many
tasks are done and in progress, bytes in and out, processing rates, and
which workers failed while running which tasks (§4.8). The authors used
them to predict how long a job would take and to find slow machines.

## What is served

`coordinator -http :8080` (the default) serves:

| Path | What | Where |
|------|------|-------|
| `/` | Jobs with their duration, map and reduce progress, backup attempts, counters and errors; live workers and what each runs; lost workers and the tasks that had to run again. It refreshes every 2 seconds. | `statusweb/status.html` |
| `/metrics` | `mr_workers`, `mr_job_tasks{job,kind,state}`, `mr_job_duration_seconds{job}`, `mr_job_backups{job,state}` and `mr_job_counter{job,name}`, in the Prometheus text format | `statusweb/metrics.go` |

Both read `coordinator.Server.Status`, a copy of the coordinator's state
taken under its lock. Nothing in `statusweb` touches the lock.

A job's duration is the time from when the coordinator accepted it to when
it succeeded or failed, or to the moment of the status while it runs. Both
times are in `GetJobStatusResponse` (`submitted_at`, `finished_at`), so
`mr status` and `mr submit -wait` print a finished job's `seconds`, as the
coordinator measured it. The paper gives each benchmark's time the same
way, from start to finish (§5.2, §5.3).

`worker -http :8081` serves the worker's own `/metrics`: task attempts
finished by kind and result (`mr_worker_tasks_total{kind,result}`), those
running (`mr_worker_task_running{kind}`), bytes of input mapped, of map
output written and fetched, and of output reduced, and registrations. Input
and output bytes are counted as each task ends; output bytes are the keys
and values before the output format (`reduce.output.bytes`). It is off by
default. They are atomic counters
on the worker, in `worker/metrics.go`; both servers write the text format
with `promtext`.

In the Compose cluster the coordinator's are at `127.0.0.1:18780`; the
workers' are on port 8081 inside the Compose network, not published, since
there are several. `make e2e` checks that the coordinator's `/metrics` shows
the job's reduce tasks completed.

## Dashboard

`make monitoring-up` starts Prometheus (`127.0.0.1:19090`), which scrapes
the coordinator and every worker each 5 seconds, and Grafana
(`127.0.0.1:13000`, read-only without a login). Its MapReduce dashboard
shows live workers, each job's progress, tasks by kind and state, task
attempts finished per second, the input, map output, shuffle and output
rates in bytes per second, as in the paper's Figures 2 and 3,
each job's duration, and its backup attempts started and won. They sit in the Compose `monitoring` profile, so `cluster-up` and
`e2e` do not start them; `cluster-down` removes them. The files are in
`deploy/compose/monitoring`, and `TestDashboardQueriesServedMetrics` checks
that the dashboard queries only metrics that are served.

## Logs

`-log-format json` makes the coordinator and worker log one JSON object per
line; the default is text. A worker tags every message about an attempt,
its own and those of the attempt's user code (`Context.Logger`), with
`worker`, `job` and `task`, a group of the task's kind, index and attempt:

```json
{"level":"WARN","msg":"task failed","worker":"…","job":"job-1","task":{"kind":"map","index":4,"attempt":1},"err":"..."}
```

## Choices

- **No dependencies.** The metrics are a few gauges and counters, and the text format
  takes about 30 lines to write. The Prometheus client library would be
  worth adding for histograms, not for this.
- **Gauges for jobs, counters for workers.** A job's counters can go down
  when a lost map task's counts are replaced by its re-run's (#27), so they
  are not Prometheus counters. A worker's only go up while it runs.
- **A refreshing page, not a script.** `<meta http-equiv="refresh">` keeps
  the page free of JavaScript and of any CDN.
- **Lost workers since the process started.** They are kept in memory, the
  last 100, and are not rebuilt from the log after a restart.

## Limits

- The status page shows no bytes or rates: workers count them in their own
  `/metrics`, but do not report them to the coordinator.
- Per-attempt logs are not shown either: they are in each worker's log,
  found by `job` and `task`.
