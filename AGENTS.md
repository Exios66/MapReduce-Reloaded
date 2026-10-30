# Agent instructions

Rules for any coding agent working in this repository. CircleCI's Free plan
is the only CI (docs/ci.md, ADR 0014), and its credits must last until the
assignment is done, so every push has a cost.

## Batch pushes to save CI credits

Each push to a PR branch runs the full CircleCI pipeline for the files the PR
changes. A merged PR used to take about six pipelines. Aim for two or three.

1. Run `make check` locally before every push. Run `make hooks` once per
   clone so the pre-push hook runs it for you. CI is where a change is
   confirmed, not where failures are found.
2. Push once per review round, not once per fix. Commit fixes locally and
   push them together when the round is done.
3. Put `[skip ci]` in the commit message of a work-in-progress push that no
   one needs CI on yet. The last push before merging must not carry it:
   branch protection needs the six checks (`proto`, `go`, `python-3.11`,
   `python-3.13`, `lint`, `mapping-coverage`) green on the head commit.
4. Merge `main` into a PR branch only right before merging it. Branch
   protection requires the branch to be up to date, and each update re-runs
   CI, so do it once, at the end.
5. Do not push to re-run CI. Never push an empty commit, or close and reopen
   a PR, to trigger a pipeline.

Pushes to `main` run no CI jobs (ADR 0021): a PR merges only after its six
checks pass on a branch up to date with `main`, so the merged tree was
already tested. Do not add CI work to `main` pushes.
