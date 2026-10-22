# Roadmap

The full design and phased plan for MapReduce-Reloaded. Feature IDs (F1–F23)
are tracked in [paper-mapping.md](paper-mapping.md). Each unit of work is a
GitHub issue, labelled by phase and numbered in the order it should be done.

## Context

The goal is to rebuild the core of Google's MapReduce as described in Dean &
Ghemawat, *MapReduce: Simplified Data Processing on Large Clusters* (OSDI
2004). The rebuild should include as many of the paper's original features as
possible, then extend them for modern infrastructure and modern use cases.

- **Scope:** a real distributed system. A coordinator and N workers talk over
  the network. The cluster runs locally with Docker Compose and can optionally
  be deployed to Kubernetes.
- **Languages:** Go is the primary implementation. Python is a second,
  complete reference implementation. The original system was C++; see
  [ADR 0001](design/0001-languages-and-shared-contract.md) for why we use Go
  and Python instead.
- **Key architectural idea:** both languages share one contract:
  - gRPC/protobuf for RPC;
  - a documented on-disk record format;
  - a language-neutral partition hash.

  Go and Python components can therefore be mixed in one cluster, and running
  the same job through both implementations gives a byte-for-byte correctness
  check.

## Development process

- Work proceeds one small, reviewable unit at a time. Each issue is roughly
  one PR, and each commit covers one concept.
- Every feature ships with tests. CI must be green before merge.
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
| F1 | Programming model: `map(k1,v1)→list(k2,v2)`, `reduce(k2,list(v2))→list(v2)` (§2) | `Mapper`/`Reducer` interfaces in Go; decorators in Python |
| F2 | Input split into M pieces of 16–64 MB (§3.1) | `InputSplitter` with a configurable split size; split on record boundaries |
| F3 | One master, M map tasks, R reduce tasks (§3.1) | Coordinator with a task scheduler; workers pull tasks over RPC |
| F4 | Intermediate pairs buffered, partitioned by `hash(key) mod R`, written to local disk (§3.1) | In-memory buffer, spill to R partition files per map task |
| F5 | Reducers fetch partitions remotely, sort by key, group, apply reduce (§3.1) | `FetchPartition` streaming RPC on each worker, plus external merge sort |
| F6 | R output files, one per reduce task (§3.1) | `part-r-00000` … in the output directory or bucket |
| F7 | Master data structures: task state (idle / in-progress / completed), worker identity, intermediate file locations (§3.2) | `TaskTable` and `WorkerRegistry` in the coordinator |
| F8 | Worker failure detection by heartbeat; reset its tasks; **re-run completed map tasks** of a failed worker; notify reducers (§3.3) | Heartbeat timeout, task re-queue, location update pushed to reducers |
| F9 | Master failure: periodic checkpoints and recovery (§3.3) | Coordinator state snapshot + write-ahead log (WAL); restart resumes the job |
| F10 | Atomic commit semantics: map output reported to master once; reduce output via atomic rename (§3.3) | Write to temp file, `rename`/object-store commit; duplicate completions ignored |
| F11 | Locality: schedule map tasks near their input data (§3.4) | Workers advertise a host/zone label; scheduler prefers a matching split |
| F12 | Task granularity: M and R much larger than the number of workers (§3.5) | Configurable; defaults derived from input size and worker count |
| F13 | Backup tasks for stragglers near the end of the job (§3.6) | Speculative copies once the job is about 95% done; first finisher wins |
| F14 | Custom partitioning function, e.g. `hash(Hostname(url))` (§4.1) | `Partitioner` interface; built-in hash and range partitioners |
| F15 | Ordering guarantee: keys sorted within each partition (§4.2) | Comes from the sort-merge in the reduce phase; test asserts it |
| F16 | Combiner function (§4.3) | Optional `Combiner`, run on spill and on merge |
| F17 | Input/output types: text lines, key/value, custom `Reader`/`Writer` (§4.4) | Pluggable `InputFormat`/`OutputFormat` registry |
| F18 | Side effects: atomic, idempotent auxiliary outputs (§4.5) | Task-scoped temp directory, committed together with the main output |
| F19 | Skipping bad records: worker reports the crashing record; master tells retries to skip it (§4.6) | Record-sequence tracking; skip list after 2 failures on the same record |
| F20 | Local execution mode for debugging (§4.7) | `mr run --local`: everything in one process, sequential, deterministic |
| F21 | Status information: HTTP status pages (§4.8) | Coordinator web UI showing progress, throughput, failures and worker list |
| F22 | Counters, aggregated by master, de-duplicated across re-executions (§4.9) | `ctx.Counter("name").Inc()`; only counted from the winning task attempt |
| F23 | Paper example programs (§2.3, §5) | grep, word count, URL access frequency, reverse web-link graph, term vectors, inverted index, distributed sort |

