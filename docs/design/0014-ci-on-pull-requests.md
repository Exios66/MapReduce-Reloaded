# ADR 0014 — CI on every pull request

**Status:** accepted · **Supersedes:** [ADR 0002](0002-local-quality-gate.md)'s
decision to run GitHub Actions by hand only · **Amended:** CircleCI Free is the
primary CI provider (#212, see [CI](../ci.md))

## Context

[ADR 0002](0002-local-quality-gate.md) turned off automatic GitHub Actions
runs because account-level failures made them noisy, and made `make check`,
run by a pre-push hook, the only gate.

The course's engineering practices ask for the opposite: work lands through
small pull requests, and Actions run tests on those pull requests. A local
hook leaves no record on the PR, so a reviewer (a person, or an AI agent
playing that role on a solo project) cannot see that the checks passed.

## Decision

- CircleCI is the sole CI provider. Its six checks are `proto`, `go`,
  `python-3.11`, `python-3.13`, `lint` and `mapping-coverage`; see
  [CI](../ci.md) for their commands and triggers.
- A newer push to a pull request branch cancels its run still in progress.
- `make check` and the pre-push hook stay. They catch failures before a
  push, and CI shows the result on the PR.
- `main` is changed only through pull requests. That is enforced by a branch
  protection rule for `main` (Settings, Branches; rule 85457483, strict
  status checks on since 2026-10-28, settings verified 2026-10-29) that
  requires a pull request and all six CircleCI checks: `proto`, `go`,
  `python-3.11`, `python-3.13`, `lint` and `mapping-coverage`, with branches
  up to date before merging and administrators exempt from the rule.

## CircleCI as the primary provider (#212)

GitHub-hosted jobs fail on the account lock described below, and every failure
notifies the owner. `.circleci/config.yml` therefore runs the same gate on
CircleCI's Free plan, in six jobs named `proto`, `go`, `python-3.11`,
`python-3.13`, `lint` and `mapping-coverage`. Each job calls make targets;
the original four call the same ones as the Actions jobs, so the two
providers cannot disagree about what a check means, and `lint` and
`mapping-coverage` are CircleCI-only additions for dynamic config (ADR 0020).
[CI](../ci.md) has the
jobs, the caches, the Free-plan limits, the setup the owner completes, and
rollback.

- CircleCI is the only CI. The Actions workflow was first kept, run only
  by hand (`workflow_dispatch`) and disabled in the repository's Actions
  settings, so the billing lock no longer failed a run on every push; on
  2026-10-29 it was deleted (docs/ci.md, Setup, step 5).
- The required checks are the six CircleCI checks listed in the Decision,
  in place of the former Actions checks `proto`, `go` and `python`.
- `make check`, the pre-push hook and `make ci-report` are unchanged.
- The Compose end-to-end test, the S3 test, the long chaos, fuzz and flaky
  runs and the benchmarks stay on their own targets, outside CI.
- The Free plan has a monthly credit allowance and is not unlimited. The
  setup uses no paid feature (remote Docker, Docker layer caching, larger
  resource classes).
- What is committed is the configuration. Connecting the project, requiring
  the checks and retiring the workflow are the owner's steps, in that order,
  and only the first green CircleCI run makes the provider live.

## Workaround while hosted runners are unavailable

On the owner's account every GitHub-hosted job currently fails within
seconds, before any step runs. The jobs have no log, but each one's check
run has an annotation that names the cause (confirmed 2026-10-27, #181):
"The job was not started because your account is locked due to a billing
issue." Only the account owner can clear it, under Settings, Billing.
There are two ways round it, and both record the same checks on the PR.

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
  Since #189 every PR has been merged on this status: `make check` runs on
  the course VM in a clean worktree of the PR's head commit, and the
  status is posted through the API, where it shows on the PR. The status
  is narrower than the workflow in one way: the Python checks run under
  the VM's one interpreter, 3.12, where the workflow's matrix runs 3.11 and
  3.13. A change that breaks only one of those versions can pass it, so
  until Actions run again, a change to the Python package that uses
  version-specific features is also tested under 3.11 by hand.

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
