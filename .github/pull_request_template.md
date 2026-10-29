<!--
One pull request, one concern. If this one does two things, split it.
Hidden comments like this one are guidance; delete them or leave them.
For a prose-only change, use ?template=docs.md in the URL instead.
Headings and field names below are fixed: keep them exactly as written so
this description reads the same way every time, for reviewers and for tooling.
Describe the change, not who or what produced it. Fill every section; use
"None" or "N/A" where a field does not apply, and never leave a placeholder.
-->

## Executive summary

<!-- One short paragraph plus the touched areas. A reviewer who reads nothing
else should understand what changed, why, and what the outcome is. -->

Summary:

Touched areas:

## Roadmap links

<!-- Link the work to the repo's plan. "Closes #N" closes the issue on merge;
"Refs #N" does not. List every issue this touches. -->

- Closes: #
- Related issues:
- Roadmap phase:
- Paper features (F1–F24):
- Experiments (E0–E6):
- Design docs (ADRs):
- Course deliverable (design.md / demo / freeze):

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

<!-- Ordered walkthrough: list the changes in the order a reviewer should read
them. One bullet per area, naming the files or packages first. -->

-

## Blast radius

<!-- What this PR can affect. Answer every row; use "None" where there is no
effect. The table is the human-readable map; the metadata block below it
carries the same information in a form tooling can extract. Keep the two in sync. -->

| Area | Effect | Risk | Verified by |
| --- | --- | --- | --- |
| Coordinator / workers | | | |
| `mr` client / CLI | | | |
| Python reference implementation | | | |
| Protobuf / wire format / storage format | | | |
| Configuration / flags / Compose setup | | | |
| CI / tooling | | | |
| Documentation | | | |
| Data / test data / benchmarks | | | |

```yaml
# Machine-readable summary. Keep these values in sync with the table above.
scope: []  # e.g. [coordinator, docs]
behavior_change: false
storage_format_change: false
config_change: false
breaking_change: false
migration_required: false
tests: []  # e.g. [go-test, py-test, e2e]
```

## Verification

<!--
Paste the command and its result. "Tests pass" is not evidence.
A bug fix needs a test that fails without the fix; say which one.
-->

- [ ] `make check PYTHON=python` passes (proto-check, lint, `go test -race`, Python tests, coverage floors)
- [ ] New or changed behavior has a test, and it fails without the change: <!-- test name -->
- [ ] All required CI checks are green on the latest commit; if using `make ci-report`, `local-ci/make-check` has state `success` on that commit
- [ ] Run by hand where tests cannot reach: <!-- the command, or "not applicable" -->

<!-- Output of the commands above, trimmed to the lines that show the result: -->

```text

```

## Rollback

<!-- How to undo this PR if it causes a problem: revert, flag, or follow-up.
If it cannot be rolled back cleanly, say so and why. -->

## Follow-ups

<!-- Anything deliberately left out, with issue numbers. "None" means nothing
known is left out. -->

## Design and docs

- [ ] `design.md` still describes what the code does, or is updated here
- [ ] A decision with alternatives has an ADR in `docs/design/`, listed in `docs/README.md`
- [ ] README, `docs/`, flags and defaults match the change
- [ ] Generated code is current (`make proto-check`) if `proto/` changed
- [ ] None of these apply

## Review status

<!-- Every review thread needs a fix or a reason. Findings that stand but are
out of scope get their own issue. -->

- [ ] Each review thread has a fix or a reason, and findings I disagree with say why
- [ ] A finding that stands but is out of scope has its own issue
- [ ] The description was re-read against the final diff before requesting review

## Before merging

- [ ] One concern, a size a reviewer can read in one sitting
- [ ] The branch is not `main`, and `main` changes only through this pull request
- [ ] The branch is up to date with `main`
- [ ] Commits are authored by the repository owner
- [ ] No secrets, tokens, local paths or machine names in the diff or the description
- [ ] The title says what changes, in the imperative, and under 72 characters

## Notes for reviewers

<!-- Where to look first, what looks wrong but is not, what was left out on
purpose, and what to try. -->