---

## 2. Modern extensions

Ranked by value. Tiers A and B are the "improve and fit modern use" deliverable.

**Tier A — infrastructure (2004 → 2026)**
- **Pluggable storage** instead of GFS: local FS, **S3-compatible object storage** (MinIO locally), and optionally GCS/Azure. The `Storage` interface handles listing, ranged reads and atomic commit.
- **gRPC + protobuf** instead of the custom RPC layer. Supports streaming shuffle, deadlines and retries.
- **Containers**: Docker images for the coordinator and worker, `docker-compose.yml` for a local cluster, and a Helm chart/manifests for Kubernetes.
- **External shuffle service** (optional mode): map output persists in object storage or a shuffle daemon, so losing a worker no longer forces map tasks to re-run (contrast with F8). Both modes are kept so their behaviour can be benchmarked side by side.
- **Compression** (zstd/snappy) for intermediate and output files, plus checksums (CRC32C) on every block.

**Tier B — robustness and operations**
- **Coordinator high availability**: leader election via an etcd lease, or embedded Raft (`hashicorp/raft`). This builds on the F9 WAL.
- **Smarter speculation**: LATE-style straggler detection that compares task progress rates, instead of a fixed "end of job" threshold.
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

```
            ┌──────────── mr CLI / REST / Python SDK ────────────┐
            ▼                                                     │
   ┌──────────────────┐  WAL + snapshots  ┌───────────────┐       │
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
   Storage: local FS │ S3/MinIO │ GCS        (input, output, optional shuffle)
```

**Pull-based scheduling** (as in MIT 6.824): idle workers call `RequestTask`. This keeps the coordinator simple and makes heartbeats piggyback naturally.

---

## 4. Repository layout

```
MapReduce-Reloaded/
├── proto/mapreduce/v1/          # coordinator.proto, worker.proto, records.proto  ← shared contract
├── docs/
│   ├── paper-mapping.md         # F1–F23 → code locations → tests
│   ├── file-format.md           # intermediate/output record framing spec
│   └── design.md
├── go/
│   ├── go.mod
│   ├── cmd/{coordinator,worker,mr}/          # binaries
│   ├── pkg/api/                 # public Mapper/Reducer/Combiner/Partitioner/Context
│   ├── internal/coordinator/    # scheduler.go, tasktable.go, registry.go, wal.go, speculate.go, status/
│   ├── internal/worker/         # mapper.go, reducer.go, spill.go, shuffle_server.go, extsort.go, udfhost/
│   ├── internal/storage/        # local.go, s3.go, commit.go
│   ├── internal/formats/        # text, kv (framed), jsonl, parquet
│   ├── internal/partition/      # hash.go, range.go (sampling)
│   └── examples/                # wordcount, grep, invertedindex, reverselinks, urlfreq, termvector, sort
├── python/
│   ├── pyproject.toml           # package: mapreduce_reloaded
│   ├── mapreduce_reloaded/
│   │   ├── api.py               # @mapper/@reducer/@combiner decorators, Context, counters
│   │   ├── coordinator/         # asyncio + grpcio reference coordinator
│   │   ├── worker/              # reference worker, also usable as UDF host for Go workers
│   │   ├── storage/, formats/, partition/
│   │   ├── local.py             # local execution mode (F20)
│   │   └── pipeline.py          # job DAG builder (tier C)
│   ├── examples/
│   └── tests/
├── deploy/{docker,compose,helm}/
├── bench/                       # data generators, benchmark harness, results
├── tests/interop/               # Go↔Python cross-implementation tests
├── Makefile                     # proto gen, build, test, lint, cluster-up
└── .github/workflows/ci.yml
```

---

## 5. Phased roadmap

Each phase ends in a runnable, tested state.

