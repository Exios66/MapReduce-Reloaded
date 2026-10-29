# Intent: the paper's benchmarks at 10 GB on the course VM

**Status: campaign tooling implemented; validation and benchmark evidence
must be recorded per revision. No 10 GB results are claimed here.**
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

The [campaign frontend](paper.sh) reuses the existing generators, host CLI,
MRKV implementation, samplers and rate plots; historical E0–E6 runs remain
in [their original runner](experiments.sh). Consult the ponytail reviewer
before implementation. Use the **host client for submission and polling**,
not the historical runner's container submission. Keep four one-CPU workers,
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

Tooling alone does not establish that a 10 GB configuration fits safely or
has passed validation. Archive the measured evidence before making that claim.

## Full-size resume checklist

**Hold:** other VM work was still active at the last handover check. The
commands below are prepared instructions, not executed results. Do not SSH,
clean, build, run tests or change services until the owner confirms exclusive
access. Keep the prepared native grep input and all rejected-trial evidence.

### 1. Establish and test the exact revision

- Confirm processes, other sessions, Docker activity and ownership of the
  shared `mapreduce` project. The campaign lock excludes campaign commands
  for this account, not other agents or automation.
- Use the existing separate campaign worktree. Inspect its changes before
  `git pull --ff-only`; never reset the other checkout or a running check.
- Review and commit any required VM formatting fixes. Run the targeted
  regressions, shell lint and full quality gate **on the VM**. Then build
  the pinned image/tools from that clean commit:

  ```bash
  export PATH="$HOME/.local/bin:$HOME/MapReduce-Reloaded/python/.venv/bin:$HOME/.local/go/bin:$PWD/.tools/bin:$PATH"
  python -m pytest -q python/tests/test_paper_campaign.py python/tests/test_bench_rates.py
  shellcheck bench/paper.sh
  make check PYTHON="$HOME/MapReduce-Reloaded/python/.venv/bin/python"
  make paper-tools
  ```

- Repeat the existing 100 MB native grep/sort and 1 GB zstd-KV normal,
  backups-off and calibrated-restart smokes. Reuse their prepared inputs;
  `prepare` must never overwrite them. Require valid output, pinned worker
  **and coordinator** images, complete telemetry, and stopped attempts.
- Archive the gate log and build stamp with their exact commit. These review
  fixes have not yet passed the VM gate; older `f56cadc` passes do not cover them.

### 2. Reuse the prepared 10 GB grep input

The existing `data/paper-input-grep-native-10000000000-160/` manifest records
100,000,000 records, 10,000,000,000 bytes and 923 matches. Do **not** run
`prepare` again for this dataset. Inventory actual shards and current free
space, then, only after the gates above:

```bash
for trial in 1 2 3; do
    SIZE=10000000000 M=160 bench/paper.sh run grep native normal || break
done
```

A failure stops the sequence. Preserve its data and diagnose it before any
new trial. Each accepted result must report M160/R1, exactly 100M accepted
input records, exactly 10GB accepted input bytes, and all 923 output records.
Archive all three accepted results and validation before selecting the
completed grep dataset for deletion. State that cleanup's exact scope first;
no broad data, volume or image pruning.

### 3. Admit sort only with a measured storage budget

Remeasure after approved, named cleanup. The earlier 1 GB zstd-KV calibration
stored 558,913,313 input bytes, 572,223,482 accepted intermediate bytes and
575,189,846 output bytes. Linear extrapolation alone gives about **17.1 GB
of simultaneous data at 10 GB logical input**, before spare attempts, spills,
logs and operational reserve. It is a floor, not an admission budget.

- Calibrate the new revision with M160 and include normal, backups-off and
  restart peaks. Account for in-flight/speculative copies and scratch, not
  only the accepted intermediate/output sizes.
- Record the resulting conservative budget and free-space measurement.
  `SPACE_BUDGET_BYTES` is required free space **after** input exists; never
  count input space twice or lower the gate just to pass.
- Prepare `sort zstdkv` only after the preparation gate fits, then check
  run admission again. If either fails, keep sort blocked and identify
  additional safe cleanup or engineering work. Native sort stays separately
  blocked unless its own larger budget genuinely fits.
- Use an untimed-for-comparison full-size normal calibration to select an
  injection delay during mapping. Freeze that `EVENT_AFTER` across restart
  trials; retain the calibration as a separately labeled result.

After admission, execute this balanced order with the same input, M160/R8,
image, interval and cache policy. Do not dispatch the whole table blindly;
validate and stop/join each trial before advancing.

| Round | First | Second | Third |
|---|---|---|---|
| 1 | normal | no-backups | restart |
| 2 | no-backups | restart | normal |
| 3 | restart | normal | no-backups |

Each trial uses:

```bash
# Replace these only with the documented calibration values.
SPACE_BUDGET_BYTES="$measured_budget" EVENT_AFTER="$calibrated_delay" \
    SIZE=10000000000 M=160 bench/paper.sh run sort zstdkv "$setting"
```

Stop on disk pressure, missed injection, provenance mismatch, missing samples
or failed validation. Do not substitute another storage format mid-matrix.

### 4. Publish evidence, not a success assumption

Retain private raw diagnostics. Publish sanitized manifests, build identity,
accepted counters, actual events, validations, sampled resource extrema,
rate curves and per-trial times. Remove private host paths and environment
details from public copies, not from the private originals. Report three
trials per setting with medians and ranges, no cold-cache claim and no
statistical-significance claim. Record any capacity-blocked configuration.
Reconcile hosted review threads against the tested revision; seek owner
approval rather than merging independently.
