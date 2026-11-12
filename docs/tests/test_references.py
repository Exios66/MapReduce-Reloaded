"""Every name a guide gives the reader must exist: make targets, repository
paths, ADRs, scripts and skills (make docs-test, ADR 0025).

scripts/check-links.py checks Markdown links. These tests check the names
written as code, which no link check sees and which go stale when a target,
a file or an ADR is renamed: a guide that says to run `make foo` or read
`scripts/foo.py` must name something that is there. Each test reports every
stale name at once, with the file that gives it.
"""

import re
import subprocess
from functools import cache
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]

# The top-level directories a path written in a guide is checked under.
CHECKED_DIRS = (
    ".circleci",
    ".claude",
    ".githooks",
    "bench",
    "deploy",
    "docs",
    "go",
    "proto",
    "python",
    "scripts",
    "testdata",
)
# Paths a guide names because they do not exist, each with where and why.
ABSENT = {
    "python/examples/": "docs/explainers/11-local-mode.md says why the examples are not there",
}

FENCE = re.compile(r"^\s{0,3}(`{3,}|~{3,})")
CODE_SPAN = re.compile(r"`([^`\n]+)`")
PATH = re.compile(rf"(?<![\w./<>-])((?:{'|'.join(map(re.escape, CHECKED_DIRS))})/[\w./-]*)")
ADR_MENTION = re.compile(r"\bADR (\d{4})\b")
TARGET = re.compile(r"^([A-Za-z0-9][\w.-]*(?:[ \t]+[A-Za-z0-9][\w.-]*)*)[ \t]*:(?![=:])", re.M)
CITATION = re.compile(r"\((?:[^()]*#\d+[^()]*|`[0-9a-f]{7,40}`[^()]*)\)\.?$")


@cache
def tracked() -> tuple[str, ...]:
    out = subprocess.run(["git", "ls-files", "-z"], cwd=ROOT, capture_output=True, text=True)
    if out.returncode != 0:
        pytest.skip("not a git checkout")
    return tuple(f for f in out.stdout.split("\0") if f)


@cache
def tracked_dirs() -> frozenset[str]:
    return frozenset(str(p) for f in tracked() for p in Path(f).parents if str(p) != ".")


@cache
def pages() -> dict[str, str]:
    """Every tracked Markdown file's text."""
    return {f: (ROOT / f).read_text(encoding="utf-8") for f in tracked() if f.endswith(".md")}


def guides() -> dict[str, str]:
    """The pages that describe the repository as it is now: all but the
    CHANGELOG, whose entries name files and targets as they were then."""
    return {f: text for f, text in pages().items() if f != "CHANGELOG.md"}


def split_code(text: str) -> tuple[list[str], list[str]]:
    """The lines outside fenced code, and those inside it."""
    prose, code, fence = [], [], None
    for line in text.splitlines():
        m = FENCE.match(line)
        if fence is None and m:
            fence = m[1][0]
        elif fence is not None and m and m[1][0] == fence:
            fence = None
        else:
            (prose if fence is None else code).append(line)
    return prose, code


def make_commands(text: str):
    """The make commands a page gives: in a code span, or as a line of
    fenced code, each with the targets it names (variables and flags end
    the list)."""
    prose, code = split_code(text)
    commands = [s for line in prose for s in CODE_SPAN.findall(line)]
    commands += [line.strip() for line in code]
    for command in commands:
        words = command.split()
        if len(words) < 2 or words[0] != "make":
            continue
        targets = []
        for word in words[1:]:
            if not re.fullmatch(r"[A-Za-z0-9][\w.-]*", word):
                break
            targets.append(word)
        yield command, targets


@cache
def make_targets() -> frozenset[str]:
    makefile = (ROOT / "Makefile").read_text(encoding="utf-8")
    return frozenset(t for names in TARGET.findall(makefile) for t in names.split())


def ignored(paths: list[str]) -> set[str]:
    """Those of paths that .gitignore covers: what a build or a run writes.

    A pattern for a directory (`.venv/`) matches a path that is not on disk
    only when the path ends in a slash, and a clean checkout has none of the
    output, so each path is asked about both as a file and as a directory.
    """
    if not paths:
        return set()
    asked = {form: path for path in paths for form in (path, path.rstrip("/") + "/")}
    out = subprocess.run(
        ["git", "check-ignore", "--no-index", "--stdin"],
        cwd=ROOT,
        input="\n".join(asked),
        capture_output=True,
        text=True,
    )
    return {asked[form] for form in out.stdout.split() if form in asked}


def test_make_targets_named_in_the_guides_exist():
    targets = make_targets()
    stale = sorted(
        f"{page}: `{command}` names `{target}`, which the Makefile does not define"
        for page, text in guides().items()
        for command, names in make_commands(text)
        for target in names
        if target not in targets
    )
    assert not stale, "\n".join(stale)


