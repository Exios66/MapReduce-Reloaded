<!--
One pull request, one concern. If this one does two things, split it.
Hidden comments like this one are guidance; delete them or leave them.
For a docs-only change, use ?template=docs.md in the URL instead.
-->

## Summary

<!-- What changes, and why. Lead with the reason; the diff shows the how. Two to four sentences. -->

## Linked issues

<!-- Closes #N closes the issue when this merges. Refs #N does not. List every issue this touches. -->

Closes #

## Base and order

<!-- Say if this is stacked on another pull request, so it merges in the right order. -->

- Base: `main`
- Depends on: none

## Type of change

- [ ] Bug fix
- [ ] New behavior or feature
- [ ] Refactor, no change in behavior
- [ ] Tests only
- [ ] Docs, design or ADR only
- [ ] Build, CI or tooling
- [ ] Deployment (Compose, Docker, scripts)

## What changed

<!-- Bullets, grouped by area. Name the files or packages a reviewer should read first. -->

-

## How it was tested

<!--
Paste the command and its result. "Tests pass" is not evidence.
A bug fix needs a test that fails without the fix; say which one.
-->

- [ ] `make check PYTHON=python` passes (proto-check, lint, `go test -race`, Python tests, coverage floors)
- [ ] New or changed behavior has a test, and it fails without the change: <!-- test name -->
- [ ] Checks recorded on this commit: CI is green, or `make ci-report` posted `local-ci/make-check` ([ADR 0014](https://github.com/Exios66/MapReduce-Reloaded/blob/main/docs/design/0014-ci-on-pull-requests.md))
- [ ] Run by hand where tests cannot reach: <!-- the command, or "not applicable" -->

<!-- Output of the commands above, trimmed to the lines that show the result: -->

```text

```

## Design and docs

- [ ] `design.md` still describes what the code does, or is updated here
- [ ] A decision with alternatives has an ADR in `docs/design/`, listed in `docs/README.md`
- [ ] README, `docs/`, flags and defaults match the change
- [ ] Generated code is current (`make proto-check`) if `proto/` changed
- [ ] None of these apply

## Risk

<!--
What could this break, and how would you notice? Anything that changes
recovery, the log format, the wire protocol, limits or defaults needs a line
here. For "none", say why.
-->

- Blast radius:
- Rollback: revert this pull request / <!-- or what else -->

## AI assistance and review

<!--
The course asks for feedback sought and given (design.md, Process). Say who
wrote this and who reviewed it, and answer every review thread.
-->

- Written by: <!-- me / an AI agent, which, and what I checked myself -->
- AI review requested from: <!-- CodeRabbit, CodeAnt, another -->
- [ ] Each review thread has a fix or a reason, and findings I disagree with say why
- [ ] A finding that stands but is out of scope has its own issue (use the review-finding form)
- [ ] If an AI agent wrote this, I read the diff line by line and left review comments before merging

## Before merging

- [ ] One concern, a size a reviewer can read in one sitting
- [ ] The branch is not `main`, and `main` changes only through this pull request
- [ ] Commits are authored by the repository owner, with no bot or agent co-author lines
- [ ] No secrets, tokens, local paths or machine names in the diff or the description
- [ ] The title says what changes, in the imperative, and under 72 characters

## Notes for reviewers

<!-- Where to look first, what looks wrong but is not, what was left out on purpose, and what to try. -->
