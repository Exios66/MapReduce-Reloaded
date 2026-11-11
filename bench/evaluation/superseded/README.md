# Superseded runs

Kept so no measurement is lost; none of it is reported. The results in
[`../rerun-8f0ddb9/`](../rerun-8f0ddb9/) and
[`bench/README.md`](../../README.md#evaluation-e0e7) replace them.

- [`before-8f0ddb9/`](before-8f0ddb9/): the E0–E6 results published before
  the rerun at `8f0ddb9` ([`../rerun-8f0ddb9/`](../rerun-8f0ddb9/)), with
  their harness logs. They ran at several commits: E0, E1 and E4 at
  `4b5a235`, E2 at `ca082a3`, E2-pairs at `162be31`, E3 at `9a3cd42`, E5 at
  `6b6c7be` and E6 at `b9bb3f4`, with three repeats each. The `dirty` flag
  in the `4b5a235` and `9a3cd42` `machine.json` files was set by an untracked
  backup directory, not a code change.
- [`../rerun-8f0ddb9/E0/`](../rerun-8f0ddb9/E0/): the rerun's first E0,
  replaced by `E0-isolated/` beside it; its `NOTE.md` says why.
- [`4b5a235-E3/`](4b5a235-E3/): the first full E3 run, at commit
  `4b5a235`. Its job times are valid and close to the rerun's, but its
  sampler started before the workers were restarted, so its rate samples
  start about 8 s late against the kill time. E3 was rerun at `9a3cd42`
  after the fix. Its harness log is the E3 part of
  [`before-8f0ddb9/experiments-E0-E4.log`](before-8f0ddb9/experiments-E0-E4.log).
- [`4b5a235-E2/`](4b5a235-E2/): the first E2, at commit `4b5a235`, with
  three settings. It had no run with backups off and no straggler, so it
  could not say what backups cost; E2 ran again at `ca082a3` with all four.
- [`6e017d0-partial/`](6e017d0-partial/): the first attempt at E0–E4, at
  commit `6e017d0`, before workers were restarted between runs. Map output
  from earlier runs filled the VM's disk during E3, and the run stopped with
  `No space left on device` (see its `experiments.log`). E0 and E2 had
  finished; E1 and E4 did not run.
