# ADR 0016 — Shared storage is a host directory, and the client runs on the host

**Status:** accepted · **Phase:** course deliverable

## Context

The assignment asks for a client program that runs outside every container
and submits jobs that name where inputs are in shared storage and where
outputs go. The README must also say how to upload example data to shared
storage.

Until now the Compose file mounted `testdata/corpus` read-only at `/corpus`
and kept output in a named Docker volume at `/out`. A user could not add
their own input without editing the Compose file, and could not read output
without starting a container. The README's cluster example ran `mr` inside a
container (`docker compose run --rm mr …`).

## Decision

**Shared storage (planned).** A directory on the VM, `data/` at the repository
root (git-ignored), will be bind-mounted at `/data` in the coordinator and
every worker. It will be the cluster's shared storage: the assignment names
"a shared volume mount across all containers" as an acceptable choice for a
single-VM deployment. Job inputs, job outputs and nothing else will live there.
Today Compose still mounts `/corpus` and `/out`; `/data` job paths require
the planned mounts and client path handling.

- A path in a job will be a shared-storage path, as every container sees it:
  `/data/input/corpus/*.txt`, `/data/out/wc`.
- Intermediate data does not use shared storage. Map output stays on each
  worker's own disk and reducers fetch it over gRPC
  ([ADR 0008](0008-shuffle.md)), as in the paper (§3.1).
- The containers run as the distroless `nonroot` user (UID 65532). `make
  data` creates `data/` with `input/` and `out/`, and makes `out/` writable
  by that UID. `make cluster-up` runs `make data` before starting Compose;
  the directories are prepared for the planned mount.
- The S3 profile ([ADR 0013](0013-commit-on-object-storage.md)) stays as an
  option for `s3://` paths.

**The client.** Building `mr` for the host with `make mr` (into `bin/mr`)
is planned. `mr submit` and `mr status` default to `127.0.0.1:18770`, which
Compose publishes on loopback only; `-coordinator` overrides the address.

The upload, job-file submission and output-reading workflow below is planned:

- `mr upload <local files or dirs…> <shared dir>` copies files into shared
  storage. On the VM this is a copy into `data/` (`MR_DATA_DIR`, by default
  `./data`). The client resolves `/data/...` to that directory, so the user
  types the same path for upload and for the job.
- `make upload-example` uploads the bundled corpus and a generated web log
  to `/data/input/`.
- `mr submit` takes the five things the assignment lists: the input paths,
  the output path, the job file ([ADR 0015](0015-user-job-files.md)), `-m`
  and `-r`.
- `mr cat /data/out/wc` prints a job's output files in order, for a quick
  look without knowing the layout.
- `docker compose run --rm mr …` keeps working, for machines without Go.
  Pass `-coordinator coordinator:7070` inside the Compose network.

Planned host-client quick start, once the `/data` mounts and client path
handling are available:

```sh
make cluster-up                      # initialize data/, build the image, start 1 coordinator and 4 workers
make mr                              # build the client into bin/mr
bin/mr upload testdata/corpus /data/input/corpus
bin/mr submit -m 8 -r 4 -wait examples/wordcount.go /data/out/wc '/data/input/corpus/*.txt'
bin/mr cat /data/out/wc | sort -t$'\t' -k2 -nr | head
```

**M is exact (planned).** `-m` will request an exact number of map tasks.
The target behavior is proportional allocation among input files by size
(largest remainder, at least one split per file), with each file cut into
the allocated number of splits on record boundaries. The implementation
will need to handle inputs with more files than M or too few records for
the requested splits, and report the resulting count. Today `-m` sets an
approximate split size; exact allocation and its reporting are planned.

## Alternatives considered

- **Keep the named volume and the read-only corpus mount.** No way to add
  input or read output from the host, which the assignment's client needs.
- **S3 as the default.** Closer to GFS, and already built. But it adds a
  service to every run, and uploads need an S3 client or an `mr upload` that
  speaks S3. It stays available as a profile.
- **HDFS.** A NameNode and DataNodes in a 16 GB VM, and Java, for a
  benefit (locality) that one VM cannot show.
- **The client inside a container.** Simpler, but not what the assignment
  asks for.

## Consequences

- The README's quick start becomes host commands only: `make cluster-up`,
  `make mr`, `mr upload`, `mr submit`.
- The job file is read on the client and sent in the RPC, so it does not
  need to be uploaded first.
- Every container shares one disk, so there is no locality to exploit
  ([explainer](../explainers/23-locality.md)). Locality stays a scheduler
  hook, not a headline feature.
- `make cluster-down` removes containers, not `data/`. Users delete
  `data/out/…` themselves, and `mr submit` refuses an output directory that
  already holds `part-r-*` files.
