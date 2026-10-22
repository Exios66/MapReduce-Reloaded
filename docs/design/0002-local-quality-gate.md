# ADR 0002 — Local quality gate instead of hosted CI

**Status:** accepted · **Phase:** 0

## Context

GitHub Actions runs on this account kept failing for account-level reasons
unrelated to the code, and every failure sent an email. Failures that do not
reflect the code are noise, and they hide real problems.

We still need the guarantee that CI provides: nothing reaches GitHub unless
every linter and test passes.

## Decision

- `make check` is the single quality gate. It runs, for both languages:
  - formatting checks;
  - `go vet`;
  - `golangci-lint`;
  - `ruff`;
  - all tests, with the Go race detector on.
- A versioned git hook, `.githooks/pre-push`, runs `make check` before every
  push and aborts the push if anything fails. Each clone enables it once with
  `make hooks`, which sets `core.hooksPath`.
- The GitHub Actions workflow is kept, but it only runs when started by hand
  (`workflow_dispatch`). It can be switched back to automatic runs by
  restoring the `push` and `pull_request` triggers.

## Alternatives considered

- **Delete the workflow entirely.** Loses a ready-made way to run checks on
  clean machines, e.g. on a different Python version.
- **Keep CI and filter the emails.** The red marks on commits and PRs would
  remain, and they would mislead reviewers.
- **pre-commit framework.** Adds a Python dependency and its own config
  format. A three-line shell hook that calls `make check` is easier to
  understand and needs nothing extra.

## Consequences

- The gate only runs on machines where `make hooks` has been run. The README
  calls this out as a setup step.
- Every check must stay runnable locally. That was already a rule: every CI
  step was a make target.
- Anything that later needs a cluster, such as the end-to-end and chaos tests
  in Phases 2–3, gets its own make target. It runs before opening a PR rather
  than on every push.
- Where an issue or older doc says "in CI", read it as "in `make check`" (or
  the dedicated make target for cluster tests).
