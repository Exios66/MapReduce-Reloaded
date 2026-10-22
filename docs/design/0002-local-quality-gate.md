# ADR 0002 — Local quality gate instead of hosted CI

**Status:** accepted · **Phase:** 0

## Context

GitHub Actions runs on this account kept failing for account-level reasons
unrelated to the code, and every failure sent an email. Failures that do not
reflect the code are noise, and they hide real problems.

We still want what CI gave us: every linter and test runs before code reaches
GitHub. Without hosted CI this becomes a local, best-effort gate, not a hard
guarantee (see Consequences).

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
- Because `make check` tests the working tree, the hook first makes sure the
  working tree *is* the pushed commit: every pushed branch must be the one
  checked out (`HEAD`), and there must be no uncommitted or untracked files.
  Otherwise an uncommitted local fix could make the checks pass while a
  broken commit is pushed.
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

- The gate is local and can be skipped. It only runs in clones where
  `make hooks` has been run (the README calls this out as a setup step), and
  `git push --no-verify` bypasses it. GitHub does not enforce it: there is no
  required status check on `main`. For a single-author project this is an
  accepted trade-off; before merging a PR, the reviewer can run `make check`
  on the branch or start the workflow by hand.
- To push a branch, check it out first. One push cannot update several
  branches at once unless they all point at `HEAD`.
- Every check must stay runnable locally. That was already a rule: every CI
  step was a make target.
- Anything that later needs a cluster, such as the end-to-end and chaos tests
  in Phases 2–3, gets its own make target. It runs before opening a PR rather
  than on every push.
- Where an issue or older doc says "in CI", read it as "in `make check`" (or
  the dedicated make target for cluster tests).