**Phase 0 — Foundations (about 1 week)**
- Write `proto/` messages and services: `Task`, `TaskKind`, `TaskState`, `RequestTask`, `ReportTask`, `Heartbeat`, `FetchPartition` (server-stream), `SubmitJob`, `JobStatus`, `Counters`.
- Write `docs/file-format.md`: length-prefixed `(key, value)` records with block CRC32C and optional compression. Both languages must read and write it identically.
- Set up the Go module and Python package skeletons, the Makefile (`buf` or `protoc` codegen), CI (go test, pytest, ruff, golangci-lint) and pre-commit.

**Phase 1 — Sequential core + local mode (F1, F15–F17, F20)**
- Public API in both languages, plus text/kv input formats and the external merge sort.
- `mr run --local wordcount input/ out/`: the whole pipeline in one process. This mode is the **correctness oracle** for every later phase.

**Phase 2 — Distributed core (F2–F7, F10, F12, F14)**
- Coordinator task table and scheduler; workers pull tasks; map partitions spill to local disk; the shuffle server streams partitions; reducers fetch, merge and reduce; atomic rename commit.
- Run with Docker Compose: 1 coordinator, 4 workers, MinIO.

**Phase 3 — Fault tolerance (F8, F9, F13, F18, F19, F22)**
- Heartbeats with timeouts, map re-execution on worker loss, reducers notified of new locations.
- Coordinator WAL and snapshot, with recovery on restart.
- Backup tasks; bad-record skipping; counters de-duplicated by task attempt.
- **Chaos test harness**: kill and pause workers at random (`docker kill`, `SIGSTOP`), crash workers in the middle of a task, and assert the output still matches local mode.

**Phase 4 — Ops & paper parity (F11, F21, F23)**
- Locality-aware scheduling, the status web UI, Prometheus `/metrics`, and all paper example programs.
- `docs/paper-mapping.md` complete: every row F1–F23 linked to code and tests.

**Phase 5 — Python parity & interop**
- Finish the Python coordinator and worker.
- Python UDF host for Go workers (subprocess over stdio, using the proto framing).
- Interop matrix in `tests/interop/`: {Go, Py} coordinator × {Go, Py} worker × {Go, Py} UDF, all byte-identical to local mode.

**Phase 6 — Modern extensions (Tier A → B → C)**
- In order: S3 storage → compression and checksums → external shuffle mode → coordinator HA (etcd lease) → LATE speculation → skew/range partitioner → mTLS → Helm + KEDA → Parquet/JSONL → pipelines/DAG → incremental mode.

**Phase 7 — Evaluation & write-up**
- Reproduce the paper's §5 experiments at small scale:
  - grep over about 10 GB, plotting input rate over time;
  - sort (TeraSort-style), comparing normal, no-backup-tasks, and "200 tasks killed" runs, as in the paper's Figure 3.
- Compare against modern baselines: the external-shuffle mode, and optionally Spark on the same data.
- Final report: feature parity table, design deltas from 2004, benchmark charts.

---

## 6. Key technical choices

- **Go libraries:** `google.golang.org/grpc`, `log/slog`, `prometheus/client_golang`, `aws-sdk-go-v2` (S3/MinIO), `klauspost/compress` (zstd), `hashicorp/raft` or `etcd/client/v3`, `bbolt` for the WAL.
- **Python libraries:** `grpcio`, `asyncio`, `pyarrow` (Parquet), `boto3`, `zstandard`, `pytest`, `hypothesis` (property tests).
- **Determinism:** the hash partitioner uses a fixed, language-independent hash (FNV-1a 64 over key bytes), so Go and Python place the same key in the same partition.
- **Exactly-once output:** a task attempt ID goes into temp filenames. The coordinator accepts only the first `ReportTask(COMPLETED)` per task; later attempts are discarded.

---

## 7. Verification

- **Unit tests:** partitioner (cross-language hash vectors), external sort, record framing round-trip, task-table state machine (property-based).
- **Golden tests:** every example job in distributed mode must produce output identical to `--local` mode on a fixed corpus (e.g. Project Gutenberg texts and a synthetic web-link graph).
- **Fault-injection tests:** the chaos harness from Phase 3 runs in CI, using a small Compose cluster.
- **Interop tests:** the Phase 5 matrix.
- **End-to-end smoke test:** `make cluster-up && mr submit examples/wordcount --input s3://bench/gutenberg --reduces 8`, then check the status UI at `localhost:8080`, the Prometheus metrics, and the output diff.
- **Benchmarks** (`bench/`): throughput, job completion time, and the cost of stragglers with and without backup tasks.
