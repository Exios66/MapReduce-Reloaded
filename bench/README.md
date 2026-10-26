# Benchmarks

Scripts that measure the cluster. Each one runs on the Compose cluster and
writes its raw results to `bench/results/`, which git ignores. The numbers
below are copied from a run, with the machine it ran on.

## Data generators (#57)

Each generator writes reproducible input to stdout from a size in bytes and
an optional seed (42 by default). Measured on the machine below, each
writes 64 MB in about a second, except `gen-zipf.py`, which takes about 6 s.

| Script | Records | For |
|---|---|---|
| [`gen-words.py`](gen-words.py) | Lines of 5 to 15 words from the test corpus | Word count, the codec benchmark |
| [`gen-grep.py`](gen-grep.py) | 100-byte records; the pattern `XYZ` is in 92,337 per 10^10 records, as in the paper's grep (§5.2) | `grep` with `-param pattern=XYZ` |
| [`gen-sort.py`](gen-sort.py) | TeraSort-style 100-byte records with random 10-byte keys, as in the paper's sort (§5.3) | `distsort` |
| [`gen-zipf.py`](gen-zipf.py) | Lines of words from a vocabulary of 100,000, with Zipf-distributed frequencies (exponent S, 1 by default) | Word count with a skewed key |
| [`gen-weblog.py`](gen-weblog.py) | Requests in the Common Log Format for 10,000 pages; the page is drawn with probability proportional to 1 / N | `urlfreq` |
| [`gen-links.py`](gen-links.py) | A source page, a tab and a target page, over 100,000 pages; targets are drawn with probability proportional to 1 / N | `reverselinks` |

- `gen-grep.py`: the rest of each record is lowercase letters and digits,
  so the pattern never occurs by chance. grep's output has exactly
  round(records × 92,337 / 10^10) lines, at least one.
- `gen-sort.py`: keys are random bytes, any but newline and carriage
  return, so they spread evenly over the first byte that `distsort`
  partitions on.
- `python/tests/test_bench_gen.py` checks the record sizes, the number of
  matches, the spread of keys and the skew.

## Data rates over a job (#57)

The paper's Figures 2 and 3 plot, against seconds since the job started,
the rate at which map tasks read input, the rate at which data is shuffled
from map to reduce tasks, and the rate at which reduce tasks write output
(§5.3). Two scripts make the same plots from the workers' `/metrics`:

- [`scrape-metrics.py`](scrape-metrics.py) `INTERVAL DURATION URL...`
  fetches each worker's `/metrics` every INTERVAL seconds and writes its byte
  counters to stdout as CSV.
- [`plot-rates.py`](plot-rates.py) `SAMPLES.csv [OUT.png]` sums the counters
  over the workers and differentiates them between samples. It writes the
  input, shuffle and output rates in MB/s as CSV, or, given `OUT.png`, draws
  them in three panels as the paper does. Drawing needs matplotlib.

Workers count input and output bytes as each task ends, so those two rates
rise in steps of whole tasks. The shuffle is counted as it is fetched.
`python/tests/test_bench_rates.py` tests both scripts against a fake
`/metrics`.

`make bench SCENARIO=grep|sort|zipf` runs one benchmark with them on the
Compose cluster, through [`run.sh`](run.sh):

| Scenario | Input | Program |
|---|---|---|
| `grep` | `gen-grep.py` | `grep` for `XYZ` (§5.2) |
| `sort` | `gen-sort.py` | `distsort` (§5.3) |
| `zipf` | `gen-zipf.py` | `wordcount` |

- Settings come from the environment: `WORKERS` (default 4), `SIZE` in
  bytes (default 256 MiB), `M` (16), `R` (4), `SEED` (42) and `INTERVAL`,
  the seconds between samples (0.5).
- The cluster is started or scaled to `WORKERS` workers. The host samples
  each worker's `/metrics` at its container's address, so this needs Docker
  on Linux, where the host can reach the Compose network.
- Each run writes `bench/results/SCENARIO-STAMP/`: `samples.csv`,
  `rates.csv`, `rates.png` (with matplotlib), `status.txt` (from
  `mr submit -wait`) and `result.json`.
- `result.json` records the settings, the job's counters and duration, the
  git commit (and whether the tree had changes), and the machine: CPUs, CPU
  model, memory, kernel and Docker version.
- Seconds in the samples count from just before the job is submitted.

## Codecs (#42)

`make bench-codecs` starts the cluster and runs [`codecs.sh`](codecs.sh):

- Input: 64 MiB of lines of 5 to 15 random words from the test corpus,
  written by [`gen-words.py`](gen-words.py) with a fixed seed.
  - The lines are text-like but don't repeat.
  - Copies of the corpus would make sorted map output far more compressible
    than real data: a first attempt with 100 copies shrank 11 times with
    zstd.
- Jobs: word count, which combines, and `distsort`, which shuffles every
  line.
  - Each job runs with M = 16 and R = 4, three times per intermediate codec.
- Bytes shuffled: every map output file is fetched once by its reduce task,
  so the `map.output.bytes` counter is the bytes shuffled.
- Time: the job's duration as the coordinator measured it, from accepting
  the job to its last reduce task (`seconds` in `mr status`).
  - The CSV also has `wall_seconds`, the time of `mr submit -wait`, which
    adds about a second to start its container.

Results on 2026-10-24: 4 vCPUs (Intel Xeon, 2.1 GHz), 16 GB of memory,
Docker 29.9.1, 4 workers. This was a development machine, not the course
VM, which has 4 vCPUs (Intel Xeon Silver 4516Y+) and 8 GB
([the VM](../docs/cluster-vm.md#the-vm)). Time is the median of three runs, with the range.

| Job | Codec | Bytes shuffled | vs none | Time (s) |
|---|---|---:|---:|---|
| wordcount | none | 2,071,855 | 1.00 | 2.81 (2.75–2.99) |
| wordcount | zstd | 1,225,752 | 0.59 | 2.93 (2.55–3.02) |
| wordcount | snappy | 1,930,867 | 0.93 | 3.00 (2.80–3.04) |
| distsort | none | 68,353,231 | 1.00 | 2.25 (1.98–2.37) |
| distsort | zstd | 31,892,233 | 0.47 | 2.40 (2.22–2.58) |
| distsort | snappy | 51,049,537 | 0.75 | 2.28 (2.23–2.34) |

What the numbers show:

- **Bytes:** compression measurably reduces the bytes shuffled.
  - zstd roughly halves them, and Snappy saves a quarter on `distsort`.
  - Word count's combiner already leaves one record per word and map task,
    so there is less left to compress.
- **Time:** job time does not change beyond run-to-run noise.
  - Every container shares one machine, so the shuffle crosses loopback,
    not a network, and saving bytes saves almost no time.
  - The paper's cluster shuffled over a real network (§5.1), where bytes
    cost time. The Phase 7 benchmarks (#57, #58) will measure that across
    machines.
