# Roadmap

The full design and phased plan for MapReduce-Reloaded. Feature IDs (F1–F24)
are tracked in [paper-mapping.md](paper-mapping.md). Each unit of work is a
GitHub issue, labelled by phase and numbered in the order it should be done.

**Status.** Phases 0–4 and Phase 7 are done: F1–F23 run in Go and have
tests; F24 is covered by the Phase 7 evaluation harness. #41 and #42
(Phase 6 Tier A) are also done. Phase 7's benchmarks
and evaluation (#57, #58) and the final report (#60) are done; the course deliverables below remain. Where the build departed
from this plan, [design.md](../design.md) lists the differences from the paper
and why.

## Course deliverables (Honors Project 1)

This project is a solo submission for Honors Project 1. The course's
requirements come before the stretch phases of this roadmap.

| Date (2026) | Deliverable | State |
|---|---|---|
| Nov 1 | Email Tyler that this is a solo team, with the repository link (the repository is public, so no invite is needed) | to confirm |
| Nov 6 | [design.md](../design.md#key-decisions) answers the design questions | done |
| Nov 16 | Final code merged to `main` through pull requests | in progress |
| Nov 17–20 | Demo with Tyler, scheduled by me | — |
| After finishing | Submit the course's end-of-project form | — |

Work still due by Nov 16, each a small pull request reviewed by an AI agent
before it merges ([design.md](../design.md#process)):

1. **Deployment:** four workers limited to one CPU each; the VM described
   as 4 vCPUs, 8 GB and a 35 GB disk (33 GB root, shared;
   [cluster VM](cluster-vm.md), the source of truth).
2. **CI on pull requests** ([ADR 0014](design/0014-ci-on-pull-requests.md)).
3. **Shared storage:** `data/` bind-mounted at `/data`; `make data`,
   `make upload-example`
   ([ADR 0016](design/0016-shared-storage-and-host-client.md)).
4. **Host client:** `make mr`, `mr upload`, `mr cat`, and `127.0.0.1:18770`
   as the default coordinator address (ADR 0016).
5. **Exact M:** `-m` gives the number of map tasks (ADR 0016).
6. **Job files:** `mr submit job.go …`, `JobSpec.program_source`, Yaegi in
   the worker, and `examples/wordcount.go`
   ([ADR 0015](design/0015-user-job-files.md)) (done, with the fatal
   task failure in #198).
7. **Combiner switch:** `mr submit -combiner=false`, for the evaluation
   (done).
8. **Coordinator recovery in Compose:** `-data-dir` on a volume (done).
9. **Evaluation:** experiments E0–E5 of
   [design.md](../design.md#performance-evaluation), with plots in
   `bench/README.md`.
10. **README:** what to build, what to install, how to upload the example
    data, how to write a job file and how to submit it, all from the host.
11. **Feedback given:** review at least one pull request written by an AI
    agent, with comments on it ([design.md](../design.md#process)).

Items 1 and 2 are done (#164, #165), items 3, 4, 5 and 10 in #188, and
item 8 in #177: Compose runs the coordinator with `-data-dir /out/wal`.
Item 9 is done: E0–E5 ran with `bench/experiments.sh` (#179). Phase 5, the rest of Phase 6 and #59 stay stretch goals; LATE speculation (#45) is built, and E7, its evaluation, is wired but not run.

## Context

The goal is to rebuild the core of Google's MapReduce as described in Dean &
Ghemawat, *MapReduce: Simplified Data Processing on Large Clusters* (OSDI
2004). The rebuild should include as many of the paper's original features as
possible, then extend them for modern infrastructure and modern use cases.

- **Scope:** a real distributed system. A coordinator and N workers talk over
  the network. The cluster runs locally with Docker Compose and can optionally
  be deployed to Kubernetes.
- **Languages:** Go is the primary implementation. Python is the
  reference implementation of the API, the file format and local mode, and it
  also has a coordinator server (done, #36); a Python worker is stretch work (#37). The original system was C++; see
  [ADR 0001](design/0001-languages-and-shared-contract.md) for why we use Go
  and Python instead.
- **Key architectural idea:** both languages share one contract:
  - gRPC/protobuf for RPC;
  - a documented on-disk record format;
  - a language-neutral partition hash.

  Running the same job through both implementations' local modes gives a
  byte-for-byte correctness check. Mixing Go and Python components in one
  cluster is the goal of Phase 5.

## Delivery scope (6 weeks)

The full plan below is about 20 weeks of work. The delivered scope, with a
week-by-week schedule and a plain-language walkthrough, is in
[roadmap-explained.md](roadmap-explained.md). In short:

- **In scope:**
  - Phases 0–4: every paper feature, F1–F23, in Go (done), plus F24 with Phase 7;
  - the Python halves of #5 and #8–#12: file format, hash, formats, sort,
    combiner and local mode;
  - #41 S3 storage and #42 compression;
  - #57 and #58: benchmarks and the paper's §5 experiments;
  - #60: the final report.
- **Stretch** (issues kept open with a `[stretch]` prefix):
  - #35, #37–#39: Python distributed parity;
  - #43–#55: the remaining modern extensions;
  - #59: evaluation of the extensions. LATE (#45) is built; its experiment
    E7 is wired and has not been run.
- **Cluster host:** the course VM (Ubuntu 24.04.4 LTS, 4 vCPUs, 8 GB RAM, 35 GB disk with a 33 GB root, shared). The whole
  cluster runs there under Docker Compose, with each worker limited to one
  CPU and every service to a memory limit; see
  [cluster-vm.md](cluster-vm.md), the single source of truth for the VM's
  specification.

## Development process

- Work proceeds one small, reviewable unit at a time. Each issue is roughly
  one PR, and each commit covers one concept.
- Every feature ships with tests. `make check` should pass before every push;
  the pre-push hook runs it in clones where `make hooks` was run
  ([ADR 0002](design/0002-local-quality-gate.md)), and CircleCI runs it on
  every pull request ([ADR 0014](design/0014-ci-on-pull-requests.md)).
- Each phase adds a short architecture decision record (ADR) under
  [`design/`](design/), covering the decision, the alternatives and the
  rationale.
- Code favours clarity over cleverness. Doc comments cite paper sections
  (e.g. `// §3.3: re-execute completed map tasks`).
- [paper-mapping.md](paper-mapping.md) is updated whenever a paper feature
  lands.

## 1. Paper feature checklist (what "faithful" means)

Each item maps to a paper section and will get a test.

| # | Paper feature (§) | How we implement it |
|---|---|---|
| F1 | Programming model: `map(k1,v1)→list(k2,v2)`, `reduce(k2,list(v2))→list(v2)` (§2) | `Mapper`/`Reducer` interfaces in Go; `Mapper`/`Reducer` classes in Python |
| F2 | Input split into M pieces of 16–64 MB (§3.1) | `split.Plan` with a configurable split size; split on record boundaries |
| F3 | One master, M map tasks, R reduce tasks (§3.1) | Coordinator with a task scheduler; workers pull tasks over RPC |
| F4 | Intermediate pairs buffered, partitioned by `hash(key) mod R`, written to local disk (§3.1) | In-memory buffer, spill to R partition files per map task |
| F5 | Reducers fetch partitions remotely, sort by key, group, apply reduce (§3.1) | `FetchPartition` streaming RPC on each worker, plus external merge sort |
| F6 | R output files, one per reduce task (§3.1) | `part-r-00000` … in the output directory or bucket |
| F7 | Master data structures: task state (idle / in-progress / completed), worker identity, intermediate file locations (§3.2) | `TaskTable` and `Registry` in the coordinator |
| F8 | Worker failure detection by heartbeat; reset its tasks; **re-run completed map tasks** of a failed worker; notify reducers (§3.3) | Heartbeat timeout, task re-queue; a reduce attempt gets the new locations from the coordinator |
| F9 | Master failure: periodic checkpoints and recovery (§3.3) | With `-data-dir`, a write-ahead log of coordinator decisions, replayed on restart, so the job resumes (no snapshot, ADR 0007); without it a restart loses the job |
| F10 | Atomic commit semantics: map output reported to master once; reduce output via atomic rename (§3.3) | Write to temp file, `rename`/object-store commit; duplicate completions ignored |
| F11 | Locality: schedule map tasks near their input data (§3.4) | Workers advertise a host/zone label; scheduler prefers a matching split |
| F12 | Task granularity: M and R much larger than the number of workers (§3.5) | Configurable; defaults derived from input size and worker count |
| F13 | Backup tasks for stragglers near the end of the job (§3.6) | Speculative copies once no task of the current phase is idle; first finisher wins |
| F14 | Custom partitioning function, e.g. `hash(Hostname(url))` (§4.1) | `Partitioner` interface; built-in hash partitioner (a range partitioner is Phase 6 stretch work) |
| F15 | Ordering guarantee: keys sorted within each partition (§4.2) | Comes from the sort-merge in the reduce phase; test asserts it |
| F16 | Combiner function (§4.3) | Optional `Combiner`, run on each spill |
| F17 | Input/output types: text lines, key/value, custom `Reader`/`Writer` (§4.4) | Pluggable `InputFormat`/`OutputFormat` registry |
| F18 | Side effects: atomic, idempotent auxiliary outputs (§4.5) | Task-scoped temp directory, committed together with the main output |
| F19 | Skipping bad records: worker reports the crashing record; master tells retries to skip it (§4.6) | Record-sequence tracking; skip list after 2 failures on the same record |
| F20 | Local execution mode for debugging (§4.7) | `mr run --local`: everything in one process, sequential, deterministic |
| F21 | Status information: HTTP status pages (§4.8) | Coordinator web UI showing progress, throughput, failures and worker list |
| F22 | Counters, aggregated by master, de-duplicated across re-executions (§4.9) | `ctx.Count(name, delta)`; only counted from the winning task attempt |
| F23 | Paper example programs (§2.3) | grep, word count, URL access frequency, reverse web-link graph, term vectors, inverted index, distributed sort |
| F24 | Performance experiments (§5) | Benchmark harness (#57) and the grep, sort, no-backup and killed-worker runs at small scale (#58) |

---

## 2. Modern extensions

Ranked by value. Tiers A and B are the "improve and fit modern use" deliverable.

**Tier A — infrastructure (2004 → 2026)**

- **Pluggable storage** instead of GFS: local FS, **S3-compatible object storage** (VersityGW in Compose), and optionally GCS/Azure. The `Storage` interface handles listing, ranged reads and atomic commit.
- **gRPC + protobuf** instead of the custom RPC layer. Supports streaming shuffle, deadlines and retries.
- **Containers**: Docker images for the coordinator and worker, `docker-compose.yml` for a local cluster, and a Helm chart/manifests for Kubernetes.
- **External shuffle service** (optional mode): map output persists in object storage or a shuffle daemon, so losing a worker no longer forces map tasks to re-run (contrast with F8). Both modes are kept so their behaviour can be benchmarked side by side.
- **Compression** (zstd/snappy) for intermediate and output files, plus checksums (CRC32C) on every block.

**Tier B — robustness and operations**

- **Coordinator high availability**: leader election via an etcd lease, or embedded Raft (`hashicorp/raft`). This builds on the F9 WAL.
- **Smarter speculation**: LATE-style straggler detection that compares task progress rates, instead of a fixed "end of job" threshold. Implemented as the opt-in `-speculation=late` beside the paper's rule (#45, [ADR 0019](design/0019-late-speculation.md)); its evaluation is pending in #59 (experiment E7, not yet run).
- **Skew handling**: sample-based range partitioner (TeraSort-style) and hot-key salting.
- **Memory-aware spilling**: a bounded sort buffer that spills to disk under memory pressure.
- **Observability**: Prometheus metrics, OpenTelemetry traces, structured JSON logs. A React or HTMX UI replaces the static status page.
- **Security**: mTLS between components and token auth on job submission.
- **Autoscaling**: Kubernetes horizontal pod autoscaling (HPA) or KEDA, scaled on the queue depth of pending tasks.

**Tier C — flexibility (fits modern workloads)**

- **Multi-language user-defined functions (UDFs)**: Go native; Python via a worker subprocess speaking the shared proto ("streaming" mode, like Hadoop Streaming but typed); optional WASM UDFs for sandboxing.
- **Columnar formats**: Parquet/Arrow input and output, and JSON Lines.
- **Job DAGs / pipelines**: chain jobs (`map→reduce→map→reduce`) with a typed Python/Go builder API. This is a lightweight step toward Spark/Beam.
- **Incremental and micro-batch mode**: re-run only over new input splits (content-hash cache), plus an optional windowed streaming mode.
- **Job API**: REST and gRPC job submission, a `mr` CLI, a multi-job queue with fair scheduling.

---

## 3. Architecture

```text
            ┌──────────── mr CLI / REST / Python SDK ────────────┐
            ▼                                                     │
   ┌──────────────────┐        WAL        ┌───────────────┐       │
   │   Coordinator    │──────────────────▶│ etcd / Raft   │ (HA, tier B)
   │ scheduler, task  │                   └───────────────┘
   │ table, heartbeats│── HTTP :8080  status UI + /metrics
   └────────┬─────────┘
     gRPC   │  RequestTask / ReportTask / Heartbeat
   ┌────────┴───────────┬────────────────────┐
   ▼                    ▼                    ▼
 Worker (Go)        Worker (Go)        Worker (Python)
  ├ map runner        ...                 same proto
  ├ spill/partition
  ├ shuffle server ◀── FetchPartition (streaming gRPC) ── reducers
  └ UDF host: Go plugin | Python subprocess | WASM
            │
            ▼
   Storage: local FS │ S3         │ GCS        (input, output, optional shuffle)
```

This is the target. Today storage is the local filesystem, a shared volume
or an S3-compatible store (#41), every worker is Go, and the UDF host is Phase 5 stretch work.

**Pull-based scheduling** (as in MIT 6.824): idle workers call `RequestTask`. This keeps the coordinator simple and makes heartbeats piggyback naturally.

---

## 4. Repository layout

```text
MapReduce-Reloaded/
├── design.md                    # key decisions; one job's lifecycle; differences from the paper
├── proto/mapreduce/v1/          # coordinator, shuffle, types, log .proto  ← shared contract
├── docs/
│   ├── design/                  # ADRs
│   ├── explainers/              # one plain-language page per feature
│   ├── paper-mapping.md         # F1–F24 → code → tests (checked by internal/papermap)
│   └── file-format.md           # MRKV block and record format
├── go/
│   ├── cmd/{coordinator,worker,mr}/
│   ├── pkg/api/                 # Mapper, Reducer, Combiner, Partitioner, Context
│   ├── internal/coordinator/    # scheduler, task table, registry, recovery
│   ├── internal/worker/         # task loop, heartbeats; cluster and chaos tests
│   ├── internal/task/           # map and reduce attempts, commit
│   ├── internal/shuffle/        # FetchPartition server and client
│   ├── internal/wal/            # append-only, checksummed log
│   ├── internal/{split,partition,extsort,combine,formats,local}/
│   ├── internal/{statusweb,promtext,logs}/   # status page, /metrics, slog
│   ├── internal/{wire,contract,papermap}/    # proto conversion and doc checks
│   └── examples/                # wordcount, grep, urlfreq, reverselinks, termvector, invertedindex, distsort
├── python/
│   ├── pyproject.toml           # package: mapreduce_reloaded
│   ├── mapreduce_reloaded/      # api, split, partition, formats, extsort, combine, local, examples
│   └── tests/
├── deploy/{docker,compose}/     # images; 1 coordinator + 4 workers, monitoring profile
├── scripts/                     # proto generation, e2e, VM check
├── testdata/                    # golden outputs and hash vectors
└── Makefile                     # check, proto, test, lint, cluster-up, e2e
```

`tests/interop/` (Phase 5) does not exist yet; the Python-coordinator interop coverage that does exist lives in `python/tests/test_coordinator_e2e.py` (`make py-e2e`). `bench/` came with #57.

---

## 5. Phased roadmap

Each phase ends in a runnable, tested state.

**Phase 0 — Foundations (about 1 week)**

- Write `proto/` messages and services: `Task`, `TaskKind`, `TaskState`, `RequestTask`, `ReportTask`, `Heartbeat`, `FetchPartition` (server-stream), `SubmitJob`, `JobStatus`, `Counters`.
- Write `docs/file-format.md`: length-prefixed `(key, value)` records with block CRC32C and optional compression. Both languages must read and write it identically.
- Set up the Go module and Python package skeletons, the Makefile (`buf` or `protoc` codegen), and the local quality gate (`make check` plus a pre-push hook: buf lint and breaking checks, generated-code freshness, go test, pytest, ruff, golangci-lint).

**Phase 1 — Sequential core + local mode (F1, F15–F17, F20)**

- Public API in both languages, plus text/kv input formats and the external merge sort.
- `mr run --local wordcount input/ out/`: the whole pipeline in one process. This mode is the **correctness oracle** for every later phase.

**Phase 2 — Distributed core (F2–F7, F10, F12, F14)**

- Coordinator task table and scheduler; workers pull tasks; map partitions spill to local disk; the shuffle server streams partitions; reducers fetch, merge and reduce; atomic rename commit.
- Run with Docker Compose: 1 coordinator and 4 workers on a shared volume.

**Phase 3 — Fault tolerance (F8, F9, F13, F18, F19, F22)**

- Heartbeats with timeouts, map re-execution on worker loss; reduce attempts get the new locations.
- Coordinator write-ahead log (with `-data-dir`), replayed on restart.
- Backup tasks; bad-record skipping; counters de-duplicated by task attempt.
- **Chaos test harness**: `TestChaos` kills, replaces and freezes workers and restarts the coordinator at random, in one process, and asserts that output and counters match local mode. `TestChaosPrograms` does the same for every example program and job option, with lost map output, duplicate RPCs, broken fetches, a torn log tail, a clock set back, full disks, bad records and S3 commit failures too (#155). Driving the same faults against the Compose cluster is #29.

**Phase 4 — Ops & paper parity (F11, F21, F23)**

- Locality-aware scheduling, the status web UI, Prometheus `/metrics`, and all paper example programs.
- `docs/paper-mapping.md` complete: every row F1–F24 linked to code and tests (F24 with Phase 7).

**Phase 5 — Python parity & interop**

- Finish the Python worker (the coordinator is done, #36).
- Python UDF host for Go workers (subprocess over stdio, using the proto framing).
- Interop matrix in `tests/interop/`: {Go, Py} coordinator × {Go, Py} worker × {Go, Py} UDF, all byte-identical to local mode.

**Phase 6 — Modern extensions (Tier A → B → C)**

- In order: S3 storage → compression and checksums → external shuffle mode → coordinator HA (etcd lease) → LATE speculation (done, #45; evaluation pending, #59) → skew/range partitioner → mTLS → Helm + KEDA → Parquet/JSONL → pipelines/DAG → incremental mode.

**Phase 7 — Evaluation & write-up**

- Reproduce the paper's §5 experiments at small scale:
  - grep over about 10 GB, plotting input rate over time;
  - sort (TeraSort-style), comparing normal, no-backup-tasks, and "200 tasks killed" runs, as in the paper's Figure 3.
- Compare against modern baselines: the external-shuffle mode, and optionally Spark on the same data.
- Final report: feature parity table, design deltas from 2004, benchmark charts.

---

## 6. Key technical choices

- **Go libraries:** `google.golang.org/grpc`, `google.golang.org/protobuf`, `aws-sdk-go-v2` for the s3 backend (with `gofakes3` as its test server), `klauspost/compress` for the zstd and Snappy codecs (#42), and the standard library. `log/slog` for logs; `/metrics` is written by `internal/promtext`, not `prometheus/client_golang`; the WAL is one file of checksummed records (`internal/wal`), not `bbolt`. Planned: `hashicorp/raft` or `etcd/client/v3` (Tier B).
- **Python libraries:** `grpcio` and `protobuf`, with `pytest`, `hypothesis` and `ruff` for development. Planned: `asyncio` for the Phase 5 cluster, `boto3`, `zstandard`, `pyarrow`.
- **Determinism:** the hash partitioner uses a fixed, language-independent hash (FNV-1a 64 over key bytes), so Go and Python place the same key in the same partition.
- **Exactly-once output:** a task attempt ID goes into temp filenames. The coordinator accepts only the first `ReportTask(COMPLETED)` per task; later attempts are discarded.

---

## 7. Verification

- **Unit tests:** partitioner (cross-language hash vectors), external sort, record framing round-trip, task-table state machine (property-based).
- **Golden tests:** every example job in distributed mode must produce output identical to `--local` mode on a fixed corpus (e.g. Project Gutenberg texts and a synthetic web-link graph).
- **Fault-injection tests:** `cd go && go test ./internal/worker -run TestChaos -chaos.runs=50` (3 runs by default in `make check`). `make chaos` does the same on the Compose cluster's containers (`docker kill`, `docker pause`, coordinator crashes), seeded (#29).
- **Interop tests:** the Phase 5 matrix.
- **End-to-end smoke test:** `make e2e` starts the Compose cluster, submits word count with `mr submit`, compares the output with the golden output, and checks the job's reduce tasks in `/metrics`.
- **Benchmarks** (`bench/`): throughput, job completion time, and the cost of stragglers with and without backup tasks.
