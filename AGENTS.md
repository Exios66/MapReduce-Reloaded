# Agent instructions

Rules for any coding agent working in this repository.

## CI: one full run per PR, at the end

CircleCI's Free plan is the only CI (docs/ci.md, ADR 0014), and its credits
must last until the assignment is done. Save credits by running CI less
often, never by running less of it. The full required suite (`proto`, `go`,
`python-3.11`, `python-3.13`, `lint`, `mapping-coverage`) runs once on each
PR's final head, after all work and reviews are done and before it merges
into `main`.

### While a PR is in progress

1. Test locally, not in CI. Run `make check` before every push. Run
   `make hooks` once per clone so the pre-push hook runs it for you. A push
   should confirm a change that already passes, not find out whether it does.
2. Put `[skip ci]` in the commit message of every push made while work or
   review is still going on: the first push that opens the PR, fixes for
   review comments, and work-in-progress commits. CircleCI skips the
   pipeline when the pushed head commit's message contains `[skip ci]`.
3. Batch commits. Commit locally as you go, and push once per review round,
   not once per fix.
4. Do not merge `main` into the branch while work is in progress. Branch
   protection requires the branch to be up to date with `main`, so update it
   once, as part of the final push.

### The final push

1. When all work is done and every review thread has a fix or a reply, merge
   `main` into the branch, run `make check`, and push without `[skip ci]`.
   That one pipeline runs the full required suite on the head that merges.
2. If that pipeline fails, fix the cause and push the fix without
   `[skip ci]`. If `main` moves before the PR merges, the branch must be
   updated and checked again, so merge PRs promptly once they are green.
3. Merge only when all six required checks are green on the head commit.
   Only the repository owner can approve merging without CI, one PR at a
   time.

### Never

- Never skip, disable, or weaken a test, a job, or a required check to save
  credits. Never narrow `.circleci/mapping.txt` or the selection in
  `scripts/ci-params.py`.
- Never push only to re-run CI: no empty commits, and no closing and
  reopening a PR to trigger a pipeline.

### After merging

Pushes to `main` run no CI jobs (ADR 0021): a PR merges only after its six
checks pass on a branch up to date with `main`, so the merged tree was
already tested. Do not add CI work to `main` pushes.
