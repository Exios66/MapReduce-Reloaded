# Language choice: Go, with Python as a second implementation

The 2004 system was written in C++. This repository uses Go for the cluster
and Python for a second implementation. This page gives the constraints
that led there, the evidence that Go is enough, what it costs, and what
was rejected. [ADR 0001](design/0001-languages-and-shared-contract.md) is
the short decision record; this page is the argument behind it. The
[Language](../design.md#language) section of the design is the summary.

Numbers on this page are copied from the page named beside them. Where
nothing was measured, the page says so.

## 1. Constraints

| Constraint | Source |
|---|---|
| One VM with 4 vCPUs and 8 GB, running one coordinator and four workers limited to one CPU each | [Design](../design.md), [cluster VM](cluster-vm.md) |
| Memory limits of 256 MB for the coordinator and 768 MB for each worker, with a 256 MB sort buffer inside the worker's share | [Cluster VM](cluster-vm.md#memory-budget) |
| A client outside every container, which submits a file of map and reduce functions | [Design](../design.md#client-interface) |
| Workers are static binaries in a distroless image: no compiler, no shell, no cgo | [ADR 0015](design/0015-user-job-files.md) |
| A solo project with a code deadline of Nov 16, 2026 | [Design](../design.md) |
| Fidelity to the paper is about behaviour (what the system does under load, failure and stragglers), not about the language | [ADR 0001](design/0001-languages-and-shared-contract.md) |
| The checks run on a laptop with `make check`, and on CircleCI's Free plan | [CI](ci.md) |

Two of these shape the choice most. The workers have no compiler, so a user's
job has to be interpreted or compiled in. And the project has to show that
the design does not depend on one language, without paying for a second full
cluster.

## 2. Why Go

### The argument

The paper's library is C++, linked into the user's program (its Appendix A
shows a word count that way). This repository replaces each piece of that
with something Go already has, or with something the design chose
differently ([the full table](../design.md#differences-from-the-2004-paper)).

- **The master's bookkeeping.** The coordinator serves many workers at
  once and keeps its state under one mutex
  ([ADR 0006](design/0006-coordinator-concurrency.md)). Goroutines and the
  race detector fit that.
- **The wire.** gRPC is first-class in Go, including the server-streaming
  call that the shuffle uses ([ADR 0003](design/0003-rpc-contract.md),
  [ADR 0008](design/0008-shuffle.md)).
- **Deployment.** A Go binary is one static file, so the image is
  `distroless/static` with three binaries and nothing else
  ([`deploy/docker/Dockerfile`](../deploy/docker/Dockerfile)).
- **Memory.** The garbage collector replaces manual memory management. The
  worker also asks the runtime to keep the heap under three quarters of
  its container's memory limit (`limitHeap` in `go/cmd/worker/main.go`, #47).
- **CPU limits.** Since Go 1.25, `GOMAXPROCS` follows the container's CPU
  limit, so a one-CPU worker does not start more threads than it can run
  ([cluster VM](cluster-vm.md#cpu-budget)).
- **A known comparison.** ADR 0001 notes that Go is the language of MIT
  6.824's MapReduce lab.

### Evidence that Go is adequate

- **Scaling.** On the course VM, each job got faster with more one-CPU
  workers. At four workers against one, word count ran 3.8× faster,
  `distsort` 2.9× and grep 2.3×
  ([bench/README.md, E0](../bench/README.md#e0-scaling-over-workers)). The
  small gain for grep is fixed cost in a job of about a second, and the
  `distsort` gain is probably limited by one shared disk; both explanations
  are in that section.
- **Features.** All 24 of the paper's features, F1 to F24, are marked done in
  the [paper mapping](paper-mapping.md). Go has code for F1 to F23. F24 is
  the benchmark harness, which is shell and Python scripts. A test
  (`go/internal/papermap`) fails if a path or test the mapping names does not
  exist.
- **Tests.** Statement coverage is about 90% of the Go
  statements in 31 checked packages (`make cover` prints the current figure;
  [testing](testing.md#coverage)).
  Each package has a floor in [`go/cover-floors.txt`](../go/cover-floors.txt),
  and the Go tests run under the race detector.

### What Go does not replace

Go does not make this a reproduction of Google's system, and nothing here
claims it does.

- **GFS.** A bind-mounted host directory, or S3-compatible storage, holds
  input and output. There is no replicated, chunked file system, and no
  locality to exploit on one disk
  ([ADR 0016](design/0016-shared-storage-and-host-client.md)).
- **Google's RPC.** gRPC and protobuf replace it
  ([ADR 0003](design/0003-rpc-contract.md)).
- **Scale.** The paper's cluster had about 1,800 machines (§5.1). This one
  has four workers on one VM. Absolute times are not comparable with the
  paper's, and [the evaluation](evaluation.md) compares shapes only.
- **C++ memory and speed tuning.** This repository does not try to match
  the paper's library byte for byte in memory use or per-record cost. No
  C++ MapReduce was built or run here, so there is **no measurement of Go
  against C++**. The adequacy claim above is that the Go cluster scales and
  passes its tests, not that it is as fast.

## 3. What Go costs

### Garbage collection

Garbage collection is **not measured separately** in E0 to E6. No
experiment turns it off, changes its settings or reports pause times. Its CPU
time is charged to the same one-CPU quota as the tasks, so it is inside
every worker number on this page, and its share is unknown.

What is bounded instead is memory:

- `TestSortMemoryBounded` sorts 80 MB with a 4 MB budget and checks that
  the live heap stays under 16 MB. It measured 4.2 and 6.0 MB uncompressed
  ([bench/README.md](../bench/README.md#benchmarks-and-limits-160)).
- `make soak` runs 1 GiB through workers limited to 256 MB, and fails if any
  container is killed for exceeding its limit ([runbook §5.8](runbook.md#58-the-soak-run-160)).
  It is not part of `make check`, and this page does not record a result
  for it.

### Interpreted job files

A user's job is a Go source file that each worker interprets with
[Yaegi](https://github.com/traefik/yaegi), because the image has no
compiler ([ADR 0015](design/0015-user-job-files.md)). Yaegi's version is
pinned in `go/go.mod`. This is a cost of Go as the job language in a static
image, not of Go as the implementation language. The built-in programs are
compiled in, and the benchmarks use them.

E5 measured the cost on 1 GiB of Zipf word count, M = 32, R = 4, median of
three runs
([bench/README.md, E5](../bench/README.md#e5-what-an-interpreted-job-file-costs)):

| Setting | Job time |
|---|---:|
| Compiled `wordcount` | 38.12 s |
| Job file `examples/wordcount.go`, which uses the compiled helpers `api.Words` and `api.Sum` | 91.08 s (2.4×) |
| Job file `bench/jobs/wordcount-naive.go`, which tests for word boundaries itself | 398.97 s (10.5×) |

A profile traced most of the 10.5× to the job file's own per-byte word test,
which the interpreter ran for every byte. The helpers move that work into
compiled code. A job file that does heavy per-byte work in its own code
pays the full cost.

Two smaller costs come from the same choice. A job file's `Reduce` takes a
`func(yield func([]byte) bool)`, not the compiled API's `iter.Seq[[]byte]`,
because Yaegi cannot import `iter` and panics on a range over a function.
And the interpreter is not a sandbox
([ADR 0015](design/0015-user-job-files.md),
[ADR 0018](design/0018-trust-model.md)).

### Toolchain and CI

- **Generated code in both languages.** `proto/` is compiled to Go
  (`go/gen`) and Python (`python/mapreduce_reloaded/gen`), and both are
  committed. The generators are pinned in the Makefile (`protoc-gen-go`,
  `protoc-gen-go-grpc`, `buf`), and `make proto-check` fails if the output
  is stale or the change breaks an existing field.
- **cgo for tests.** The binaries are built with `CGO_ENABLED=0`, but
  `go test -race` needs a C compiler. `gcc` is not installed on the course
  VM by default ([cluster VM](cluster-vm.md#the-vm)), and CircleCI's Go job
  runs on an image that includes one ([CI](ci.md)).
- **A pinned Go.** `scripts/ci-install-go.sh` reads the version from
  `go/go.mod` and checks the download's SHA-256. golangci-lint is pinned in
  `scripts/vm-setup.sh`.
- **Four CircleCI jobs**, from [`.circleci/config.yml`](../.circleci/config.yml):
  `proto`, `go`, `python-3.11` and `python-3.13`. The `go` job also installs
  the Python package, because the Go golden tests run it. How long the jobs
  take, and what they cost in credits, is not recorded here.

## 4. Why Python as a second implementation, and not Python alone

### What the second implementation is for

Python is a reference implementation of the API, splitting, the partition
hash, sorting, the combiner, the MRKV file format, local mode, and a
coordinator server. It covers 14 of the 24 features in the paper mapping
(its column lists F1, F2, F4, F6, F10, F12, F14 to F18, F20, F22 and F23).
Its job is to be a second reading of the shared contract that can be run and
compared. ADR 0001 adds that Python is easier to read and easier to write a
map function in; that is a judgement, not a measurement.

The strongest check it gives is to run the same job through Go and Python
and compare the output bytes. `TestExamplesMatchPython`
(`go/cmd/mr/examples_test.go`) does this for every example program, as
text and as uncompressed MRKV, with R = 1 and R = 4. The inputs and parameters
are in `testdata/examples/`.

### The shared contract

Both implementations are tested against one set of files in this repository,
so they agree because of the files and not because they were written to the
same prose.

| Part of the contract | Where it is defined | Shared test data | Go test | Python test |
|---|---|---|---|---|
| Wire messages and RPCs | [`proto/mapreduce/v1/`](../proto/mapreduce/v1/) ([README](../proto/README.md)) | `testdata/proto/` (text and binary forms) | `go/internal/contract` | `python/tests/test_contract.py` |
| MRKV block format, with its codecs | [file format](file-format.md) | `testdata/format/` and its `vectors.json` | `go/internal/formats/kv` | `test_kv.py`, `test_format_vectors.py` |
| Partition hash, FNV-1a 64 mod R | [file format](file-format.md), [explainer](explainers/05-partition-hash.md) | `testdata/hash/vectors.json` | `TestVectors` in `go/internal/partition` | `test_partition_hash.py` |

The hash is `partition.FNV1a64` in Go and `fnv1a64` in Python. Without
agreement on it, a key could land in different reduce tasks, and a Go worker
and a Python worker would never produce the same output.

### Why not Python alone

ADR 0001 rejects it because the interpreter and the GIL make Python a poor
fit for the performance experiments in §5 of the paper. **That argument is
not backed by a measurement in this repository:** no Python cluster exists,
so no Python run was timed against a Go run. It rests on general
knowledge of the interpreter, not on a number from this repository. A Python-only
system would also lack the second reading of the contract that makes the
byte comparison possible.

## 5. What the combination costs

- **Dual maintenance.** The API, splitting, partitioning, sorting, the
  combiner, the file format, local mode, the example programs and a
  coordinator exist twice. Any change to the wire or file contract is made in
  `proto/` or `docs/file-format.md` first, then in both languages (ADR 0001).
- **Drift.** The two can disagree about something no shared file covers.
  What limits that:
  - the shared vectors above, which fail the build of either side;
  - `TestExamplesMatchPython`, which compares whole jobs. It runs only where a
    Python that can import the package is found, and skips otherwise.
    CircleCI's `go` job installs Python for it;
  - the paper-mapping check, which keeps the docs pointing at code and
    tests that exist;
  - `make py-e2e`, which runs the Python coordinator with the Go worker and
    `mr` commands over localhost. It sets `MR_REQUIRE_GO`, so a skip fails
    the `go` job;
  - `make proto-check`, so generated code cannot go stale;
  - per-package coverage floors in both languages (Python's are in
    `python/cover-floors.txt`, and its coverage is above 99%,
    [testing](testing.md#coverage)).

  A vector tests only what it contains, and the example comparison runs in
  local mode.

### The honest limit

Do not read this as two equal implementations.

- The **Compose cluster is Go only.** The image holds the `coordinator`,
  `worker` and `mr` binaries and no Python.
- A **Python coordinator exists and is tested**, with Go workers and the Go
  client, by `make py-e2e`. That is the only mixed combination checked.
- There is **no Python worker** (#37), and **no Python map or reduce
  functions on Go workers** (#38). Job files are Go source, run by Yaegi.
- The full matrix of Go and Python combinations is stretch work
  ([roadmap](roadmap.md), [final report](report.md#future-work)).

So the shape is a production implementation plus an oracle, not a choice of
two production clusters. The Python side shows the contract is
language-independent for data and, through the coordinator, for the
protocol. It does not show that a Python worker would be correct, because
none exists.

## 6. Alternatives rejected

The first four rows are the alternatives in ADR 0001. Go only is not listed
there; its row follows from the ADR's decision.

| Alternative | Why not |
|---|---|
| C++, as in the paper | Most faithful to the code, slow to develop, and not a language the author works in. Fidelity here is behavioural |
| Python only | The interpreter and the GIL fit the performance experiments poorly (not measured here, see section 4). It would also have no second implementation to compare with |
| Rust | Best raw performance, but development speed would limit how many of the paper's features get built |
| Two implementations with no shared contract | Simpler at first, but it loses the byte-for-byte comparison, the strongest correctness check the project has |
| Go only | Cheaper to maintain: no dual code, and no Python CI jobs. But nothing independent would check the file format, the hash or the wire messages, and they would be defined by one implementation's behaviour |

## Where to look next

- [ADR 0001](design/0001-languages-and-shared-contract.md): the decision.
- [Paper mapping](paper-mapping.md): which implementation has which feature.
- [Testing](testing.md): every kind of test and the coverage floors.
- [Evaluation](evaluation.md) and [bench/README.md](../bench/README.md#evaluation-e0e6): the experiments.
