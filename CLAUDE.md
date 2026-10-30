# Claude Code instructions

Follow the rules in AGENTS.md, imported here so Claude Code loads them:

@AGENTS.md

In short: commit as the owner with no agent footers, add every PR's entry
to `CHANGELOG.md` under `## [Unreleased]`, and run the full CI suite on the
PR's final head, rerunning it after a failed final pipeline or after updating
the branch when `main` moves. AGENTS.md has the details.
