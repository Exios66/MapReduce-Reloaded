# ADR 0021: No CI jobs on pushes to main

Date: 2026-10-30. Status: accepted. Supersedes the "`main` is never
filtered" bullet of ADR 0020.

## Context

Every push to `main` ran the setup job and then all six CI jobs in full
(ADR 0020). Each such push is a merge of a PR, and branch protection lets a
PR merge only when the six required checks (`proto`, `go`, `python-3.11`,
`python-3.13`, `lint`, `mapping-coverage`) passed on its head and the branch
was up to date with `main`. The merged commit's tree is the tree those checks
tested, so the run on `main` repeated a result we already had. On the Free
plan that was about a sixth of the credits spent (3,075 of about 17,400 on
2026-10-29): `main` pipeline #331, a merge that day, ran the setup workflow
and all six jobs again.

No project setting stops it. The CircleCI project has no "Only build pull
requests" option, and "Auto-cancel redundant workflows" excludes the default
branch. The fix has to be in the config.

## Decision

On `main`, the setup config's `select` job finishes the pipeline instead of
continuing it. Its steps are two conditional steps on
`pipeline.git.branch`:

- `unless` the branch is `main`: check out, run `scripts/ci-params.py`, and
  continue with `.circleci/continue-config.yml`, exactly as before.
- `when` the branch is `main`: run the continuation orb's `finish` command,
  which continues the pipeline with an empty configuration. No continuation
  workflow and no CI job runs.

A push to `main` therefore costs one small `select` job of a few seconds,
with no checkout, and produces no other job. PR pipelines are unchanged: the
`unless` branch holds the same three steps, so every PR still reports the
six checks.

### Alternatives rejected

- **Branch filters on the continuation workflow's jobs** (`filters:
  branches: ignore: main`). The setup job would still check out, select and
  continue on `main`, and a filter on each of the six required jobs is six
  places a mistake could drop a check from PRs. CircleCI's documentation does
  not say what a continued workflow with every job filtered out reports. We
  would have to find out on `main`.
- **A filter or a workflow `when` that leaves the setup workflow with no job.**
  CircleCI's documentation requires that a setup config let exactly one
  workflow run per pipeline. It does not say how a pipeline that is never
  continued ends. The orb's `finish` command is the documented way to end a
  setup pipeline without running further workflows.

## Consequences

- Coverage is unchanged. Every commit that reaches `main` is a tree the six
  checks passed on its PR. Strict status checks make the PR's tested tree the
  merged tree.
- Nothing tests `main` after a merge. A failure that depends on the merge
  itself, rather than on the tree, is not caught there. The up-to-date rule
  is what prevents two separately green PRs from landing broken. To test
  `main` as it is, open a PR from a branch at `main`'s HEAD.
- The `main` conditions in the continuation config and `run-all` for `main`
  in `scripts/ci-params.py` stay. They are not reached on a push. If this
  gate were removed, they would make `main` run everything again, as in ADR
  0020.
- To roll back, replace the `select` job's two conditional steps with the
  three steps inside `unless`.
