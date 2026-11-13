# Pre-freeze documentation and verification audit: implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Before the Nov 16 code freeze, every consumer-facing page (README
and the guides a user, grader or demo audience reads) cites only links,
commands, flags, numbers and instructions that work at the freeze commit. The
checks that need the course VM are listed as a runbook for the owner.

**Architecture:** Work in two lanes. **Lane A (this session, no Docker
daemon):** an audit of every consumer page. It checks external URLs over the
network, runs each command that needs no cluster, and recomputes each
published number from `bench/evaluation/`. Each defect becomes a fix in
one docs PR. Lasting gaps get a test in `docs/tests/` only where a test can
stop the defect coming back. **Lane B (the owner, on the course VM):** the
#272 evidence runs at the freeze commit. Lane A produces their checklist.

**Tech Stack:** Markdown, pytest (`docs/tests/`), `scripts/check-links.py`,
Go 1.26 (`go run ./cmd/mr`), Python 3.11+ (`python -m mapreduce_reloaded`),
`.claude/skills/interpreting-bench-results/summarize.py`.

**Spec:** the user's request of 2026-11-12 ("ensure our README is fully
clear, citing only valid links, commands, and instructions, as well as all
other consumer facing documentation & functionalities"), plus
issues #347 (status 2026-11-12), #272 (freeze gate), #269, #276 item 6.

## Global Constraints

