# Decimal-10-GB campaign evidence

Raw results of the expanded campaign at approximately ten times the previous
1 GiB evaluation. The primary E0–E5 controls keep their original generated
formats, seeds, skew and paired settings. Real datasets are separate
extensions and do not replace these runs. Every complete matrix here comes
from one frozen source revision; the revision and immutable image are in each
run's `build.json`.

Every accepted trial was independently validated on the stopped cluster.
Grep and sort use input-derived count and multiset fingerprints (explicitly
probabilistic) plus their framing/order checks; word count uses
input-derived expected output-part hashes. A checksum against an earlier
distributed run alone is not accepted as correctness.

Cache state is uncontrolled: the host page cache was not dropped, so these
are neither cold-cache nor warm-cache claims. Docker resource extrema are
sampled, not guaranteed instantaneous peaks. `disk.csv` gives host-wide
root-device sectors, not per-container physical I/O.

## E0: grep scaling

Source `70fcbd424672ec4405e6c0325ddf326ff509ef6a`, image
`sha256:41d5bacf3835fce4479ed95fa06048b65defedd103129b435439e5a4b28b9bad`.
Each job scans exactly 100,000,000 records and 10,000,000,000 bytes for `XYZ`,
which 923 records contain. M = 160, R = 1, three trials per worker count.
Worker counts rotate by round (4/2/1, 2/1/4, 1/4/2), so a drift in the VM does
not fall entirely on one setting.

| Workers | Job seconds, trials | Median | Wall seconds, median |
|---:|---|---:|---:|
| 1 | 51.625, 50.366, 48.742 | 50.366 | 50.703 |
| 2 | 23.260, 26.247, 23.537 | 23.537 | 23.780 |
| 4 | 19.942, 15.390, 16.016 | 16.016 | 16.528 |

![Grep at 10 GB on four workers: input, shuffle and output rates](E0-grep/w4-r1/rates.png)

Scaling from one to four workers is **3.15×** by median job time (2.14× from
one to two, then 1.47× from two to four). Four workers share one disk, so this
is not CPU-only scaling. The shape is the paper's
Figure 2: input rises as maps start, holds, then falls; shuffle and output are
tiny. The historical same-size run in `docs/evaluation.md` was 18.3 s on four
workers; this current-runtime median is 16.0 s, but cache state and revisions
differ, so the 12.5% difference is not a controlled speedup claim. This is a
rerun, not the first 10 GB grep.

Each `wN-rM/` directory holds the sanitized `status.txt`, `events.csv`,
`clock.json`, `submission-clock.json`, `samples.csv`, `tasks.csv`,
`rates.csv`, `resources.jsonl`, `disk.csv`, host snapshots, provenance and
validation. Worker URLs and IPs are replaced with stable labels; sample times
are relative to submission, so baseline samples are negative. `trials.json`,
`trials.csv` and `audit.json` summarize all nine, and `SHA256SUMS` names every
published file. Private raw copies, including absolute host paths, remain
outside the repository.

## Sort representation and admission

The paper's sort uses a 10-byte key plus the original 100-byte record.
`papersort` implements that representation. The historical `distsort` uses
the whole line as its key, so its E0/E2/E3/E4 trials are a separately labeled
control, never the paper-format result.

The extra reduce wave and shared disk make native uncompressed sort
infeasible on this VM. The admitted sort runs therefore use the named
`zstdkv` variant: **compressed input, intermediate and output**. The input
holds the same 100,000,000 records and 10,000,000,000 logical bytes; its
physical size is 5,612,159,103 bytes. This is a storage variant, not a
10 GB-native result.

The final, 1,024 MiB calibration ran before the matrix and was independently
verified:

| Quantity | Value |
|---|---:|
| Input records / logical bytes | 100,000,000 / 10,000,000,000 |
| Map / reduce tasks | 160 / 8 |
| Worker memory / CPUs | 1,024 MiB / 1 CPU each |
| Job seconds | 194.604 |
| Wall seconds | 194.797 |
| Output records | 100,000,000 |
| Physical output bytes | 5,424,640,027 |
| Observed free-space growth | 11,249,983,488 bytes |
| Sampled free-space floor | 8,289,402,880 bytes |

[`sort-calibration.json`](sort-calibration.json) records these final
calibration fields without private host paths. It is not counted as one of the
three matrix repeats; its wall time sets the fault-injection baseline at 25%,
50% and 75%. The native variant's larger simultaneous
input/intermediate/output requirement does not fit the same free space and is
reported as capacity-blocked, without shrinking the workload.

The earlier attempt used the historical 768 MiB worker cap. Three E0 trials
and a pilot calibration passed, but the next trial lost a worker to a
container OOM at 765 MiB (Docker `OOMKilled=true`, exit 137). Its completed
output still revalidated against the input manifest, showing the job logic was
correct and the memory cap was not. That trial is retained as **invalid
telemetry**, and the earlier 768 MiB runs are a labeled pilot, not part of the
final matrix ([pilot fields](sort-calibration-768pilot.json)). The final
cohort uses **1,024 MiB per worker** (1,000,000,000 NanoCPUs, four workers),
verified by container inspection before every run. The grep cohort ran under
768 MiB but used about 10 MiB per worker, so its cap did not constrain that
workload; its nine trials remain a consistent cohort.

