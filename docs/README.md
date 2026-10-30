# Documentation

Where to start depends on the question.

- **What are the key decisions, and how will they be evaluated?** [Design](../design.md#key-decisions):
  the client, language, communication, storage, formats, tests and evaluation.
- **How does a job run, and how does it differ from the paper?** [Design](../design.md).
- **Is feature F*n* of the paper built, and where?** [Paper mapping](paper-mapping.md).
  A test checks that every code path and test it names exists.
- **What is the plan, and what is done?** [Roadmap](roadmap.md), or [the
  roadmap explained](roadmap-explained.md) in plain language.
- **What do the files look like?** [File format](file-format.md): the MRKV
  block format, its codecs, and the partition hash.
- **How is it tested, and how do I run each kind of test?** [Testing](testing.md),
  with the coverage floors.
- **What checks run on a pull request, and how is CI set up?** [CI](ci.md):
  the six CircleCI jobs, caching, the Free-plan limits, the steps the owner
  completes, and troubleshooting.
- **How do the results compare with the paper's?** [Evaluation](evaluation.md):
  §5's grep, sort, backup tasks and machine failures, run again on the VM.
- **What data does it run on, and how does it match the paper's?** [Data](data.md):
  the generators for §5, the golden corpus, and real datasets for the §2.3
  programs, checked against an oracle (`make real-data`).
- **What was built, how does it compare, and what was learned?** The
  [final report](report.md): the summary of everything below, with the
  demo.
- **How do I get from SSH to a running demo?** The [runbook](runbook.md).
  The five-minute walkthrough for a review is [the demo](demo.md). A notebook
  version of the runbook demo runs cell by cell in [`examples/demo.ipynb`](../examples/demo.ipynb).
- **How do I run the cluster?** [Cluster VM](cluster-vm.md): the VM's specifications,
  the memory budget, setup, and daily use. [`deploy/`](../deploy/) has the Compose project.
- **Why Go and Python, not C++?** [Language choice](language-choice.md): the
  constraints, the evidence that Go is enough, what it costs, and why Python
  is a second implementation and not the only one.
- **What does each container do, and where does the data live?**
  [Docker topology](docker-topology.md): for a reader new to Docker, the
  services, ports, volumes and the reasons for each choice.
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
| [0014](design/0014-ci-on-pull-requests.md) | CI on every pull request, now on CircleCI Free (supersedes part of 0002) |
| [0015](design/0015-user-job-files.md) | User job files, interpreted by the workers |
| [0016](design/0016-shared-storage-and-host-client.md) | Shared storage is a host directory, and the client runs on the host |
| [0017](design/0017-disk-and-memory-bounds.md) | Workers delete ended jobs' map output; disk, memory and logs are bounded |
| [0018](design/0018-trust-model.md) | The cluster trusts its network; what that costs, and what is bounded |
| [0019](design/0019-late-speculation.md) | LATE speculation as an opt-in mode beside the paper's backup tasks |
| [0020](design/0020-implicated-only-ci.md) | Implicated-only CI: dynamic config runs only the jobs a PR's files implicate |
| [0021](design/0021-no-ci-on-main-pushes.md) | No CI jobs on pushes to main: the merged tree already passed on its PR |

## Explainers

One page for each component, in the order they were built. Each says what it
is, how it works, how we know it works, and answers the questions a reviewer is
likely to ask. The number is the issue that built it. File numbering starts
at 03: there are no explainers 01 and 02, and the files were not renumbered.

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
| [The Python Coordinator](explainers/25-python-coordinator.md) | #36 |

## Other documentation

- [`bench/README.md`](../bench/README.md): the data generators, the benchmark
  harness, and the results.
- [`proto/README.md`](../proto/README.md): the RPC contract.
- [`go/README.md`](../go/README.md) and [`python/README.md`](../python/README.md):
  each implementation's layout.
- [`testdata/README.md`](../testdata/README.md): the golden files, and
  [`testdata/corpus/README.md`](../testdata/corpus/README.md): the word-count
  corpus.
- [`deploy/README.md`](../deploy/README.md): the Compose project and its profiles,
  and [`deploy/compose/README.md`](../deploy/compose/README.md): each file in
  `deploy/compose/` and when to use it. [Docker topology](docker-topology.md)
  explains the Compose setup for a reader new to Docker.
- [`scripts/README.md`](../scripts/README.md): the helper scripts.
- [`examples/README.md`](../examples/README.md): the job file that the quick
  start and the demo submit, and the demo notebook.