- Branch: `claude/affectionate-hypatia-nj7lmc`, restarted from `origin/main`
  at `018296f` (its earlier PR, #352, is merged).
- Commits: author and committer `Jack J Burleson <exios4@protonmail.com>`,
  no `Co-Authored-By` or `Generated with` lines (AGENTS.md).
- Every push while work is in progress carries `[skip ci]`. The PR opens only
  when ready for review. The final push merges `main`, passes `make check`,
  and runs CI once without `[skip ci]` (AGENTS.md, ADR 0014).
- One `CHANGELOG.md` bullet per notable change under `## [Unreleased]`,
  starting `Docs:`, ending `(#NNN)` once the PR number is known.
- Never weaken a test, `.circleci/mapping.txt` or `scripts/ci-params.py`.
- Change no result number unless it is recomputed from
  `bench/evaluation/**/runs.csv`. State the clock (`seconds` or
  `wall_seconds`). Overlapping ranges read "no clear difference".
- Do not edit `docs/design/` ADRs or past `CHANGELOG.md` entries: they record
  history, not the current state.
- No network checks inside `make check` or CI: external URLs are checked once
  by hand, here and again at the freeze.

## Scope: the consumer-facing pages

`README.md`, `docs/README.md`, `docs/demo.md`, `docs/runbook.md`,
`docs/cluster-vm.md`, `docs/reviewing.md`, `docs/report.md`,
`docs/evaluation.md`, `docs/data.md`, `docs/testing.md`, `bench/README.md`,
`examples/README.md`, `examples/demo.ipynb`, `deploy/compose/README.md`,
`testdata/corpus/README.md`, `SECURITY.md`. Out of scope: ADRs, explainers'
historical narrative, `CHANGELOG.md` history, `docs/roadmap*.md` except
statements of current status.

## What is already checked (do not duplicate)

| Check | Where |
|---|---|
| Relative links and `#anchors` resolve | `scripts/check-links.py` (`make lint`) |
| `make` targets named in guides exist | `docs/tests/test_references.py::test_make_targets_named_in_the_guides_exist` |
| Repo paths in code spans exist | `test_repository_paths_named_in_the_guides_exist` |
| ADRs mentioned exist and are indexed | `test_every_adr_mentioned_exists` |
| README `mr` flags are registered flags | `go/cmd/mr/main_test.go::TestUsageMatchesTheFlags` |
| Figures match their CSVs | `make figures-check` (#357) |
| Compose guide's limits, profiles, mounts | `docs/tests/test_documentation.py` |

Not checked anywhere today: **external URLs**, **commands other than `make`
actually running** (Python CLI flags, script arguments such as
`scripts/fetch-data.sh warpeace`, `make bench SCENARIO=…` values), **ports
and environment variables**, **result numbers in prose** (only figure titles
are computed), and **the notebook's text cells**.

## Review Focus

1. **A reader on macOS runs the README's local-mode block.** The block uses
   `/tmp` and `sort -t$'\t'`, which are bash-only. Expect: the README says the
   shell it assumes, or the commands are portable. Task 3 runs the block in `sh`
   as well as `bash`.
2. **A reader without `make venv` runs `pip install -e .` in the system
   Python** (README "Local mode"). On Debian-family systems this fails with
   "externally-managed-environment". Expect a venv line. Task 3 checks it.
3. **A redirected URL that still returns 200 but lands on a different page**
   (Docker docs reorganise often). Expect the final URL to be the cited topic.
   Task 1 records each redirect's final location, and a redirect to a generic
   page counts as broken.
4. **A number quoted in prose but not in a figure title**, e.g. README
   Results "E3 … +8 s to +26 s". Expect each to equal the recomputed value
   at the stated rounding. Task 4 recomputes every row.
5. **Stale "how to run" text that names an older default**, e.g. port
   18780, `$MR_DATA_DIR` default `./data`, "Default: 2 per worker" for `-r`.
   Expect each to match the code. Task 2 checks each against its source.

---

### Task 1: External URL audit

**Files:**

- Create (scratch, not committed): `/tmp/claude-0/url-audit.py`, output `/tmp/claude-0/url-audit.tsv`
- Modify: whichever consumer pages hold a dead or misdirected URL

**Interfaces:**

- Produces: `url-audit.tsv` with columns `url, status, final_url, pages`, consumed by Task 6's report.

- [ ] **Step 1:** Extract every `http(s)://` URL from the scope pages (42
  unique on `018296f`). Skip loopback, `minio:`, `coordinator:` and
  placeholder hosts. Use `python3 -I` with `urllib.request`, a `HEAD` request
  falling back to `GET`, a 15 s timeout and a generic User-Agent (no personal
  data), and follow redirects.
- [ ] **Step 2:** Run it. Expected: one row per URL. A URL counts as **broken**
  if its status is ≥ 400 or the request times out, or if it redirects to a
  generic page (a site root, or a docs index for a topic link).
- [ ] **Step 3:** For each broken URL, find the current location of the same
  document on the same site, or remove the link if none exists. The proxy may
  block some hosts with 403; mark those "unverified (proxy)" rather than broken,
  and list them for the owner to click.
- [ ] **Step 4:** Check the special cases by hand:
  - The CircleCI badge renders `main`'s status.
  - `security/advisories/new` is reachable when signed in, which only the
    owner can confirm.
  - The paper PDF URL.
  - `data.commoncrawl.org/.../wet.paths.gz`, with a `HEAD` request only, since
    the file is large.
- [ ] **Step 5:** Commit the fixes:
  `git commit -m "Docs: replace dead and moved external links [skip ci]"`.

### Task 2: Commands, flags, ports and environment variables

**Files:**

- Modify: consumer pages with a wrong command or default
- Test: `docs/tests/test_references.py` (add the test below)

**Interfaces:**

- Consumes: `guides()`, `split_code()` from `docs/tests/test_references.py`.
- Produces: `test_scripts_named_with_arguments_accept_them()`.

- [ ] **Step 1: Inventory.** List every command line in the scope pages'
  code blocks and code spans that is not a `make` target:
  - `bin/mr …` and `go run ./cmd/mr …`
  - `python3 -m mapreduce_reloaded …`
  - `scripts/*.sh …` and `bench/*.sh …`
  - `docker …` / `docker compose …`
  - `pip …`
  - `jupyter …`
- [ ] **Step 2: Check each against its source without a cluster:**
  - Python CLI: `python -m mapreduce_reloaded run --help` must list every
    flag a page passes (`--local`, `-m`, `-r`).
  - Script arguments: each `scripts/fetch-data.sh NAME` that a page cites must
    be a case the script accepts. Read the script's `case` arms.
  - `make bench SCENARIO=X`: each X in README "Benchmarks" (`grep`, `sort`,
    `zipf`, `book`) must be a value the bench target accepts.
  - Ports: `18780` (status), Prometheus' and Grafana's ports must match
    `deploy/compose/*.yaml` `ports:`.
  - Environment variables: `MR_DATA_DIR` must default to `./data` in
    `go/cmd/mr`.
  - The `-r` default ("2 per worker") and the `-m` default ("one per 64 MiB")
    must match the code that computes them.
- [ ] **Step 3: Write the failing test** for the one class that can regress
  without notice, script arguments:

```python
def test_scripts_named_with_arguments_accept_them():
    """`scripts/fetch-data.sh NAME` in a guide names a dataset the script knows."""
    # case arms such as `warpeace | all)`: alternatives may be spaced
    arms = re.findall(r"^\s*([a-z0-9][a-z0-9| -]*)\)", (ROOT / "scripts/fetch-data.sh").read_text(), re.M)
    names = {n.strip() for arm in arms for n in arm.split("|")}
    assert "warpeace" in names, "the case arms were not parsed"
    cited = {m for text in guides().values() for m in re.findall(r"scripts/fetch-data\.sh\s+([a-z0-9-]+)", text)}
    assert cited <= names, sorted(cited - names)
```

  Run `python -m pytest docs/tests/test_references.py -k scripts_named_with_arguments -q`.
  Expected: PASS if no page is wrong. If none is wrong, keep the test anyway as
  a guard, and confirm it fails by citing a fake name in a scratch copy.

- [ ] **Step 4:** Fix every mismatch found in Step 2, in the page (or in the
  code, if the page states the intended behavior and the code is the bug; such a
  code fix gets its own failing test first).
- [ ] **Step 5:** Run `make docs-test lint`. Expected: pass.
- [ ] **Step 6: Commit:** `"Docs: commands and defaults match the code; test fetch-data names [skip ci]"`.

### Task 3: Run every no-cluster instruction literally

**Files:**

- Modify: `README.md` ("Local mode", "Development"), `examples/README.md`, `docs/testing.md` as needed

- [ ] **Step 1:** In a fresh clone checked out at this PR's head commit
  under `/tmp/claude-0/fresh`, with a clean `PATH`, run the README "Local
  mode" block line by line in `bash`, and again after every edit to it.
  Expected: `identical`. Record the output.
- [ ] **Step 2:** Run the same block in `sh` (dash). If `sort -t$'\t'` or
  anything else breaks, the README says "in bash" before the block (Review
  Focus 1).
- [ ] **Step 3:** Run `pip install -e .` as written in a Python with no
  venv. If it fails with "externally-managed-environment", add the venv lines
  the README already implies (`python3 -m venv .venv && . .venv/bin/activate`)
  (Review Focus 2).
- [ ] **Step 4:** Run the "Development" block (`make venv`, `make tools`,
  `make hooks`, `make check`) in the fresh clone. Expected: `make check`
  exits 0. Record the pass counts, to compare with the counts the docs quote.
- [ ] **Step 5:** Run the README quick start up to the first command that
  needs the Docker daemon (`make cluster-up`), then hand the rest to Task 7.
  Confirm `make mr` without Go is documented as needing Docker; it can't run
  here.
- [ ] **Step 6:** Run `examples/README.md`'s commands that need no cluster.
- [ ] **Step 7: Commit:** `"Docs: local-mode and development instructions run as written [skip ci]"`.

### Task 4: Every published number against the data

**Files:**

- Modify: `README.md` "Results", `docs/report.md` results table, `docs/demo.md`, `docs/evaluation.md`, `bench/README.md`, `examples/demo.ipynb` markdown cells
- Read: the data paths listed in the rerun tracking issue #347 (its
  2026-11-11 comments), which named them under a rerun-8f0ddb9 directory,
  with an E0-isolated directory beside them. On 13 November they moved to the core location,
  `bench/evaluation/E0/` to `E7/` and `E2-pairs/` (`runs.csv`,
  `machine.json`, `cache.jsonl`, `QUALITY.txt`), with E0 the isolated one,
  recorded at `3a907de` (tag `e0-isolated-run-3a907de`). The chain's first
  E0 is in `bench/evaluation/archive/8f0ddb9-E0-first/`.

