<!--
For changes to prose only: design.md, README, docs/, ADRs. If the change also
touches code or tests, use the default template (drop ?template=docs.md).
Headings and field names below are fixed: keep them exactly as written so
this description reads the same way every time, for reviewers and for tooling.
Describe the change, not who or what produced it. Fill every section; use
"None" or "N/A" where a field does not apply, and never leave a placeholder.
-->

## Executive summary

<!-- What the docs say now that they did not, why the change was needed, and
which documents it touches. A reviewer who reads nothing else should
understand the change from this section. -->

Summary:

Touched areas:

## Roadmap links

<!-- Link the work to the repo's plan. "Closes #N" closes the issue on merge;
"Refs #N" does not. List every issue this touches. -->

- Closes: #
- Related issues:
- Design docs (ADRs):
- Course deliverable (design.md / demo / freeze):

## What changed

<!-- Ordered walkthrough: files and sections, in the order a reviewer should
read them. -->

-

## Blast radius

<!-- What this PR can affect. Answer every row; use "None" where there is no
effect. A prose-only PR must show "None" for code behavior; if it cannot,
use the default template instead. The table is the human-readable map; the
metadata block below it carries the same information in a form tooling can
extract. Keep the two in sync. -->

| Area | Effect | Risk | Verified by |
| --- | --- | --- | --- |
| Guides / README | | | |
| References / ADRs | | | |
| Examples / diagrams | | | |
| Links / anchors | | | |
| Code behavior | None | None | Not touched by this PR |

```yaml
# Machine-readable summary. Keep these values in sync with the table above.
scope: [docs]
behavior_change: false
storage_format_change: false
config_change: false
breaking_change: false
migration_required: false
tests: [md-lint]
```

## Verification

<!-- Every command in the text was run, and prints what the text says. -->

- [ ] Every claim about behavior matches the code, or names the test that shows it
- [ ] Numbers come from a recorded run, or say they are an estimate
- [ ] Links resolve, and a new ADR is listed in `docs/README.md`
- [ ] `design.md` and the ADRs do not contradict each other or this change
- [ ] `make check PYTHON=python` passes, or CI is green on this commit

<!-- Output of the commands above, trimmed to the lines that show the result: -->

```text

```

## Rollback

<!-- How to undo this PR if it causes a problem: revert, flag, or follow-up.
If it cannot be rolled back cleanly, say so and why. -->

## Follow-ups

<!-- Anything deliberately left out, with issue numbers. "None" means nothing
known is left out. -->

## Review status

<!-- Every review thread needs a fix or a reason. -->

- [ ] Each review thread has a fix or a reason, and findings I disagree with say why
- [ ] The description was re-read against the final diff before requesting review

## Before merging

- [ ] The branch is not `main`
- [ ] The branch is up to date with `main`
- [ ] Commits are authored by the repository owner
- [ ] No secrets, tokens, local paths or machine names in the diff or the description
- [ ] The title says what changes, in the imperative, and under 72 characters

## Notes for reviewers

<!-- Where to look first, what looks wrong but is not, what was left out on
purpose, and what to try. -->
