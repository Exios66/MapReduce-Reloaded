# MapReduce-Reloaded

A from-scratch re-implementation of Google's MapReduce, as described in
Dean & Ghemawat, [*MapReduce: Simplified Data Processing on Large Clusters*](https://research.google.com/archive/mapreduce-osdi04.pdf)
(OSDI 2004), extended for modern infrastructure: object storage, compression,
containers, gRPC, observability, and multi-language user code.

- **Go** is the primary implementation: the coordinator, the workers and the
  `mr` command, and the only one that runs on a cluster.
- **Python** is the reference implementation of the API, the file format and
  local mode, without compression. For the same uncompressed job, both
  languages produce the same bytes.

## Status

Phases 0 to 4 are done, with S3 storage and compression from Phase 6. The
Go coordinator and workers run jobs over gRPC with every paper feature
F1–F23 (see the [paper mapping](docs/paper-mapping.md)), and survive worker
crashes, coordinator restarts, slow machines and bad input records. The
chaos test passes 50 seeded runs.

The benchmark harness and data generators are done. Next are the course
deliverables: a host-side client with uploads to a shared `/data` mount, user
job files submitted with each job, and the evaluation of the special features
(see [the course deliverables](docs/roadmap.md#course-deliverables-honors-project-1)
and [design](design.md#key-decisions)). Python workers and Go↔Python interoperability (Phase 5) and the
rest of Phase 6 are stretch goals. The [roadmap](docs/roadmap.md) has the full
plan, and [the roadmap explained](docs/roadmap-explained.md) walks through it
in plain language.

## Quick start

You need Go 1.26+ and Python 3.11+. Run these from the repository root.

### Run a job on one machine

Local mode runs every task in one process (§4.7). It is the oracle that
cluster runs are checked against.

```sh
cd go
go run ./cmd/mr run -local -m 3 -r 4 wordcount /tmp/wc '../testdata/corpus/*.txt'
head -3 /tmp/wc/part-r-00000
```

The job's counters print to standard error, and each reduce task writes one
`part-r-NNNNN` file. The Python implementation gives the same files:

```sh
cd python
pip install -e .
python3 -m mapreduce_reloaded run --local -m 3 -r 4 wordcount /tmp/wc-py '../testdata/corpus/*.txt'
diff -r /tmp/wc /tmp/wc-py && echo identical
```

### Run a job on a cluster

The cluster runs in Docker Compose: one coordinator and four workers, each
worker limited to one CPU. It needs Docker with Compose, 4 CPUs and about
3.7 GB of memory ([cluster VM](docs/cluster-vm.md) has the budgets and the
setup for the course VM, and the [runbook](docs/runbook.md) goes from SSH to
a full demo).

```sh
make cluster-up       # build the image; start the coordinator and 4 workers
make e2e              # run word count on the cluster and compare with the golden output
docker compose -p mapreduce -f deploy/compose/compose.yaml run --rm mr \
    submit -coordinator coordinator:7070 -wait wordcount /out/wc '/corpus/*.txt'
make cluster-down     # remove the containers and the output volume
```

To read the output today, copy it out of the volume: `docker compose -p
mapreduce -f deploy/compose/compose.yaml cp coordinator:/out/wc ./wc` (or
run a short container that mounts the `output` volume). There is no upload
step yet: input is the corpus mounted at `/corpus`.

Planned ([ADR 0016](docs/design/0016-shared-storage-and-host-client.md)):
`make mr`, `bin/mr upload` and `bin/mr cat`, against a host
`data/` directory mounted at `/data`. Those commands are not built yet.

Every container sees the test corpus at `/corpus` and writes job output to
the `output` volume at `/out`. While a job runs, the status page and
Prometheus metrics are at `127.0.0.1:18780` (`/` and `/metrics`). `make
monitoring-up` adds Prometheus (`127.0.0.1:19090`) and a Grafana dashboard
(`127.0.0.1:13000`). `make e2e-s3` runs the same job with its input and output
in an S3-compatible store.

## How a job runs

```
                 mr submit
                     │  SubmitJob
                     ▼
              ┌─────────────┐   heartbeats, RequestTask, ReportTask
              │ coordinator │◄─────────────────────────────┐
              └─────────────┘                              │
      task table · worker registry · write-ahead log       │
      status page · /metrics                               │
                     │ tasks, handed out on request        │
        ┌────────────┼────────────┐                        │
        ▼            ▼            ▼                        │
    ┌────────┐   ┌────────┐   ┌────────┐                   │
    │ worker │   │ worker │   │ worker │ ──────────────────┘
    └────────┘   └────────┘   └────────┘
     map task: read a split, map, combine, sort, spill one
               sorted file for each reduce task
        ▲   FetchPartition: reduce tasks pull map output
        └────────────────────────────────────────────┐
     reduce task: fetch, merge, group, reduce, write part-r-NNNNN
```

The coordinator splits the input into map tasks and hands tasks to workers
that ask for them. Map output stays on the worker that made it, and reduce
tasks fetch it from there. The coordinator logs every decision before it
replies, so a restarted coordinator resumes the job. [Design](design.md)
follows one job from submission to completion, with sequence diagrams and a
list of where this system differs from the paper and why.

## Example programs

Every program the paper names is registered under a name that `mr` takes.

| Program | Paper | Input | Output |
|---|---|---|---|
| `wordcount` | §2 | Any text | Each word and its count |
| `grep` | §2.3 | Any text; `-param pattern=STRING` | Each line that contains the string |
| `invertedindex` | §2.3 | Any text | Each word and the files it occurs in |
| `urlfreq` | §2.3 | A web log in the Common Log Format | Each URL and its request count |
| `reverselinks` | §2.3 | `source<TAB>target` links | Each target and the pages that link to it |
| `termvector` | §2.3 | `url<TAB>text` documents; `-param min=N` | Each host and its term vector |
| `distsort` | §5.3 | Any text | All lines in byte order, across all output files |

Their code is in [`go/examples/`](go/examples/) and
[`python/mapreduce_reloaded/examples/`](python/mapreduce_reloaded/examples/).
Programs written in Go register themselves with the `api` package; see
[the programming API](docs/explainers/07-programming-api.md).

## The `mr` command

```
mr run -local [job flags] <program | job.go> <output-dir> <inputs>
mr submit [-coordinator addr] [-max-skipped N] [-wait] [job flags] <program | job.go> <output-dir> <inputs>
mr status [-coordinator addr] <job-id>

job flags:
  -m M  -r R  -param name=value (repeatable)
  -input-format text|kv  -output-format text|kv
  -intermediate-codec none|zstd|snappy  -output-codec none|zstd|snappy
  -combiner=false (run without the program's combiner)
```

Inputs are files or glob patterns, or `s3://` URIs on a cluster with an
object store. `mr status` and `mr submit -wait` print the job's state, its
progress, its backup attempts, its duration and its counters.

## Benchmarks

`make bench SCENARIO=grep` (also `sort` and `zipf`) runs one of the paper's
benchmarks on the Compose cluster, and writes the input, shuffle and output
rates over time, the job's counters, and the machine it ran on to
`bench/results/`. `make bench-codecs` compares the compression codecs. See
[`bench/README.md`](bench/README.md).

## Repository layout

```
proto/     gRPC/protobuf contract shared by Go and Python
go/        Go implementation: coordinator, worker, mr, example programs
python/    Python reference implementation: API, file format, local mode
deploy/    Dockerfile, Compose cluster, Prometheus and Grafana
bench/     data generators and benchmark harness
scripts/   code generation, the end-to-end test, test-data generators
testdata/  test corpus, golden files and format vectors
docs/      design, roadmap, paper mapping, file format, ADRs, explainers
```

Each of these has its own README.

## Development

Requirements: Go 1.26+, Python 3.11+, `golangci-lint` v2 (built with Go 1.26+).

```sh
pip install -e "python[dev]"   # install the Python package + dev tools
make tools                     # install pinned code generators into .tools/bin
make hooks                     # once per clone: run `make check` before every push
make check                     # all linters and tests (the project's quality gate)
make fmt                       # auto-format both languages
make proto                     # regenerate code after editing proto/
```

`make` runs Python as `python3`. To use another interpreter, e.g. a
virtualenv's `python`, pass `PYTHON=...` (`make check PYTHON=python`).

`make check` is the gate: it checks the generated code, runs the linters,
and runs every test, including a short chaos run that kills workers and
restarts the coordinator during jobs. The pre-push hook runs it before every
push, and GitHub Actions runs the same targets on every pull request. Work
reaches `main` only through pull requests. If hosted Actions runners are
unavailable, `make ci-report` posts the result as a commit status instead
(see ADR 0014). See
[ADR 0002](docs/design/0002-local-quality-gate.md) and
[ADR 0014](docs/design/0014-ci-on-pull-requests.md).

## Documentation

[`docs/README.md`](docs/README.md) is the index. In short:

- [Design](design.md): one job from submission to completion, and the
  differences from the paper.
- [Paper mapping](docs/paper-mapping.md): each feature of the paper, where it
  is, and how it is tested.
- [Design records](docs/design/): short ADRs for the key decisions.
- [Explainers](docs/explainers/): one page for each component, written to
  answer a reviewer's questions.
- [File format](docs/file-format.md): the MRKV intermediate and output file
  format, and the partition hash.