- [ ] **Step 1:** Run
  `summarize.py` from `.claude/skills/interpreting-bench-results/` on
  `bench/evaluation/E0` and the `E1`–`E7` and `E2-pairs` directories
  beside it.
- [ ] **Step 2:** Compare each chunk's summary with the numbers #347's
  comments posted (including its corrections of 2026-11-11 15:58 and
  16:59). A difference is a finding, not a fix: report it.
- [ ] **Step 3:** For each README Results row (E0–E7), and for the same claim
  in `report.md`, `demo.md`, `evaluation.md`, `bench/README.md` and the
  notebook, recompute the number. Use the median of the measured settings at
  the page's rounding, excluding `warmup` and `calibrate`. Write a table of
  page, claim, recomputed value and verdict into the PR description.
- [ ] **Step 4:** Check the stated commits. The README says `8f0ddb9`, with
  E0 at `3a907de`. Expected: every `machine.json` names one of these, with
  `dirty: false`, and the tag `e0-isolated-run-3a907de` exists on the remote.
- [ ] **Step 5:** Fix each wrong number. Reword any finding whose ranges
  overlap (rule in `docs/evaluation.md`).
- [ ] **Step 6:** Run `make figures-check docs-test`. Expected: pass.
- [ ] **Step 7: Commit:** `"Docs: published results match bench/evaluation [skip ci]"`.

