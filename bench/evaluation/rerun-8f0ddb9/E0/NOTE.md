# Superseded by `../E0-isolated/`

Kept so no measurement is lost; none of it is reported. This E0 ran with all
three programs' inputs in the page cache at once, about 3 GiB on a VM with
8 GiB of RAM, so sort often read its own 1 GiB input from disk: up to
1.97 GB machine-wide per run, where E2–E4's sort runs read almost none
(#347). That made 4-worker sort take 9.39 s here against 7.7–8.0 s in
E2–E4.

[`../E0-isolated/`](../E0-isolated/) reran E0 with `E0_ISOLATE=1`, which
drops the other programs' inputs from the page cache and reads the running
program's own before each run. It ran at commit `3a907de`, which is
`8f0ddb9` plus `bench/drop-cache.py` and that switch, so the system under
test is unchanged. `bench/plot-experiments.py` reads it in place of this
directory. Its harness log is [`../chain/E0-isolated.log`](../chain/E0-isolated.log)
and its archive `../archives/E0-isolated.tar.gz`.
