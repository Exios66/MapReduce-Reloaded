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
- `main` is changed only through pull requests. That is a project rule, not
  something GitHub enforces yet. To enforce it, the owner adds a branch
  protection rule for `main` (Settings, Branches) that requires a pull
  request and the status checks `proto`, `go`, `python` (or
  `local-ci/make-check`, see below). Until then the rule rests on practice
  and on review.

## Workaround while hosted runners are unavailable

On the owner's account every GitHub-hosted job currently fails within
seconds, before any step runs. This is an account or billing problem, not a
problem in the code. There are two ways round it, and both record the same
checks on the PR.

- **Self-hosted runner.** Every job uses `runs-on: ${{ vars.CI_RUNNER ||
  'ubuntu-latest' }}`. Register a self-hosted runner on the course VM
  (Settings, Actions, Runners), then set the repository variable
  `CI_RUNNER` to `self-hosted`. Self-hosted runners do not use hosted
  minutes. The VM needs Linux x64, `git`, `make`, `curl`,
  `tar` and `unzip`; the setup actions install Go and Python. Docker is not
  needed. Unset the variable to go back to hosted runners. The repository
  is public, so a pull request from a fork would run its code on the VM:
  under Settings, Actions, General, require approval for workflows from
  all outside collaborators. Hosted runners are free for public
  repositories, so the failure is an account lock rather than spent
  minutes, and a lock may stop self-hosted jobs too.
- **Local report.** `make ci-report` runs `make check` on a clean clone of
  `HEAD` and posts a commit status `local-ci/make-check` (pending, then
  success or failure, with duration and host) through the GitHub API. It
  needs `GITHUB_TOKEN` or `GH_TOKEN`: a fine-grained token with "Commit
  statuses: write" on this repository. It refuses to run with uncommitted
  changes, and never prints the token. Run it after the last push to a PR.
  It does not depend on Actions at all, so it is the dependable option.

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
- While hosted runners fail, the PR shows either the self-hosted run or the
  `local-ci/make-check` status. A person has to run `make ci-report`.
- ADR 0002's hook and its rules for pushing (check out the branch first,
  no uncommitted files) still apply.
