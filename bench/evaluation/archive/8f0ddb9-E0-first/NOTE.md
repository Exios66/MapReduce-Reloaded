# Replaced by `../../E0/`

Kept so no measurement is lost; none of it is reported. This E0 ran with all
three programs' inputs in the page cache at once, about 3 GiB on a VM with
8 GiB of RAM, so sort often read its own 1 GiB input from disk: up to
1.97 GB machine-wide per run, where E2–E4's sort runs read almost none
(#347). That made 4-worker sort take 9.39 s here against 7.8–8.0 s in
E2–E4.

[`../../E0/`](../../E0/) reran E0 with `E0_ISOLATE=1`, which
drops the other programs' inputs from the page cache and reads the running
program's own before each run. It ran at commit `3a907de`, which is
`8f0ddb9` plus `bench/drop-cache.py` and that switch, so the system under
test is unchanged. Both are in this repository, and isolation is on by
default, so `REPEAT=5 bench/experiments.sh E0` reproduces it.
The figures are drawn from it, not from this directory. Its harness log is [`../../logs/E0-isolated.log`](../../logs/E0-isolated.log)
and its archive `../../raw/E0-isolated.tar.gz`. This directory's own harness
log is `E0.log` and its archive `E0.tar.gz`.
