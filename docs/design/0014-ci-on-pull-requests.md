# ADR 0014 — CI on every pull request

**Status:** accepted · **Supersedes:** [ADR 0002](0002-local-quality-gate.md)'s
decision to run GitHub Actions by hand only

## Context

[ADR 0002](0002-local-quality-gate.md) turned off automatic GitHub Actions
runs because account-level failures made them noisy, and made `make check`,
run by a pre-push hook, the only gate.

The course's engineering practices ask for the opposite: work lands through
small pull requests, and Actions run tests on those pull requests. A local
hook leaves no record on the PR, so a reviewer (a person, or an AI agent
playing that role on a solo project) cannot see that the checks passed.

## Decision

- `.github/workflows/ci.yml` runs on every `pull_request`, on pushes to
  `main` and by hand. It has three jobs, each a set of make targets:
  - `proto`: `make proto-check` (buf lint, buf breaking against `main`, and
    a check that the generated code is up to date);
  - `go`: `make go-lint go-test`, with `golangci-lint` built from source by
    the module's Go version, as the README requires;
  - `python`: `make py-lint py-test` on Python 3.11 and 3.13.
- A newer push to the same branch cancels the run still in progress.
- `make check` and the pre-push hook stay. They catch failures before a
  push, and CI shows the result on the PR.
- `main` is changed only by merging a pull request whose checks pass.

## Alternatives considered

- **Keep runs manual (ADR 0002).** No record on the PR, which the course
  asks for.
- **Run only the tests, without the proto and lint checks.** Cheaper, but
  stale generated code or lint failures would reach `main`, and `make check`
  would then fail for everyone.
- **Run the Compose end-to-end test in CI.** It needs Docker, takes
  minutes, and measures nothing a hosted runner can tell us about the VM.
  `make e2e` stays a step before opening a cluster-facing PR.

## Consequences

- Every PR shows a pass or fail for each job. A failure that has nothing to
  do with the code is visible on the PR, and is explained there rather than
  hidden.
- Everything CI runs is still a make target, so every check can be
  reproduced locally.
- ADR 0002's hook and its rules for pushing (check out the branch first,
  no uncommitted files) still apply.
