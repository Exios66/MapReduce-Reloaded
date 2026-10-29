# Compose project

The cluster on one VM ([cluster VM](../../docs/cluster-vm.md)): one
coordinator and four workers, each worker limited to one CPU, started with
`make cluster-up` and stopped with `make cluster-down`. The coordinator,
the workers, and the `mr` tools container share the host's `data/`
directory at `/data` (shared storage, ADR 0016), the test corpus read-only
at `/corpus`, and the named `output` volume at `/out` (job output and the
coordinator's write-ahead log). The `s3` service mounts the corpus and its
own output volume at different paths (`/data/corpus`, `/data/out`), and the
monitoring services mount only their own configuration. All published
ports are bound to `127.0.0.1` only.

- [`compose.yaml`](compose.yaml): the main project. The coordinator runs
  `-data-dir /out/wal` with 256 MB; each worker has 768 MB, `cpus: 1`, and
  a 2 GiB minimum-free-disk gate. Profiles, all off by default: `tools` (the
  `mr` tools container), `s3` (a local S3-compatible store), `monitoring`
  (Prometheus and Grafana; see `monitoring/`). `make cluster-up`, `make e2e`,
  `make e2e-s3` and `make monitoring-up` use it alone.
- [`soak.yaml`](soak.yaml): override for the soak run (#160, `make soak`).
  It lowers each worker to 256 MB to prove a job whose intermediate data
  exceeds worker memory spills to disk instead of being OOM-killed (#47).
  `scripts/soak.sh` applies it with `-f compose.yaml -f soak.yaml`. Not used
  by `make check` or `make e2e`; see the runbook §5.8.
- [`backups-off.yaml`](backups-off.yaml): override for experiment E2
  (`bench/experiments.sh E2`), also the no-speculation setting of E7. The
  coordinator runs with `-backups=false`, so a straggler holds up the end of
  each phase.
- [`speculation-late.yaml`](speculation-late.yaml): override for experiment
  E7 (`bench/experiments.sh E7`). The coordinator runs with
  `-speculation=late`, so it starts backup attempts by LATE instead of the
  paper's rule (ADR 0019).
- `monitoring/`: Prometheus configuration and the Grafana dashboard
  for the `monitoring` profile.

The overrides are layered on `compose.yaml` with a second `-f`. From the
repository root, for example:

```sh
docker compose -p mapreduce -f deploy/compose/compose.yaml \
    -f deploy/compose/speculation-late.yaml up -d
```

`bench/experiments.sh` does this itself: for each setting it recreates the
coordinator with that setting's override, or with none.

See [`deploy/README.md`](../README.md) for the wider deployment layout and
[`docs/cluster-vm.md`](../../docs/cluster-vm.md) for the CPU, memory, and
disk budgets these limits come from.