def test_repository_paths_named_in_the_guides_exist():
    """A path in a code span names a tracked file or directory, or output
    that .gitignore covers. A glob or a placeholder (`*`, `{`, `<`) is not
    checked."""
    files, dirs = set(tracked()), tracked_dirs()
    named = {}
    for page, text in guides().items():
        for line in split_code(text)[0]:
            for span in CODE_SPAN.findall(line):
                for m in PATH.finditer(span):
                    path, after = m[1], span[m.end() : m.end() + 1]
                    if after in ("*", "{", "<", "-") or path.rstrip("/.") != path.rstrip("/"):
                        continue
                    if path in ABSENT or path.rstrip("/") in files | dirs:
                        continue
                    named.setdefault(path, set()).add(page)
    missing = set(named) - ignored(list(named))
    stale = sorted(f"{page}: `{path}`" for path in missing for page in named[path])
    assert not stale, "these paths are not in the repository:\n" + "\n".join(stale)


def test_absent_paths_are_still_absent_and_still_named():
    for path, why in ABSENT.items():
        assert path.rstrip("/") not in set(tracked()) | tracked_dirs(), f"{path} exists now"
        page = why.split()[0]
        assert f"`{path}`" in pages()[page], f"{page} no longer names {path}"


@cache
def adrs() -> dict[str, str]:
    """Each ADR's number and file name."""
    return {
        Path(f).name[:4]: Path(f).name
        for f in tracked()
        if re.fullmatch(r"docs/design/\d{4}-[\w-]+\.md", f)
    }


def test_every_adr_mentioned_exists():
    stale = sorted(
        f"{page}: ADR {n}"
        for page, text in pages().items()
        for n in ADR_MENTION.findall("\n".join(split_code(text)[0]))
        if n not in adrs()
    )
    assert not stale, "\n".join(stale)


@pytest.mark.parametrize("number", sorted(adrs()) if (ROOT / ".git").exists() else [])
def test_each_adr_has_its_number_and_a_status_and_is_indexed(number):
    name = adrs()[number]
    lines = pages()[f"docs/design/{name}"].splitlines()
    assert re.match(rf"# ADR {number}\b", lines[0]), f"{name}'s title: {lines[0]!r}"
    head = "\n".join(lines[1:6])
    assert re.search(r"^(Date: .*)?\**Status:?\**", head, re.M), f"{name} has no status line"
    assert f"design/{name}" in pages()["docs/README.md"], f"docs/README.md does not list {name}"


def test_every_script_is_listed_in_its_readme():
    readme = pages()["scripts/README.md"]
    unlisted = sorted(
        f
        for f in tracked()
        if re.fullmatch(r"scripts/[^/]+", f)
        and f != "scripts/README.md"
        and f"`{Path(f).name}`" not in readme
    )
    assert not unlisted, f"scripts/README.md does not list: {unlisted}"


def test_every_skill_is_named_after_its_directory_and_its_scripts_are_in_agents_md():
    agents = pages()["AGENTS.md"]
    skills = sorted({Path(f).parts[2] for f in tracked() if f.startswith(".claude/skills/")})
    assert skills
    for skill in skills:
        text = pages()[f".claude/skills/{skill}/SKILL.md"]
        front = re.match(r"---\n(.*?)\n---\n", text, re.S)
        assert front, f"{skill}/SKILL.md has no frontmatter"
        assert re.search(rf"^name: {re.escape(skill)}$", front[1], re.M), f"{skill}: name"
        assert re.search(r"^description: \S", front[1], re.M), f"{skill}: description"
        for f in tracked():
            if f.startswith(f".claude/skills/{skill}/") and f.endswith((".sh", ".py")):
                assert f in agents, f"AGENTS.md's tooling table does not list {f}"


def changelog_entries():
    """Each CHANGELOG bullet, joined onto one line, with its section."""
    section, entry = None, None
    for line in pages()["CHANGELOG.md"].splitlines():
        if line.startswith("## "):
            section = line[3:]
        if line.startswith("- "):
            if entry:
                yield entry
            entry = [section, line[2:]]
        elif entry and line.startswith("  "):
            entry[1] += " " + line.strip()
        elif entry:
            yield entry
            entry = None
    if entry:
        yield entry


def test_every_changelog_entry_cites_its_pr():
    """AGENTS.md: each bullet ends with its PR's number, (#NNN), or the
    commit it came from when it reached main without a PR."""
    uncited = [
        f"[{section}] …{text[-70:]}"
        for section, text in changelog_entries()
        if not CITATION.search(text)
    ]
    assert not uncited, "end each with (#NNN):\n" + "\n".join(uncited)
