# ADR 0019: Implicated-only CI on pull requests

Date: 2026-10-29. Status: accepted (lands with the dynamic-config PR).

## Context

CircleCI runs the same four jobs on every PR (ADR 0014): `proto`, `go`,
`python-3.11`, `python-3.13`. That is correct but wasteful: a docs-only PR
ran the full compile-and-test suite, including two medium executors, and the
Free-plan credit allowance is the binding constraint on how many PRs the
project can check. The owner approved running only the jobs a PR's files
implicate, under a hard guardrail: no critical check may be weakened,
skipped in a way that reduces real coverage, or jeopardized for credit
savings. Where the design below and credit savings conflict, coverage wins.

## Decision

Split CircleCI into a setup config and a continuation config (CircleCI
dynamic configuration, path-filtering orb):

- `.circleci/config.yml` is the setup config (`setup: true`). One setup
  workflow runs `path-filtering/filter` against `main` and sets five boolean
  pipeline parameters (`run-all`, `run-proto`, `run-go`, `run-python`,
  `run-lint`) from the PR's changed files, then continues with
  `.circleci/continue-config.yml`.
- The continuation config holds the jobs. Every job always exists in the
  workflow, so every required check always reports. Skipping happens inside
  each job with `when`/`unless` wrapper steps: the skip note runs inside
  `unless` (only when the job is unimplicated) and all real steps run inside
  `when`, so a skipped job reports SUCCESS after seconds, without a checkout
  or any install. (A `when` key on an individual step is not a logic
  condition in CircleCI — it is an outcome enum — which is why the gating
  uses wrapper steps.)
- `md-lint` moves out of the Python jobs into a new `lint` job (small
  executor, Python 3.11, only pymarkdown installed): it used to force the
  full Python suite on docs-only PRs.
- A new `mapping-coverage` job always runs (no `when` gating) and fails the
  pipeline if any changed file matches no mapping pattern, so an unmapped
  file can never silently skip every job.
- After this lands, branch protection requires six checks: the original four
  plus `lint` and `mapping-coverage` (docs/ci.md, Transition). The `lint`
  check must be required, or moving `md-lint` out of the Python jobs would
  let Markdown failures merge.

## Mapping

One pattern may map to several parameters (multiple lines). Anchored
regexes; `re.search` semantics in the guard script.

| Changed files | Jobs run |
|---|---|
| `.circleci/**`, `Makefile`, `python/pyproject.toml`, `scripts/**`, `bin/**`, `go/go.mod` | all (run-all) |
| `go/**` | go |
| `python/**` | python-3.11, python-3.13, go (the go job runs py-e2e) |
| `proto/**` | proto |
| `*.sh` | go (sh-lint lives in the go job) |
| `*.md`, `docs/**`, `bench/**`, `examples/**`, `deploy/**`, `.github/**`, `LICENSE*`, root dotfiles | lint |
| anything else | guard fails the pipeline; the author adds a mapping |

## Alternatives considered

- **Status quo (full suite on every PR).** Safe, but the waste this ADR
  exists to remove. Rejected.
- **Aggregator/gate job (one always-run job requiring the rest).**
  Rejected: CircleCI `requires` is not satisfied by a job skipped with
  `when`, so a gate requiring skipped jobs would never run, and the
  required check would never report. The early-exit design above avoids
  the problem entirely: no job is ever removed from the workflow.
- **GitHub Actions paths-filter.** Rejected: Actions is disabled on the
  owner's billing lock (ADR 0014); the gate must stay on CircleCI.
- **Job-level `when` (skip whole jobs).** Rejected for the same reason as
  the aggregator: a skipped job reports no check, so a required check
  would hang pending. `when`/`unless` wrapper steps inside the job keep
  every check reporting.

## Why skipping is safe (the safety argument)

- A job's result is a function of its inputs. If none of a job's inputs
  changed, its result is the result it already had on `main`, which was
  green (main cannot merge red: the same checks gate it). Skipping such a
  job reuses that result; it does not waive it.
- Shared inputs map to run-all: CI configuration, the Makefile, the Python
  dependency manifest, shared scripts, the host client, and the Go
  toolchain file all run the full suite. Cross-job inputs are mapped too:
  Python changes run the go job because it runs py-e2e, and shell-script
  changes run the go job because it runs sh-lint.
- Unknown inputs fail loudly, not silently: the mapping-coverage job exits
  1 on any changed file no pattern matches, and it becomes a required
  check, so the PR cannot merge until the author maps the file (which
  runs the implicated jobs) or the mapping is fixed.
- Strict up-to-date protection makes the diff accurate at merge time: the
  branch must contain `main`'s HEAD before merging, so the setup workflow's
  diff against `main` is the PR's own changes, not stale-branch drift. On
  a stale branch the diff can only over-run (main's newer files also
  match), never under-run.
- The four original jobs keep their exact make targets; `make check`
  locally is unchanged. Only which PRs run them changes.

## Consequences

- Docs-only PRs cost the setup job, the lint job, the mapping-coverage job
  and four echo-only jobs instead of the full suite. Code PRs cost the
  same as before plus one small setup job.
- Adding a new top-level area to the repo requires adding its mapping;
  the guard job enforces that, and the PR cannot merge without it.
- The transition PR itself changes `.circleci/**`, so it maps to run-all
  and runs the full suite under the old four-check protection, merging
  normally with no admin bypass. Protection gains `lint` and
  `mapping-coverage` only after merge, by a separate owner action.
