# Final report

MapReduce-Reloaded, Honors Project 1, solo. This page is the end-to-end
summary; each section links to the page that has the detail.

## Abstract

This repository rebuilds the system in Dean and Ghemawat's *MapReduce:
Simplified Data Processing on Large Clusters* (OSDI 2004) and runs it on
one VM with 4 vCPUs and 8 GB: a coordinator and four worker containers,
each limited to one CPU, under Docker Compose. Go implements the
coordinator, the workers and the `mr` client; Python implements the same
API, file format, partition hash and local mode as a reference, and for
the same uncompressed job both produce the same bytes. All 23 of the
paper's features (F1–F23) are built and each is named with the test that
proves it. The paper's §5 experiments and six more (E0–E5) ran on the VM.
The mechanisms behave as the paper says: phases overlap, backups cut a
straggler's tail (17.6 s without them, 11.2 s with), and a job survives a
lost worker or a restarted coordinator with the same output. What changes
at four workers is the size of fixed costs: startup, heartbeats and the
10 s failure timeout are a large share of a 10-second job.

## Architecture

A client on the host (`mr`) submits a job: the input paths, an output
path, M, R, and a Go source file with the map and reduce functions. The
coordinator splits the input into M map tasks and hands tasks to workers
that ask for them. A map task reads its split, runs the job's map
function (interpreted from the job file by Yaegi, or compiled in for the
built-in programs), combines, sorts, and writes one MRKV file per reduce
task to the worker's own disk. A reduce task fetches its partition from
every map worker over gRPC, merges, groups, reduces, and commits
`part-r-NNNNN` atomically to shared storage: a host directory mounted at
`/data`, or S3-compatible object storage. The coordinator logs each
decision before it replies, so a restarted coordinator replays the log
and the job continues.

- [Design](../design.md): the assignment's design questions, one job from
  submission to completion, and sequence diagrams for scheduling, the
  shuffle, failure recovery and the commit.