### Provisional sort-matrix rows

The matrix has 33 of 39 rows landed so far. Walls per trial, in run order;
medians are provisional until all 39 rows and their independent audit land.
Wording follows the audit state in
[`docs/evaluation.md`](../../docs/evaluation.md#decimal-10-10-campaign);
this is a live table, not the final cohort result.

| Trial | Walls r1, r2, r3 | Provisional median |
|---|---|---:|
| E0 sort, 1 worker | 637.329, 635.715, 697.900 | 637.329 |
| E0 sort, 2 workers | 379.293, 331.893, 336.173 | 336.173 |
| E0 sort, 4 workers | 189.151, 191.454, 195.320 | 191.454 |
| E2 normal | 193.211, 187.807, 202.499 | 193.211 |
| E2 no-backups | 192.858, 176.182, 187.657 | 187.657 |
| E2 straggler (+backups) | 275.796, 247.221, 289.586 | 275.796 |
| E2 straggler-no-backups | 300.841, 302.129, 303.864 | 302.129 |
| E3 normal | 208.768, 198.884 | 203.826 |
| E3 kill 25% | 237.757, 236.837 | 237.297 |
| E3 kill 50% | 240.134, 238.461 | 239.298 |
| E3 kill 75% | 268.427, 268.682 | 268.555 |
| E4 normal | 198.494, 193.454 | 195.974 |
| E4 coordinator-restart | 186.082, 197.613 | 191.847 |

The remaining six rows (trial-3 E3 and E4 blocks) are running. One E2
straggler r2 attempt (`kill-75` run 2 as labelled in the ledger) was rejected
by the telemetry gate after an unscheduled second-worker container OOM; its
diagnostics are retained, its output was registered as a rejection (no
storage variant or workload change was made), and a retry under the same
1,024 MiB configuration measured 268.682 s, within 0.1% of the first trial.

Tests, lint, builds and `make check` ran on the course VM. No test was run on
the Mac.

## Design alignment with the 2004 MapReduce experiments

The campaign intentionally mirrors the paper's §5–6 experiment families on
hardware that is smaller by about three orders of magnitude, keeping task
shapes identical so the shapes of the results — not the absolute times — are
the comparison. The 2004 numbers below are the paper's own reported figures.

| Dimension | 2004 paper | This campaign | Preserved? |
|---|---|---|---|
| Input format | 1.0e10 x 100-byte records (1 TB) | 1.0e8 x 100-byte records (10 GB logical) | record shape identical |
| Sort key | 10-byte key prefix; reduce hash-partitions output | same key shape in `papersort` | identical |
| Grep key | ~114,619 matches per 1 TB (record prefix density) | 923 exact matches per 10 GB | pattern semantics equal; density differs and grep is I/O-bound, so only the qualitative shape is compared |
| Workers | ~1,764 machines, co-located GFS | 4 one-CPU workers over one shared disk | mismatch (mitigated by the shared-disk caveat in `REPORT-DRAFT.md`) |
| Map tasks M | 150,000 | 160 | ratio M/workers kept in the same order of magnitude (38–40 per worker) |
| Reduce tasks R | 4,000 | 8 (two waves) | whole-wave shape preserved as in the 1 GiB baseline |
| Backup tasks | speculative execution on idle machines | same semantics (`E2 straggler` with `backups`) | identical |
| Straggler | one override: system slowdown from co-tenants | one worker throttled to 0.20 CPU (`E2`) | equivalent shape, deterministic |
| Worker loss | 200 of 1,764 workers killed (11.4%) mid-sort | 1 of 4 workers killed at 25 / 50 / 75% of baseline (`E3`) | kill permanently loses map work; percentage differs, so the paper ratio is reported as design commentary, not a matched control |
| Master failure | design discussion; no timed experiment | coordinator SIGKILL + fresh-WAL restart at 50% (`E4`) | Go-port-specific addition to §3, labeled not-as-paper |
| Combining / skew | word count with synthetic skew | `E1` combiner on/off under Zipf 0.5/1.0/1.5 | same skewed-word-count family in the same Zipf discipline; exact paper parameters not replicated 1:1 |
| Implementation | C++ reference implementation | Go port; same protocol fates | fairness handled by the history in `docs/evaluation.md` |

Deviations are stated rather than hidden: scale is 1/100 of the paper (task
counts scale with it), one shared disk instead of co-located GFS, and three
repeats per setting rather than the paper's larger fleets. Observability is a
limit of any small-fleet experiment: with four workers, losing one is 25% of
scheduling capacity (the paper lost 11%), so worker-loss effects are expected
to overstate relative to the paper — reported alongside the caveat rather
than tuned away. Times are medians over three repeats with ranges. No
cold/warm page-cache state is claimed, and each accepted run keeps the
provenance (`image`, build commit, memory limits) printed in its
`build.json` and `workers.json`.

[REPORT-DRAFT.md](REPORT-DRAFT.md) in the private evidence directory contains
the fuller pruning plan: the paper comparison limitations, the capacity
blockers, and the real-corpus extensions that remain separate from the
primary campaign.
