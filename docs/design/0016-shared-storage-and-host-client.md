# ADR 0016 — Shared storage is a host directory, and the client runs on the host

**Status:** accepted, built (#173, #174, #175) · **Phase:** course deliverable

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

**Shared storage.** A directory on the VM, `data/` at the repository
root (git-ignored), is bind-mounted at `/data` in the coordinator and
every worker. It is the cluster's shared storage: the assignment names
"a shared volume mount across all containers" as an acceptable choice for a
single-VM deployment. Job inputs and job outputs live there. Compose also
keeps the test corpus at `/corpus` and the named volume `output` at `/out`,
which holds the experiments' 1 GiB inputs and outputs and the write-ahead
log, apart from user data.

- A path in a job is a shared-storage path, as every container sees it:
  `/data/input/corpus/*.txt`, `/data/out/wc`.
- Intermediate data does not use shared storage. Map output stays on each
  worker's own disk and reducers fetch it over gRPC
  ([ADR 0008](0008-shuffle.md)), as in the paper (§3.1).
- The containers run as the distroless `nonroot` user (UID 65532). `make
  data` creates `data/` with `input/` and `out/`, and makes `out/` writable
  by that UID. `make cluster-up` runs `make data` before starting Compose;
  Compose mounts what it made. A bare
  `docker compose up` skips `make data`, and Docker then creates `data/` as
  root, so the cluster cannot write to it; use `make cluster-up`.
- Ownership: output files are created by UID 65532, readable by everyone
  under the containers' umask of 022, so `mr cat` and the host user can
  read them. Every directory the coordinator and workers create in local
  storage is made mode 0777 (`storage.Local.MkdirAll`), so the host user
  can delete a job's output without `sudo`: deleting a file needs write
  permission on its directory, not on the file. Running the containers as
  the host user (`user: "${UID}:${GID}"`) would avoid this, but the
  `output` volume and the write-ahead log in it belong to UID 65532.
- The S3 profile ([ADR 0013](0013-commit-on-object-storage.md)) stays as an
  option for `s3://` paths.

**The client.** `make mr` builds `mr` for the host into `bin/mr`, with the
host's Go, or in the `golang` image when the host has none. `mr submit` and `mr status` default to `127.0.0.1:18770`, which
Compose publishes on loopback only; `-coordinator` overrides the address.
`mr submit` never touches `data/` itself: the coordinator expands globs, so
the client sends container-side paths such as `/data/input/…` unchanged.
Only `mr upload` and `mr cat` resolve `/data` to `MR_DATA_DIR`, and they fail
if that directory does not exist, so a run from another directory does not
silently use the wrong one.

The upload, job-file submission and output-reading workflow:

- `mr upload <local files, dirs or globs…> <shared dir>` copies files into
  shared storage, a directory's contents keeping their layout, each through
  a temporary file so a job never reads a partial copy. On the VM this is a copy into `data/` (`MR_DATA_DIR`, by default
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

The host-client quick start, run on the course VM:

```sh
make cluster-up                      # initialize data/, build the image, start 1 coordinator and 4 workers
make mr                              # build the client into bin/mr
bin/mr upload testdata/corpus /data/input/corpus
bin/mr submit -m 8 -r 4 -wait examples/wordcount.go /data/out/wc '/data/input/corpus/*.txt'
bin/mr cat /data/out/wc | sort -t$'\t' -k2 -nr | head
```

**M is exact.** `-m` requests an exact number of map tasks, sent as
`JobSpec.num_map_tasks`; the coordinator cuts the input
(`split.PlanM`), so the client does not need to read it. M is divided among
the non-empty input files in proportion to their sizes by largest
remainder, and a file left with none takes one from the file with the most
(`split.Allocate`). Each file is then cut into byte ranges of
ceil(size / k), aligned to records when read. Two cases cannot give exactly
M. With more files than M, every file still gets one split, so M is raised
to the number of files. A file too small to cut k ways, or a `kv` file,
which is never cut, gets fewer. `mr submit` prints the M that resulted on
standard error, and `mr status` reports it as the map task total.

## Alternatives considered

- **Keep the named volume and the read-only corpus mount.** No way to add
  input or read output from the host, which the assignment's client needs.
- **S3 as the default.** Closer to GFS, and already built. But it adds a
  service to every run, and uploads need an S3 client or an `mr upload` that
  speaks S3. It stays available as a profile.
- **HDFS.** A NameNode and DataNodes in an 8 GB VM, and Java, for a
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