### Task 5: Read-through for clarity and current status

**Files:**

- Modify: scope pages

- [ ] **Step 1:** Read each scope page as a newcomer. Flag:
  - status statements that are now false. For example, README
    "Phases 0–4 and 7 … are done" and the open PR list in `docs/report.md`.
  - The report's "199 merged pull requests by 10 November 2026". Refresh it to
    the count at the freeze (owner's number; the plan leaves a `TODO(freeze)`
    marker listed in Task 7, and no other placeholder).
  - Instructions that assume context a reader doesn't have.
- [ ] **Step 2:** Check the #272 report items still open against
  `docs/report.md`:
  - the abstract says F1–F24 and E0–E7;
  - the process/PR-review section exists;
  - future work does not list the Python coordinator.

  Fix those not done.
- [ ] **Step 3:** Run `make md-lint docs-test`. Expected: pass.
- [ ] **Step 4: Commit:** `"Docs: current status and clarity pass before the freeze [skip ci]"`.

### Task 6: PR, CHANGELOG and the final run

- [ ] **Step 1:** Add one `### Fixed` bullet per kind of fix (links,
  commands, numbers, status) under `## [Unreleased]`, starting `Docs:`.
- [ ] **Step 2:** Run `bash .claude/skills/saving-ci-credits/preflight.sh`
  then push with `[skip ci]`, then open the PR from
  `.github/pull_request_template.md`. Put the URL audit, the numbers table
  and the commands run (Tasks 1–4) in its Verification section. Opening runs one
  pipeline. Docs-only, so the lint job only (ADR 0025).
- [ ] **Step 3:** After review, add `(#NNN)` to the bullets, merge `main`,
  run `make check`, and run `preflight.sh --final`. Then push without
  `[skip ci]`. Expected: every required check green on the head.

### Task 7: Owner's freeze-day runbook (course VM; this session cannot run it)

Not code: a checklist posted as a comment on #272. Each item's output is
its evidence.

- [ ] First, before choosing the freeze commit: update the merged-PR count
  in `docs/report.md`, so the checks below cover the report as frozen.
- [ ] At the freeze commit, from a fresh clone: run the README quick start
  verbatim, then `make e2e`, `make py-e2e` and `make check`.
- [ ] `make vm-check` and `make demo-check` outputs go to #272 and #276 item 6.
- [ ] A timed rehearsal of `docs/demo.md`, and `examples/demo.ipynb` run top to bottom.
- [ ] Click the links marked "unverified (proxy)" in Task 1 and the
  `security/advisories/new` link while signed in.
- [ ] Decide each open PR before the freeze:
  - #254 (10 GB campaign): merge or defer, per #273.
  - #339, #336, #333, #314: these conflict with `main`. Close #336 if
    superseded by ADR 0020 and #354, or rebase.
- [ ] Close #351 with a link to #352, which fixed it.
- [ ] #269: the email, demo scheduling and end form.

## Self-review notes

- Coverage: README links (T1), commands (T2, T3), instructions (T3),
  numbers (T4), the other consumer docs (T1–T5 over the scope list), and the
  "other work before the freeze" (T7).
- Only one new test (T2). Other defects are one-offs that the existing
  tests or `figures-check` already guard, or that need the network, which CI
  must not depend on.
