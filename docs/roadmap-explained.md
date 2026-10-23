# The Roadmap, Explained

A plain-language guide to how MapReduce-Reloaded is built, why it is built
in this order, and what fits in the 6-week schedule. The technical version is
[roadmap.md](roadmap.md); each piece of work is a GitHub issue (`#N`).

## The big picture

We build the system **in layers**. Every phase ends with something that runs
and has tests. Every layer is checked against the one before it.

**The oracle.** In Phase 1 we build a very simple version that runs on one
computer. From then on, every more complex version (distributed, fault
tolerant, cloud storage) must produce **exactly the same output files** as
the simple one. This lets us *prove* the complex versions are correct rather
than hope they are.

**The shared contract.** Go is the main implementation. Python implements the
same file format, hash and single-machine mode. Both languages must produce
identical bytes, which gives a second, independent correctness check.

## The phases

### Phase 0 — Foundations (#1–#5)
*Agree on the language and the paperwork before anyone builds anything.*

- **#2 Repository skeleton:** build commands and the `make check` quality gate.
- **#3 Message formats:** how the coordinator, workers and clients talk to
  each other. See [explainers/03-rpc-contract.md](explainers/03-rpc-contract.md).
- **#4 File format:** how map output is laid out on disk. Checksums make
  corruption detectable instead of silently using bad data. See
  [explainers/04-file-format.md](explainers/04-file-format.md).
- **#5 Partition hash:** the formula that decides which reducer each key
  goes to (for example, "apple" goes to reducer 3). Go and Python must give
  the same answer, or data ends up at the wrong reducer. See
  [explainers/05-partition-hash.md](explainers/05-partition-hash.md).

### Phase 1 — Single-computer version (#6–#12)
*Before running a restaurant chain, cook the dish perfectly in one kitchen.*

- **#7 The programming interface:** the user writes only a `map` function
  and a `reduce` function. Hiding networks and failures behind those two
  functions is the paper's whole point. See
  [explainers/07-programming-api.md](explainers/07-programming-api.md).
- **#8–#9 Reading and writing files:** plain text and our own format. See
  [explainers/06-kv-reader-writer.md](explainers/06-kv-reader-writer.md) and
  [explainers/08-formats.md](explainers/08-formats.md).
- **#10 Sorting data bigger than memory:** sort chunks that fit in memory,
  save them to disk, then merge the sorted chunks. See
  [explainers/09-external-sort.md](explainers/09-external-sort.md).
- **#11 The combiner:** add up counts on the map side before sending them.
  For example, send `("the", 5000)` once instead of `("the", 1)` five
  thousand times. See [explainers/10-combiner.md](explainers/10-combiner.md).
- **#12 `mr run --local`:** the whole pipeline in one process. **This is the
  oracle.** See [explainers/11-local-mode.md](explainers/11-local-mode.md).

### Phase 2 — Make it distributed (#13–#21)
*Open several kitchens, with a head chef handing out tickets.*

- **#14** Split the input into M pieces. See
  [explainers/12-splits.md](explainers/12-splits.md).
- **#15** The coordinator's notebook: every task's state, and where every
  output lives. See [explainers/13-task-table.md](explainers/13-task-table.md).
- **#16** The ask-for-work loop, using the messages from #3. See
  [explainers/14-cluster.md](explainers/14-cluster.md).
- **#17** Map tasks write R sorted output files.
- **#18** The shuffle server: reducers download their share of each map
  task's output.
- **#19** Reducers fetch, merge and reduce.
- **#20** Safe commits: write to a temporary file, then rename it in one
  step, so a half-written output file can never be seen.
- **#21** Docker: one command starts 1 coordinator and 4 workers on the
  CS544 VM (8 GB RAM; see [cluster-vm.md](cluster-vm.md)). A test checks
  that the output matches the oracle.

### Phase 3 — Survive failures (#22–#29)
*The paper's famous result: thousands of cheap machines, some always
failing, and jobs still finish correctly.*

