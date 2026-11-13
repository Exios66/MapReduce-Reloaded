# Evaluation data: the current results

This directory holds the current results of the course evaluation, E0 to
E7, and nothing older. Every figure here and in
[`bench/README.md`](../README.md#evaluation-e0e7) is drawn from it. They are
the rerun of 11 November 2026 on the course VM, with the system at commit
`8f0ddb9` from a clean tree; E0 was rerun the same day at `3a907de`, which
is `8f0ddb9` plus input isolation and changes nothing in the system (#347).
Every `machine.json` names its commit and `"dirty": false`.

| Experiment | Data | Runs per setting | Figure |
|---|---|---|---|
| E0: scaling over workers | [`E0/`](E0/) (`3a907de`) | 5 | [`E0.png`](E0.png) |
| E1: what the combiner saves | [`E1/`](E1/) | 5 | [`E1.png`](E1.png) |
| E2: backup tasks and a straggler | [`E2/`](E2/), [`E2-pairs/`](E2-pairs/) | 5; E2-pairs 10 pairs | [`E2.png`](E2.png), [`E2-comparison.png`](E2-comparison.png) |
| E3: losing a worker | [`E3/`](E3/) | 5, after 3 calibration runs | [`E3.png`](E3.png) |
| E4: a coordinator restart | [`E4/`](E4/) | 5, after 3 calibration runs | [`E4.png`](E4.png) |
| E2 to E4 together | `E2/`, `E3/`, `E4/` | | [`faults.png`](faults.png) |
| E5: an interpreted job file | [`E5/`](E5/) | 5 | [`E5.png`](E5.png) |
| E6: the combiner on real text | [`E6/`](E6/) | 5 | [`E6.png`](E6.png) |
| E7: backups against LATE | [`E7/`](E7/) | 5 | [`E7.png`](E7.png) |

Each figure has a `-dark` variant beside it.

- Each experiment's directory has `runs.csv` (one row per run, with
  `valid`), `cache.jsonl` (the page cache and disk reads at each run's start
  and end), `machine.json` and `QUALITY.txt`, a run summary written when the data was
  taken (rerun `.claude/skills/interpreting-bench-results/summarize.py` on
  the directory for the current one).
- [`raw/`](raw/) has every run's samples and logs as one archive per
  experiment, with `SHA256SUMS`. The figures are drawn from these;
  `make figures-check` redraws them and compares each with the committed
  PNG. E0's archive is named `E0-isolated.tar.gz`, as it was recorded.
- [`logs/`](logs/) has the harness logs: one per experiment,
  `chain.log` for the whole run, and `inputs.sha256` for the inputs.
- [`ab/`](ab/) is an A/B check, not an experiment: ten sorts each with the
  system at `4b5a235` and at `8f0ddb9`, on the same VM, which shows the code
  does not explain why sort is faster in this rerun than before it. Its
  `NOTE.md` gives the setup.
- [`archive/`](archive/) has every older run, labeled with its commit and
  date. None of it is current or drawn in a figure.