- [Design records](README.md#design-records): eighteen ADRs, one per
  decision.
- [Explainers](README.md#explainers): one page per component.

## Feature parity

| Area | Paper features | State |
|---|---|---|
| Programming model, combiner, ordering, formats, partitioning, local mode, side outputs, counters, example programs | F1, F14–F18, F20, F22, F23 | Go and Python |
| Splitting, master and tasks, shuffle, output files, atomic commit, task granularity | F2–F7, F10, F12 | Go; Python for splitting, partitioning and commit in local mode |
| Worker failure, master recovery, backup tasks, bad records | F8, F9, F13, F19 | Go |
| Locality, status pages | F11, F21 | Go |
| Performance experiments | F24 | [Evaluation](evaluation.md) |

[Paper mapping](paper-mapping.md) names the code and the tests for each
feature; `internal/papermap` fails the build if a named file or test does
not exist.

Beyond the paper: S3-compatible storage (#41), zstd and Snappy compression
of intermediate files (#42), job files interpreted by the workers (ADR
0015), a write-ahead log in place of checkpoints (ADR 0007), Prometheus
metrics, JSON logs and a Grafana dashboard (ADR 0012).

## Differences from 2004

The full table, with the reason and the record for each, is in
[design.md](../design.md#differences-from-the-2004-paper). The ones that
change behavior:

- **gRPC and protobuf** replace Google's RPC, so Go and Python share one
  contract.
- **A bind-mounted directory or S3** replaces GFS. An S3 commit is a copy
  and a delete, not a rename ([ADR 0013](design/0013-commit-on-object-storage.md)).
- **A write-ahead log** replaces periodic checkpoints, so a coordinator
  restart no longer aborts the job.
- **Workers send heartbeats**; the coordinator never connects to a worker.
- **Backups start when no task of the phase is idle**, because the paper
  gives no threshold.
- **A job is a source file**, interpreted by each worker, because the
  workers are static binaries and the assignment's client submits a file.
- **Four one-CPU containers** replace about 1,800 machines.

## Evaluation

| Experiment | Result |
|---|---|
| §5.2 Grep, 10 GB | 18.3 s; the input rate rises, plateaus near one disk's rate, and falls, as in Figure 2 |
| §5.3 Sort, 1 GiB | 7.8 s; map, shuffle and output overlap as in Figure 3(a) |
| §5.4 and E2: backup tasks | With one worker at 0.2 CPU, 11.2 s with backups and 17.6 s without (+56%; the paper saw +44%). With no straggler, no significant cost over ten interleaved pairs |
| §5.5 and E3: losing a worker | Every job finished, and its output checksum matched a normal run's; +79% to +232%, because one of four workers is a quarter of the cluster and the 10 s timeout is most of a 12 s job |
| E0: scaling | 3.8× on four workers for word count, 2.9× for sort, 2.3× for grep |
| E1: combiner | 15× to 78× fewer bytes shuffled, 18–44% faster |
| E4: coordinator restart | 1.6 s, every finished task kept |
| E5: interpreted job file | 2.4× slower than compiled with the compiled helpers, 10.5× without |

[Evaluation](evaluation.md) compares each run with the paper's figure;
[bench/README.md](../bench/README.md#evaluation-e0e5) has the plots, the
tables and the commands, and `bench/evaluation/` has the data.

## How it is tested

[Testing](testing.md) lists every kind of test and how to run it. In
short: unit tests in both languages, golden vectors shared by Go and
Python for the file format, the hash and the messages, in-process cluster
tests, a seeded chaos test that kills workers and restarts the
coordinator, fuzzing of the decoders, a model-based test of the
coordinator's RPCs, coverage floors per package (Go 91.7%, Python 100%),
and mutation testing of the core packages. `make check` runs the fast set
locally and CircleCI runs it on every pull request.

## The demo

About five minutes, following the [runbook](runbook.md#5-the-demo):

1. Show the four workers limited to one CPU each (§5.1).
2. Upload the example data, submit `examples/wordcount.go` with `mr
   submit`, and read the output (§5.3).
3. Submit a sort and kill a worker in the middle; show the status page
   marking it dead, the lost tasks running again, and the output matching
   (§5.4).
4. Show a benchmark's rates over time and the evaluation plots (§5.5,
   §5.6).

## Lessons learned

- **Measure in pairs, in turn.** The first E2 comparison suggested
  backups cost 11% with no straggler. It was three runs per setting, with
  overlapping times. Ten interleaved pairs found no significant
  difference. Experiments that compare settings now alternate them within
  each repeat.
- **Scale changes which costs matter.** The paper's minute of startup and
  its failure timeout were small next to 891 s. Here they are most of the
  job, so a feature can work exactly as designed and still look costly.
- **With R equal to the workers, every job is "near completion".** The
  first reduce task to finish frees a worker with nothing else to do, so
  backups run in every job. They use idle workers and cost nothing
  measurable, but they shuffle the data again.
- **A shared contract needs shared vectors.** Go and Python agree byte for
  byte because both test against the same golden files, not because they
  were written to the same prose.
- **Coverage is not test strength.** Mutation testing found tests that
  ran code without checking its result, in packages at 90% coverage or
  more.
- **Small pull requests, each reviewed.** About 120 merged pull requests,
  each reviewed by an AI agent and CI before it merged; several real bugs
  were found in review, not by tests.

## Future work

Kept open as `[stretch]` issues:

- **Python parity (#35–#39):** a Python coordinator and worker, Python
  map and reduce functions on Go workers, and a test matrix over every
  Go/Python combination.
- **Modern extensions (#43–#55):** an external shuffle service,
  coordinator high availability, LATE-style speculation, skew handling,
  memory-aware spilling, tracing, mTLS, Kubernetes, Parquet and JSON
  Lines, a job service, job DAGs, incremental execution and WebAssembly
  user functions.
- **Their evaluation (#59)** against this baseline.
