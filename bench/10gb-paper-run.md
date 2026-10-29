# Intent: the paper's benchmarks at 10 GB on the course VM

**Status: proposed; no implementation or new benchmark results in this PR.**
The authorized execution scope includes VM cleanup, cluster configuration
changes and benchmark jobs, subject to the handover and safety gates below.
Wait for the parallel VM work to finish and its owner to hand over the
cluster before cleanup, reconfiguration or execution. Preserve that work's
results. This proposal does not authorize deleting unrelated files, stopping
other workloads or changing the VM's disk size.

## Paper first, our options as variants

Run the experiments in [Dean and Ghemawat's 2004 paper, section 5](https://www.usenix.org/legacy/events/osdi04/tech/full_papers/dean/dean_html/)
at **100,000,000 records of 100 bytes: 10,000,000,000 logical bytes**, not
10 GiB. Reuse the deterministic generators and existing benchmark plumbing.

| Experiment | Primary comparison |
|---|---|
| Section 5.2: grep | Rare three-character pattern; one reducer; 923 matching records at this size |
| Section 5.3: sort | Sort by the first 10 bytes of each record; retain every record; multiple reduce waves |
| Section 5.4: backups | Repeat the same sort with backups disabled; otherwise unchanged |
| Section 5.5: failures | Kill a worker during sort and immediately start its replacement, as in the paper |

Proposed task counts are M = 160 (62.5 MB per map), R = 1 for grep and
R = 8 for sort on four one-CPU workers. These are scaled choices, not the
paper's original counts. Keep them identical across paired sort runs.
Record actual failure and replacement times, not just scheduled delays.
Losing one of four workers is 25%, versus the paper's 200 of 1,746 (11.45%).

Our current `distsort` uses the whole line as the key, rather than the
paper's 10-byte key plus original record. Plan a benchmark program matching
that representation without silently changing the existing example. Keep
whole-line sort, injected stragglers, permanent worker loss and alternative
storage configurations explicitly labeled as variants. An injected slow
worker is not a replacement for the paper's unthrottled backups comparison.

## Fit the existing VM, without hiding the cost

The VM has roughly a 35 GB root filesystem. An initial read-only inspection
found only about 5 GB free while other work was in progress; remeasure after
handover. Native sort needs over 30 GB of live job data before headroom:
10 GB input, intermediate files of similar size, and roughly 10.1 GB of
current text-format output, plus spills, retries and speculative attempts.
The text writer adds a tab before each record's newline. The primary
10-byte-key plus 99-byte-record representation instead writes **111 bytes
per text output record: 11.1 GB**, with 10.9 GB of emitted key/value payload
before framing. Budget its larger intermediate files separately too.
Cleanup alone is not proof that either uncompressed sort will fit.

Investigate the smallest options in this order:

1. **Existing lifecycle, less waste.** After approved cleanup, generate
   directly into the existing shared `/data` mount under a temporary name,
   validate its size, then rename. Keep one dataset at a time. Avoid the
   experiment runner's host temporary copy followed by a volume copy.
   Validate and archive diagnostics and summaries before removing completed
   output; do not retain another full output copy on the VM. Stop losing
   speculative attempts and reclaim old scratch before generating the next
   dataset or admitting another trial.
   Retain input while worker failure could require map re-execution.
2. **Existing compression, named variants.** Measure the actual generator's
   intermediate compression ratio before committing to 10 GB. Try the
   existing zstd intermediate codec first. Compressed output additionally
   requires the existing KV output format; text output cannot take a codec.
   If input also needs compression, use existing zstd KV input in 160
   nonempty shards: KV files cannot be split, so one shard is one map task.
   Stream one uninterrupted generator into the shards without materializing
   a text copy or restarting its random stream per shard. Preserve each
   original line as the value and its logical byte offset as the input key;
   verify decoded equivalence and exactly 625,000 records per shard.
   Run the comparison matrix within each storage configuration; do not
   compare native normal sort against compressed backups-off sort.
3. **Framework extension only if needed.** A deterministic, seekable
   generated-input format could remove the stored input while retaining the
   full logical workload and reproducible retries. This follows the custom
   reader idea in section 4.4, but changes input planning and physical I/O:
   report it as a generated-input variant, not 10 GB read from disk.

Defer incremental partition reclamation unless measurements require it.
Deleting intermediate data after a fetch is unsafe: reduce retries,
speculation and recovery may still need it. A reclamation extension would
need durable reduce-commit coordination and failure tests, not just cleanup.

Gate generation and submission on measured peak-space budgets, including
concurrent tasks and operational reserve. The existing 5 GiB pre-run check
and 2 GiB worker threshold are not sufficient capacity guarantees at this
size. Calibrate with smaller inputs first. On timeout or unsafe disk pressure,
stop the matrix and stop the benchmark-owned coordinator and workers to
quiesce attempts: status `TIMEOUT` alone does not cancel anything. Preserve
diagnostics and WAL; coordinator restart replays an active job. Do not delete
its data or restart against removed inputs until the abandoned job cannot
resume. No next trial or input rotation until all attempts have stopped and
old scratch is reclaimed, even after a successful status. A native
configuration that cannot safely fit remains blocked, not silently replaced
or reported as completed.

## VM handover and execution

1. Obtain the other agent's handover, preserve its results and identify any
   workloads that must remain running. Check processes as well as Compose:
   tests may run a separate coordinator and workers outside containers.
2. Inventory disk use again. State the exact cleanup targets and blast
   radius before removal. Prefer reproducible build caches and selected
   completed benchmark artifacts; do not prune volumes, WAL, source trees,
   unrelated coursework or active-job data indiscriminately. Recheck free
   space after cleanup rather than adding up potentially shared cache sizes.
3. Build a reviewed revision, recreate only this project's core services,
   and verify four one-CPU workers, memory limits and consistent image IDs.
   Replace the two-worker/256-MB soak configuration; do not carry it into
   the primary runs. Record unrelated load and keep it out of timed trials.
4. Run correctness smoke tests and smaller storage calibration. Admit each
   10 GB configuration only if its measured budget passes; validate every
   result, preserve evidence, then reclaim its completed data safely.

## Evidence and implementation boundaries

Extend [the existing experiment runner](experiments.sh), samplers, plots and
their Python tests rather than introducing another benchmark framework.
Consult the ponytail reviewer before implementation. Use the runner's
already-built **host client for submission and polling**, replacing its
current container submission. Keep the assignment's four one-CPU workers,
shared storage and correctness requirements intact. Coordinate the tested
source revision with the
[parallel soak work](https://github.com/Exios66/MapReduce-Reloaded/pull/251),
then record the actual running images and resource limits.

- Collect at least three valid measured runs per setting, interleave paired
  comparisons and balance their order. Report raw trials, medians and ranges;
  do not infer statistical significance from three trials.
- Reuse byte-rate and task samplers. Record event outcomes, cache policy,
  machine contention, logical bytes, physical stored bytes and peak disk and
  memory usage. Input counters count split bytes (compressed file sizes for
  KV); output counters count emitted key/value payload before framing and
  compression. Both update at attempt completion; shuffle counts fetched
  bytes. Label these distinct quantities and measure stored sizes separately.
  Neither cached input nor generated bytes establish physical disk throughput.
- Independently validate grep's matching-record multiset; validate sort's
  record count, framing, global key order and preservation of input records.
  A checksum against the first run alone does not establish correctness.
  Label any probabilistic multiset fingerprint as such.
- Test storage gates, input lifecycle, timeout safety, failed/missed events,
  restart sampling and malformed output before an authorized small cluster
  smoke run, then the 10 GB matrix. Preserve failures and diagnostics too.
- Publish new results separately from the existing 1 GiB evaluation. Compare
  shapes and relative costs with the paper, explicitly noting shared VM
  hardware, no GFS output replication, different scale and any format changes.
  Our reducers wait for every map to finish; maps sort partitions and reducers
  stream-merge them. The paper overlaps map/shuffle and has a reduce-side
  sorting gap, so those curve shapes are not equivalent. Existing rate plots
  accumulate activity and handle resets; they do not reproduce the paper's
  negative input rate for lost map work.

This intent PR changes documentation only. It neither reserves the VM nor
claims that a 10 GB configuration has passed validation or fits safely yet.
