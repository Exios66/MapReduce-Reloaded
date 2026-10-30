# Agent instructions

Rules for any coding agent working in this repository.

## Commits and PRs: the owner's name only

Every commit is authored and committed as the repository owner,
`Jack J Burleson // LJM <exios4@protonmail.com>`. Never commit under an
agent's or a tool's identity.

- No `Co-Authored-By` lines, `Generated with` lines, or other agent or tool
  footers in commit messages, PR titles, or PR descriptions.
- No agent attribution or signatures in code, comments, or docs.
- Avoid posting GitHub comments. Fix review findings in code, and resolve
  threads without replying unless a reply is needed.

## Changelog: every PR adds its entry

`CHANGELOG.md` records every notable change, newest first, in the
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) format, with
[Semantic Versioning](https://semver.org/spec/v2.0.0.html) for releases.
`python/tests/test_changelog.py` checks its layout in `make check`.
The file turns off pymarkdown's duplicate-heading rule (MD024) for itself
only, because every release repeats the same categories; keep that pragma.

### In every PR

1. Add one bullet per notable change under `## [Unreleased]`, in the same
   PR as the change, under the category that fits: `Added`, `Changed`,
   `Deprecated`, `Removed`, `Fixed` or `Security`. Create the `###`
   subsection if it is missing, in that order, and never repeat one.
2. Write for someone using the repository: what changed and where, in plain
   words, not how it was done. Match the existing entries: start with the
   area (`Python coordinator:`, `CI:`, `Docs:`), name files and flags in
   backticks, and cite the ADR when there is one.
3. End each bullet with the PR number, `(#NNN)`. You learn it when you open
   the PR; add it in the next push, a review round's `[skip ci]` push or the
   final push, so it never costs a pipeline of its own.
4. A change no user would notice (a typo, a comment, a test-only refactor)
   needs no entry. When unsure, add one.
5. When merging `main` into the branch conflicts in `CHANGELOG.md`, keep
   both sides' bullets; never drop another PR's entry.

### Cutting a release

Only the repository owner decides when to release. Agents tag or publish
releases only when asked. While the version is 0.y.z, raise the minor
version when the release adds or changes behavior, and the patch version
when it only fixes bugs or changes docs. To cut version `X.Y.Z`:

1. In one PR, rename `## [Unreleased]` to `## [X.Y.Z] - YYYY-MM-DD`, with
   today's date and a one- or two-sentence summary under it, and add a new,
   empty `## [Unreleased]` above it.
2. Update the links at the bottom: `[Unreleased]` compares `vX.Y.Z...HEAD`,
   and a new `[X.Y.Z]` line compares the previous tag with `vX.Y.Z`.
3. Once that PR merges, tag the merge commit on `main` as `vX.Y.Z` and
   publish a GitHub release named `Version X.Y.Z` whose notes are that
   version's section.

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
   review is still going on: fixes for review comments and work-in-progress
   commits. CircleCI skips a push's pipeline when the pushed head commit's
   message contains `[skip ci]`, but opening a PR always runs one pipeline,
   so open the PR only when its work is ready for review.
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
