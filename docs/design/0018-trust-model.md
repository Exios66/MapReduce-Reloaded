# ADR 0018 — The cluster trusts its network; what that costs, and what is bounded

**Status:** accepted · **Phase:** course deliverable

## Context

A CodeRabbit security scan (2026-10-26) reported nine findings. Two of them
are not bugs in one function but a property of the whole design:

- **Workers do not authenticate the coordinator, or each other.** The gRPC
  connections are plaintext. Someone who can change traffic between a worker
  and the coordinator can hand it a different shuffle source, and so change
  what the worker commits.
- **The coordinator does not authenticate callers.** A `worker_id` in a
  request is the only proof of who sent it, so any caller that can reach the
  port can ask for work, or report results, as any worker.

## Decision

**The cluster runs on a network its operator trusts, and does not
authenticate or encrypt its RPCs.** The assignment's deployment is four
containers and a client on one VM. Under Compose:

- the coordinator, Prometheus, Grafana and MinIO publish their ports on
  `127.0.0.1` only (`deploy/compose/compose.yaml`), so nothing outside the VM
  reaches the coordinator;
- workers and the coordinator talk over the Compose network, which only the
  containers on it share.

Outside Compose, `coordinator -addr` (`:7070`) and `worker -listen`
(`:7071`, which serves map output) default to all interfaces. Do not run
either on a network with callers you do not trust; bind them to a private
address or put them behind a firewall.

mTLS between components and authenticated job submission are a stretch goal
(P6.9, #49), and the better fix for both findings: derive the worker ID from
the authenticated peer, and keep today's attempt-state and output-shape checks
after it. They are not built because they add certificate handling to every
component of a one-VM course project, and the loopback binding already
covers the course's deployment.

**What is bounded, because a trusted caller can still send a bad job.** The
rest of the report is fixed, so that one submission cannot exhaust the
coordinator or a worker:

- **Task counts.** `R` is at most 10,000 and `M` at most 100,000
  (`api.MaxReduceTasks`, `api.MaxMapTasks`). `JobSpec.Validate` checks both
  for the coordinator and the local runner. When the split size, not `M`,
  sets the number of map tasks, the planner counts a file's splits from its
  size and stops once the plan passes the limit, so an input cannot build
  the list before it is rejected. A log written before the limit
  existed is trusted when the coordinator restarts.
- **Failing tasks.** A task that fails `-max-task-failures` times (default 8)
  fails the job; it no longer returns to the queue for ever.
- **Long lines.** A split that starts inside a line looks for the next line
  only up to its own end, so the splits of one very long line each read their
  own range, not the rest of the line.
- **Output directory.** The check for existing output matches the directory
  name literally, so a name with `*`, `?` or `[` cannot hide earlier results.
- **Go install.** `scripts/vm-setup.sh --install` downloads over HTTPS only,
  redirects included, because the checksum comes from the same host as the
  archive.
- **Plot script.** `bench/plot-rates.py` keeps state only for the metrics it
  plots, with running totals, so the work follows the input's length.

## Consequences

- Anyone who can reach the coordinator's port can run any registered
  program, or a job file (ADR 0015), on the workers. A job file is
  interpreted with the whole standard library, `os` and `net` included, and
  ADR 0015 says the interpreter is not a sandbox; so that caller can read and
  write whatever the worker's user can, and reach whatever the worker can.
  The shuffle port serves any job's map output to whoever asks for it.
- This is a decision for this project's scope, not a claim that the design
  is safe on an open network. Someone who deploys it there should do P6.9
  first.
