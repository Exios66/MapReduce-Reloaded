# Deployment

How the cluster is built and run. See [cluster VM](../docs/cluster-vm.md) for
the memory budget and the setup of the course VM.

| Path | Contents |
|---|---|
| [`docker/Dockerfile`](docker/Dockerfile) | A multi-stage build of one small, static, distroless image with the `coordinator`, `worker` and `mr` binaries |
| [`compose/compose.yaml`](compose/compose.yaml) | The Compose project `mapreduce` |
| [`compose/monitoring/`](compose/monitoring/) | Prometheus's scrape config, and the Grafana provisioning and dashboard |

## Services

| Service | Profile | `mem_limit` | Notes |
|---|---|---|---|
| `coordinator` | default | 256 MB | gRPC on 18770 and the status page on 18780, both on loopback |
| `worker` | default | 768 MB | 4 replicas; scale with `--scale worker=N`; serves `/metrics` on 8081 inside the network |
| `mr` | `tools` | 128 MB | Runs one CLI command, e.g. `docker compose ... run --rm mr submit ...` |
| `s3` | `s3` | 256 MB | An S3-compatible store (VersityGW) that serves the corpus and keeps output |
| `prometheus`, `grafana` | `monitoring` | 256 MB each | At `127.0.0.1:19090` and `127.0.0.1:13000` |

Every container sees the test corpus at `/corpus` and job output in the
`output` volume at `/out`. A container that goes over its limit is killed, and
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
