# Documentation

Where to start depends on the question.

- **How does a job run, and how does it differ from the paper?** [Design](design.md).
- **Is feature F*n* of the paper built, and where?** [Paper mapping](paper-mapping.md).
  A test checks that every code path and test it names exists.
- **What is the plan, and what is done?** [Roadmap](roadmap.md), or [the
  roadmap explained](roadmap-explained.md) in plain language.
- **What do the files look like?** [File format](file-format.md): the MRKV
  block format, its codecs, and the partition hash.
- **How do I run the cluster?** [Cluster VM](cluster-vm.md): the memory budget,
  setup, and daily use. [`deploy/`](../deploy/) has the Compose project.
- **Why was it built this way?** The design records below.
- **How does one component work?** The explainers below.

## Design records

Short ADRs: the decision, the alternatives, and what follows from it.

| ADR | Decision |
|---|---|
| [0001](design/0001-languages-and-shared-contract.md) | Go primary, Python reference, one shared contract |
| [0002](design/0002-local-quality-gate.md) | Local quality gate (`make check` and a pre-push hook) |
| [0003](design/0003-rpc-contract.md) | The gRPC/protobuf contract |
| [0004](design/0004-intermediate-file-format.md) | A custom block format for intermediate data (MRKV) |
| [0005](design/0005-programming-api.md) | The programming API: bytes in, bytes out |
| [0006](design/0006-coordinator-concurrency.md) | Coordinator state behind one mutex |
| [0007](design/0007-write-ahead-log.md) | Logging coordinator decisions under the lock |
| [0008](design/0008-shuffle.md) | Map output stays on the map worker and is pulled |
| [0009](design/0009-failure-detection.md) | Failure detection by worker heartbeats |
| [0010](design/0010-backup-tasks.md) | Backup tasks when nothing is idle |
| [0011](design/0011-bad-records.md) | Skipping bad records after two crashes |
| [0012](design/0012-observability.md) | Status page, hand-written metrics and structured logs |
| [0013](design/0013-commit-on-object-storage.md) | Committing output on object storage |
| [0014](design/0014-ci-on-pull-requests.md) | CI on every pull request (supersedes part of 0002) |

## Explainers

One page for each component, in the order they were built. Each says what it
is, how it works, how we know it works, and answers the questions a reviewer is
likely to ask. The number is the issue that built it.

| Component | Issue |
|---|---|
| [The RPC Contract](explainers/03-rpc-contract.md) | #3 |
| [The MRKV File Format](explainers/04-file-format.md) | #4 |
| [The Partition Hash](explainers/05-partition-hash.md) | #5 |
| [MRKV Reader and Writer](explainers/06-kv-reader-writer.md) | #8 |
| [The Programming API](explainers/07-programming-api.md) | #7 |
| [Input and Output Formats](explainers/08-formats.md) | #9 |
| [External Sort and Grouping](explainers/09-external-sort.md) | #10 |
| [The Combiner](explainers/10-combiner.md) | #11 |
| [Local Mode](explainers/11-local-mode.md) | #12 |
| [Input Splits](explainers/12-splits.md) | #14 |
| [The Coordinator's Task Table](explainers/13-task-table.md) | #15 |
| [Running on a Cluster](explainers/14-cluster.md) | #16 |
| [The Shuffle](explainers/15-shuffle.md) | #18 |
| [When a Worker Dies](explainers/16-worker-failure.md) | #23 |
| [Side Outputs](explainers/17-side-outputs.md) | #28 |
| [When the Coordinator Dies](explainers/18-coordinator-recovery.md) | #24 |
| [Skipping Bad Records](explainers/19-bad-records.md) | #26 |
| [Backup Tasks](explainers/20-backup-tasks.md) | #25 |
| [Counters](explainers/21-counters.md) | #27 |
| [Chaos Testing](explainers/22-chaos.md) | #29 |
| [Locality](explainers/23-locality.md) | #31 |
| [Status Page and Metrics](explainers/24-status.md) | #32 |

## Other documentation

- [`bench/README.md`](../bench/README.md): the data generators, the benchmark
  harness, and the results.
- [`proto/README.md`](../proto/README.md): the RPC contract.
- [`go/README.md`](../go/README.md) and [`python/README.md`](../python/README.md):
  each implementation's layout.
- [`testdata/README.md`](../testdata/README.md): the golden files.
