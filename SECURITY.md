# Security policy

MapReduce Reloaded is a course project: a coordinator, workers and a
client that run on one VM under Docker Compose. Its security model is
deliberately narrow, and this page says what it covers, what it does not,
and how to report a problem.

## Supported versions

Only `main` is supported. The `v0.x` tags are not maintained, and there
are no maintained branches; a fix lands on `main` through a pull request
([ADR 0014](docs/design/0014-ci-on-pull-requests.md)).

## Reporting a vulnerability

Report it privately through GitHub:
[Security → Report a vulnerability](https://github.com/Exios66/MapReduce-Reloaded/security/advisories/new).
Do not open a public issue for it.

Include the component (coordinator, worker, `mr`, the Python coordinator,
the Compose files or a script), the commit, the steps to reproduce, and
what an attacker gains. This is a one-person course project, so there is
no bounty and no promised response or fix time. Reports are read, and
confirmed problems are fixed on `main` as time allows, with credit if you
want it.

## The trust model

[ADR 0018](docs/design/0018-trust-model.md) is the full statement. In short:

- **The cluster trusts its network.** gRPC between the client, the
  coordinator and the workers is neither encrypted nor authenticated. A
  `worker_id` in a request is the only proof of who sent it.
- **Compose keeps it on the VM.** Every published port (the coordinator's
  gRPC and status ports, the S3 gateway, Prometheus and Grafana) is bound to
  `127.0.0.1`. Workers and the coordinator talk over the Compose network.
- **Outside Compose, the defaults listen on every interface.** `coordinator
  -addr` (`:7070`, Go and Python), `coordinator -http` (`:8080`, the status
  page and `/metrics`) and `worker -listen` (`:7071`, which serves map
  output) are meant for a private network. Bind them to a private address
  or put them behind a firewall.
- **Job files run with the worker's rights.** A job file is interpreted with
  the whole standard library, and the interpreter is not a sandbox
  ([ADR 0015](docs/design/0015-user-job-files.md)). Anyone who can submit a
  job can run code as the worker's user.

mTLS between components and authenticated job submission are planned as a
stretch goal (P6.9, #49). Until they exist, do not run the cluster on a network
with callers you do not trust.

## What counts as a vulnerability

In scope:

- a way to reach a service from outside the VM under the shipped Compose
  files;
- a bad job, input or RPC that crashes the coordinator or a worker, or
  exhausts its memory or disk, despite the limits in ADR 0018 and
  [ADR 0017](docs/design/0017-disk-and-memory-bounds.md);
- a way to read or overwrite files outside the job's input and output
  directories, other than by running a job file;
- an unsafe download or install step in `scripts/`.

Not vulnerabilities, because they are the documented design:

- unauthenticated or plaintext RPCs on the cluster network;
- a caller who can reach the coordinator running code through a job file;
- the fixed S3 gateway credentials, and Grafana's anonymous Viewer access
  and default admin login, in `deploy/compose/compose.yaml`: test and demo
  services bound to loopback;
- any finding that needs the ports exposed beyond what the Compose files do.

If a report falls in the second list but shows a cheap way to narrow the
gap, it is still welcome as an ordinary issue.
