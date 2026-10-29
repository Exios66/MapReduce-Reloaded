# Superseded runs

Kept so no measurement is lost; none of it is reported. The results in
[`../`](../) and [`bench/README.md`](../../README.md#evaluation-e0e6) replace
them.

- [`4b5a235-E3/`](4b5a235-E3/): the first full E3 run, at commit
  `4b5a235`. Its job times are valid and close to the rerun's, but its
  sampler started before the workers were restarted, so its rate samples
  start about 8 s late against the kill time. E3 was rerun at `9a3cd42`
  after the fix. Its harness log is the E3 part of
  [`../experiments-E0-E4.log`](../experiments-E0-E4.log).
- [`4b5a235-E2/`](4b5a235-E2/): the first E2, at commit `4b5a235`, with
  three settings. It had no run with backups off and no straggler, so it
  could not say what backups cost; E2 ran again at `ca082a3` with all four.
- [`6e017d0-partial/`](6e017d0-partial/): the first attempt at E0–E4, at
  commit `6e017d0`, before workers were restarted between runs. Map output
  from earlier runs filled the VM's disk during E3, and the run stopped with
  `No space left on device` (see its `experiments.log`). E0 and E2 had
  finished; E1 and E4 did not run.