- **#23** Detect dead workers and re-run their work. This includes map tasks
  that had already *finished*, because their output was on the dead
  worker's disk.
- **#24** Coordinator crash recovery: a log of every decision, replayed on
  restart. The 2004 system simply aborted the job in this case.
- **#25** Backup tasks: near the end of a job, start duplicates of the slow
  tasks. In the paper, turning this off made jobs 44% slower.
- **#26** Bad-record skipping: if user code crashes on the same record
  twice, skip that record.
- **#27** Counters that don't double-count re-run tasks.
- **#28** Safe extra output files.
- **#29 Chaos testing:** randomly kill and freeze machines during a job, 50
  runs in a row, and check the output still matches the oracle. This is the
  strongest demo.

### Phase 4 — Finish the paper's features (#30–#34)

- **#31** Locality: run each map task on the machine that already holds its
  data.
- **#32** A live status web page and metrics, as in the paper (§4.8).
- **#33** The paper's example programs: grep, word count, inverted index,
  sort, and others.
- **#34** The paper-to-code table filled in: every paper feature linked to
  its code and its test. This makes the "faithful recreation" claim
  checkable.

### Phase 5 — Python catches up (#35–#39) · *stretch*

- Full Python coordinator and worker.
- Python map and reduce functions running on Go workers.
- Every Go/Python combination tested against the oracle.

### Phase 6 — Modernise (#40–#55) · *partly in scope*

*How you would build it in 2026 instead of 2004.*

- **In scope:**
  - **#41** cloud object storage (S3, run locally with MinIO);
  - **#42** compression.
- **Stretch:**
  - durable shuffle storage;
  - a standby coordinator that takes over automatically;
  - smarter slow-task detection;
  - skew handling;
  - memory limits;
  - tracing;
  - encryption;
  - Kubernetes autoscaling;
  - Parquet;
  - a job API;
  - pipelines;
  - incremental runs;
  - WebAssembly.

### Phase 7 — Prove it with numbers (#56–#60)

- **#57** A benchmark tool.
- **#58** Re-run the paper's §5 experiments at small scale and compare the
  *shapes* of the graphs.
- **#60** The final report and a 5-minute demo.
- **#59**, which measures every modern extension, is a stretch goal.

## The 6-week schedule

The full roadmap is about 20 weeks of work. The available time is about 6,
so the scope is **all 23 paper features in Go, the Python half of the shared
contract and single-machine mode, two modern extensions, and benchmarks with
a report**. Out-of-scope issues stay open with a `[stretch]` prefix and
appear in the report as future work.

| Week | Issues | Milestone at the end of the week |
|---|---|---|
| 1 | #4 file format, #5 hash, #7 API, #8 reader/writer | Contract complete in both languages |
| 2 | #9 formats, #10 external sort, #11 combiner, #12 local mode | `mr run --local wordcount` works (the oracle) |
| 3 | #14 splitting, #15 task table, #16 scheduling, #17 map spill, #18 shuffle | Map side runs distributed |
| 4 | #19 reduce, #20 commit, #21 Docker cluster, #23 worker failure | Full distributed job on Compose, on the CS544 VM, matches the oracle |
| 5 | #24 recovery, #25 backup tasks, #26 bad records, #27 counters, #28 side files, #29 chaos | Chaos runs pass |
| 6 | #31 locality, #32 status, #33 examples, #34 mapping, #41 S3, #42 compression, #57/#58 benchmarks, #60 report | All paper features done, report written |

**If we fall behind, cut in this order:**
1. #42 compression
2. #41 S3 storage
3. trim #33 to word count, grep, inverted index and sort
4. simplify #31 locality to host-only

The paper features themselves (F1–F23) are never cut.

**Python's share:** the Python halves of #5 and #8–#12. That covers the
file format, hash, formats, sort, combiner and local mode, and keeps the
byte-for-byte cross-language checks working.
