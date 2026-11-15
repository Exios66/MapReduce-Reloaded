# Archived runs: out of date, not reported

Every directory here is older data, kept so no measurement is lost. None
of it is in a figure of [`bench/README.md`](../../README.md#evaluation-e0e7)
or [`README.md`](../../../README.md#results), and none of it is the current result.
The current results are the rerun of 11 November 2026, at commit `8f0ddb9`
(E0 at `3a907de`), in the directories one level up; their
[README](../README.md) lists them.

Each directory is named for the commit it ran at, or for what it was.

| Directory | Ran at | What it was | Replaced by |
|---|---|---|---|
| [`8f0ddb9-E0-first/`](8f0ddb9-E0-first/) | `8f0ddb9`, 11 Nov 2026 | The rerun's first E0, five runs, with every program's input in the page cache at once | [`../E0/`](../E0/), at `3a907de`; its [`NOTE.md`](8f0ddb9-E0-first/NOTE.md) says why |
| [`paper-2707160/`](paper-2707160/) | `2707160`, 28 Oct 2026 | One grep run over 10 GB and one sort over 1 GiB, with `bench/run.sh`: the rate samples behind the two rates plots in [the evaluation against the paper](../../../docs/evaluation.md) | Not rerun; E0 has the rerun's grep and sort times, without rate samples |
| [`before-8f0ddb9/`](before-8f0ddb9/) | Several commits, late Oct 2026 | E0–E6 as first published, three repeats each, with their harness logs | `../E0/` to `../E6/` and `../E2-pairs/` |
| [`4b5a235-E3/`](4b5a235-E3/) | `4b5a235`, Oct 2026 | The first full E3 | `before-8f0ddb9/E3/`, then `../E3/` |
| [`4b5a235-E2/`](4b5a235-E2/) | `4b5a235`, Oct 2026 | The first E2, with three settings | `before-8f0ddb9/E2/`, then `../E2/` |
| [`6e017d0-partial/`](6e017d0-partial/) | `6e017d0`, Oct 2026 | The first attempt at E0–E4, stopped by a full disk | All of the above |

## Notes

- `paper-2707160/`: `grep/` and `sort/` each hold `samples.csv`, the rates
  (`rates.csv`, `rates.png`), `result.json` with the counters and the
  machine, and `status.txt`. They are single runs that predate
  `machine.json`; the machine is recorded in `result.json`.
- `before-8f0ddb9/`: E0, E1 and E4 ran at `4b5a235`, E2 at `ca082a3`,
  E2-pairs at `162be31`, E3 at `9a3cd42`, E5 at `6b6c7be` and E6 at
  `b9bb3f4`, with three repeats each. The `dirty` flag in the `4b5a235` and
  `9a3cd42` `machine.json` files was set by an untracked backup directory,
  not a code change.
- `4b5a235-E3/`: its job times are valid and close to the rerun's, but its
  sampler started before the workers were restarted, so its rate samples
  start about 8 s late against the kill time. E3 was rerun at `9a3cd42`
  after the fix. Its harness log is the E3 part of
  [`before-8f0ddb9/experiments-E0-E4.log`](before-8f0ddb9/experiments-E0-E4.log).
- `4b5a235-E2/`: it had no run with backups off and no straggler, so it
  could not say what backups cost; E2 ran again at `ca082a3` with all four.
- `6e017d0-partial/`: it ran before workers were restarted between runs.
  Map output from earlier runs filled the VM's disk during E3, and the run
  stopped with `No space left on device` (see its `experiments.log`). E0
  and E2 had finished; E1 and E4 did not run.
- `8f0ddb9-E0-first/` keeps its archive, `E0.tar.gz`, its harness log,
  `E0.log`, and a `SHA256SUMS` for the archive.
