---
name: analyzing-bench-results
description: Use when comparing a new MapReduce-Reloaded benchmark run with published results or another commit, regenerating bench figures or tables, or explaining why a measured time changed between runs.
---

# Analyzing bench results

Compare like with like, then draw. A change between two runs is explained only when every difference between them is listed and the rest are ruled out.

## Compare two runs

```bash
python3 .claude/skills/analyzing-bench-results/compare.py bench/evaluation/E1 bench/results/E1
```

For each setting, program and worker count: old and new median, range and valid count, the change in the median, whether the ranges overlap, and whether bytes shuffled changed. It prints both commits and flags a dirty tree.

## Before attributing a change

List what differs between the two runs and check each:

- **Commit**: `git log --oneline OLD..NEW -- go/ deploy/` for code on the job's path; a dirty tree means unknown code.
- **Order and cache state**: block order against interleaved, what ran just before, disk reads in `cache.jsonl` (machine-wide).
- **Machine**: `machine.json` (CPUs, memory, kernel, Docker), other workloads on the host.
- **Protocol**: repeats, warm-up, `REPEAT`, `SIZE`, `M`, `R`.

The same setting in two experiments at one commit (E0's `w4` distsort against E2's `normal`) separates protocol from code. Only a controlled A/B, the two commits taking turns on one host, attributes a change to code.

## Regenerate figures

Plotting needs matplotlib, which `make venv` does not install. Render on the experiment VM when the local machine is busy, and never while a chain is running there.

```bash
python3 -m venv /tmp/plotvenv && /tmp/plotvenv/bin/pip install -q matplotlib
/tmp/plotvenv/bin/python bench/plot-experiments.py RESULTS_DIR OUT_DIR
```

`RESULTS_DIR` holds `E0/`, `E1/`, ... each with `runs.csv`; it writes `E*.png` and `E*-dark.png` to `OUT_DIR`.

Titles are computed from the CSVs; check each against interpreting-bench-results' summary before publishing.

## Common mistakes

- Attributing a speed-up to code because the commits differ, without a controlled run.
- Comparing an E0 time with an E2 time for the same setting without noting that the cache state differs.
- Writing "faster" when the ranges overlap.
