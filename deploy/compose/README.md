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
  (Prometheus and Grafana; see `monitoring/`).
- [`soak.yaml`](soak.yaml): override for the soak run (#160, `make soak`).
  It lowers each worker to 256 MB to prove a job whose intermediate data
  exceeds worker memory spills to disk instead of being OOM-killed (#47).
  Not used by `make check` or `make e2e`; see the runbook §5.8 for the
  invocation.
- [`backups-off.yaml`](backups-off.yaml): override for experiment E2. The
  coordinator runs with `-backups=false`, so a straggler holds up the end
  of each phase.
- `monitoring/`: Prometheus configuration and the Grafana dashboard
  for the `monitoring` profile.

See [`deploy/README.md`](../README.md) for the wider deployment layout and
[`docs/cluster-vm.md`](../../docs/cluster-vm.md) for the CPU, memory, and
disk budgets these limits come from.
