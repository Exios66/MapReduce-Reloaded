# Deployment

How the cluster is built and run. See [cluster VM](../docs/cluster-vm.md) for
the CPU and memory budgets and the setup of the course VM.

| Path | Contents |
|---|---|
| [`docker/Dockerfile`](docker/Dockerfile) | A multi-stage build of one small, static, distroless image with the `coordinator`, `worker` and `mr` binaries |
| [`compose/compose.yaml`](compose/compose.yaml) | The Compose project `mapreduce` |
| [`compose/monitoring/`](compose/monitoring/) | Prometheus's scrape config, and the Grafana provisioning and dashboard |

## Services

| Service | Profile | `mem_limit` | `cpus` | Notes |
|---|---|---|---|---|
| `coordinator` | default | 256 MB | — | gRPC on 18770 and the status page on 18780, both on loopback |
| `worker` | default | 768 MB | 1 | 4 replicas, one per vCPU; scale with `--scale worker=N`; serves `/metrics` on 8081 inside the network |
| `mr` | `tools` | 128 MB | — | Runs one CLI command, e.g. `docker compose ... run --rm mr submit ...` |
| `s3` | `s3` | 256 MB | — | An S3-compatible store (VersityGW) that serves the corpus and keeps output |
| `prometheus`, `grafana` | `monitoring` | 256 MB each | — | At `127.0.0.1:19090` and `127.0.0.1:13000` |

The coordinator, the workers and `mr` see the test corpus at `/corpus` and
job output in the `output` volume at `/out`; the `s3`, `prometheus` and
`grafana` services mount neither. A container that goes over its limit is killed, and
`make e2e` fails if any was.

## Commands

```sh
make cluster-up       # build and start the coordinator and 4 workers
make e2e              # word count on the cluster, compared with the golden output
make e2e-s3           # the same, with input and output in the s3 profile's store
make monitoring-up    # add Prometheus and Grafana
make cluster-down     # remove this project's containers and volumes, and nothing else
```

The project name `mapreduce` keeps `cluster-down` from touching other
containers on a shared machine.

## Regression checks

`make test` (and therefore `make check`) checks the Compose memory limits
and replica-weighted totals against the VM documentation, Prometheus's
preservation of MapReduce job labels and worker DNS discovery, and Grafana's
provisioned datasources. Go tests also compare dashboard metric names and
legend labels with metrics emitted by the coordinator and worker, including
labels retained by the dashboard's `sum by` aggregations.

Python's development dependencies include PyYAML for these checks. To run
just the deployment checks, use `cd python && python3 -m pytest -q -rs
tests/test_deploy.py`. When Docker Compose is installed, the tests also run
`docker compose config` for the default, `s3`, `monitoring`, `tools` and all
profiles together, plus the backups-off override. These commands do not
start containers or require a Docker daemon. When `promtool` is installed,
its `check config` validates the Prometheus configuration too. Missing
external tools produce explicit pytest skips; the static contract checks
always run. Image builds, non-root runtime checks and a live query through
Prometheus still require a container-based end-to-end run.
