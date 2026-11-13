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

Per setting, program and worker count: valid runs, median and range of the experiment's reported clock (`seconds`, or `wall_seconds` for E3 and E4, named on its `clock:` line), wall clock, median backups launched and total backups won, bytes shuffled and machine-wide disk reads; then runs to look at (invalid, more than 2x from their median, or a group whose bytes shuffled differ between runs), settings with fewer valid runs than the others, whether each pair of settings overlaps, and E0's speed-ups.

## Rules

| Question | Rule |
|---|---|
| Which clock? | `seconds` is the coordinator's job time, which E0, E1, E2, E5, E6 and E7 report; E3 and E4 report `wall_seconds`, from when `mr submit` returned, because a restarted coordinator times its jobs from the replay. Say which one you report, and never mix the two in one comparison. |
| Which runs count? | `valid=1` only: output matched the reference for that input, the checksum of the first run on it that succeeded without missing its event (normally the warm-up or calibration run), and a disturbed job had not finished before its event. The harness deletes each output after checksumming it, so the `valid` column is the record; no checksum is kept. |
| Which settings count? | Not `warmup` or `calibrate`: they prepare the measured runs. |
| Is a difference real? | Ranges that overlap are reported as "no clear difference", whatever the medians say (the rule recorded in docs/evaluation.md). |
| Is the run trustworthy? | `machine.json` must name one commit with `dirty: false` for every experiment compared. In `bench/evaluation/` that is `8f0ddb9`, except `E0/` at `3a907de` (8f0ddb9 plus input isolation); the replaced first E0 is in `archive/8f0ddb9-E0-first/`, kept, not reported. Nothing under `bench/evaluation/archive/` is current. |
| Was the order balanced? | Settings take turns within each repeat, reversed on even repeats; E5's three rotate instead, and E1 and E6 alternate on and off within each input. Only comparisons within those turns are drift-controlled. |
| Was the input cold? | `disk_read_sectors` counts the whole machine's reads. A large read during a slow run suggests a cold input; say "likely", never "shows". |
| Bytes shuffled differ between runs of one setting? | They are deterministic for an input: suspect a different input or a bug. |

## Common mistakes

- Reporting one slow run as "noise" without checking its disk reads and its position in the run order.
- Comparing medians from different commits or experiments as if one variable changed: use analyzing-bench-results and name every difference between the two runs.
- Quoting a ratio of medians without the ranges behind it.
- Checking a write-up's numbers against the summary but not its words: a claim about which clock, which commit, or which runs read from disk is checked against the data too.
- Trusting a figure without redrawing it: `make figures-check` (analyzing-bench-results) redraws them from the archives.
