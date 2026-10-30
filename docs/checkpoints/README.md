# Progress checkpoint — 2026-10-30

This branch preserves progress without overwriting the active showcase or
timeline branches. It is not a request to merge unfinished dashboard work.

## Runnable README changes

The branch contains the README improvements, terminal setup GIFs, real
CS544 dashboard recordings, command checks, and additive CI mappings from
`cf3896e`. Their limitations are in
[README command verification](../readme-validation.md).

## Unapplied work

- `timeline-fixes.patch`: `eee3840..7da6696`, including the refresh/focus,
  timestamp-bound, backup-label, accessibility and regression-test fixes.
  The shared timeline branch advanced independently; compare its current
  changes before applying this patch.
- `event-tail-wip.patch`: `1ad1f37..fc0bfd7`. **Incomplete and not ready to
  apply or merge.** The Go/Python wiring, API parity and tests need work.
  Hold it until its scope is reconciled with `feat/showcase-plan`.
- `capture-script-wip.patch`: the earlier uncommitted local delta against
  the tooling branch at `ca0fb43`. **Not verified or ready to apply.** It
  preserves experimental capture fixes, not a working end-to-end recorder.

These patches are data, not installed code. No test was removed, disabled
or weakened to checkpoint the unfinished work. The feature branches and
the deployed CS544 checkout are not changed by storing these snapshots.
