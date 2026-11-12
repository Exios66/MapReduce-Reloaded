---
name: saving-ci-credits
description: Use when about to push a branch, open or update a pull request, merge main into a branch, or decide whether a push should carry [skip ci], in MapReduce-Reloaded, where CircleCI Free credits are limited.
---

# Saving CI credits

CircleCI's Free plan is the only CI (AGENTS.md, docs/ci.md, ADR 0014). Credits are saved by running CI less often, never by running less of it: one full pipeline per PR, on its final head.

## Before every push

```bash
bash .claude/skills/saving-ci-credits/preflight.sh          # an in-progress push
bash .claude/skills/saving-ci-credits/preflight.sh --final  # the one push that runs CI
```

It checks the owner identity and the absence of attribution footers, lists the jobs the diff implicates (`scripts/ci-params.py`), checks the CHANGELOG bullet and its `(#NNN)`, how far the branch is behind `origin/main`, and whether the head's `[skip ci]` matches the kind of push. Exit 1 means do not push yet.

## Which push is which

| Situation | Head commit | Behind main |
|---|---|---|
| Work or review still going on | `[skip ci]` | leave it |
| Opening the PR | anything: opening always runs one pipeline, so open only when ready for review | leave it |
| Final push | no `[skip ci]` | 0: merge main, then `make check` |
| Final pipeline failed | fix, push without `[skip ci]` | 0 |

## What a pipeline costs

A change to `.circleci/`, `Makefile`, `scripts/`, `bin/`, `go/go.mod`, `python/pyproject.toml` or `.gitignore`, or any deletion, runs every job (`run-all`); other paths run only the jobs `.circleci/mapping.txt` names, and a path it does not match runs every job and fails `mapping-coverage` until it is mapped. A PR that changes only `python/`, only `README.md` or `docs/paper-mapping.md`, or only shell scripts runs the `go` job in a narrow mode (two Go packages, or shellcheck alone) instead of the full suite (ADR 0025). The `go` job runs on three nodes but is still one check, so there are still six required checks (ADR 0024). With warm caches `go` takes about 200 s and is the critical path; a change to a cache key (the Makefile's tool pins, `go/go.sum`, the weekly build key) makes that run cold, and the first run after one is slower without anything being wrong.

## Common mistakes

- Pushing each fix separately: batch commits, push once per review round.
- Running `make check` on a busy laptop: run it on the experiment VM.
- Merging main mid-review: it costs nothing now but forces a re-check later; do it once, in the final push.
- Empty commits or closing and reopening a PR to rerun CI: never.
- Narrowing `.circleci/mapping.txt` or `scripts/ci-params.py` to skip jobs: never.
- Opening a PR, then pushing again only to add its number to the CHANGELOG: preflight names the likely number, so cite it before opening.
- Reading a slow first run after a cache-key change as a regression: compare with the next warm run.
