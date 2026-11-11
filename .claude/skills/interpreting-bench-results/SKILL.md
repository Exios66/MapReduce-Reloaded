---
name: interpreting-bench-results
description: Use when reading MapReduce-Reloaded experiment output (bench/results/E*, bench/evaluation/E*, runs.csv, cache.jsonl, machine.json), reporting E0-E7 numbers, deciding whether a difference between settings is real, or explaining a slow or invalid run.
---

# Interpreting bench results

A number is reportable only if its runs are valid, its commit is known and clean, and its ranges are compared honestly.

## Summarize first

```bash
python3 .claude/skills/interpreting-bench-results/summarize.py bench/results/E2 [more dirs...]
```

Per setting, program and worker count: valid runs, median and range of `seconds`, wall clock, backups, bytes shuffled and machine-wide disk reads; then runs to look at (invalid, or more than 2x from their median), whether each pair of settings overlaps, and E0's speed-ups.

## Rules

| Question | Rule |
|---|---|
| Which clock? | `seconds` is the coordinator's job time and is what the figures use; `wall_seconds` includes submission. Say which one you report. |
| Which runs count? | `valid=1` only: output matched the first run on that input, and a disturbed job had not finished before its event. |
| Which settings count? | Not `warmup` or `calibrate`: they prepare the measured runs. |
| Is a difference real? | Ranges that overlap are reported as "no clear difference", whatever the medians say (the rule recorded in docs/evaluation.md). |
| Is the run trustworthy? | `machine.json` must name one commit with `dirty: false` for every experiment compared. |
| Was the input cold? | `disk_read_sectors` counts the whole machine's reads. A large read during a slow run suggests a cold input; say "likely", never "shows". |
| Bytes shuffled differ between runs of one setting? | They are deterministic for an input: suspect a different input or a bug. |

## Common mistakes

- Reporting one slow run as "noise" without checking its disk reads and its position in the run order.
- Comparing medians from different commits or experiments as if one variable changed: use analyzing-bench-results and name every difference between the two runs.
- Quoting a ratio of medians without the ranges behind it.
