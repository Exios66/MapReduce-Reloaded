"""CHANGELOG.md keeps the Keep a Changelog layout that AGENTS.md describes."""

import re
from datetime import date
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
CHANGELOG = (ROOT / "CHANGELOG.md").read_text(encoding="utf-8")
REPO = "https://github.com/Exios66/MapReduce-Reloaded"
CATEGORIES = ("Added", "Changed", "Deprecated", "Removed", "Fixed", "Security")
RELEASE = re.compile(r"^## \[(\d+\.\d+\.\d+)\] - (\d{4}-\d{2}-\d{2})$")


def headings(level):
    """The text of every heading at a level, in order."""
    return [
        line[level + 1 :] for line in CHANGELOG.splitlines() if line.startswith("#" * level + " ")
    ]


def versions():
    """The released versions, newest first, as (x, y, z) and date."""
    found = []
    for line in CHANGELOG.splitlines():
        if match := RELEASE.match(line):
            found.append((tuple(map(int, match[1].split("."))), date.fromisoformat(match[2])))
    return found


def test_unreleased_comes_first():
    """New entries have one place to go: the first section."""
    assert headings(2)[0] == "[Unreleased]"
    assert headings(2).count("[Unreleased]") == 1


def test_every_other_section_is_a_dated_release():
    """Each section after Unreleased is `## [x.y.z] - YYYY-MM-DD`."""
    sections = [line for line in CHANGELOG.splitlines() if line.startswith("## ")]
    for line in sections[1:]:
        assert RELEASE.match(line), line


def test_releases_are_newest_first():
    """Versions and dates both go down the file."""
    found = versions()
    assert found, "no released version"
    assert [v for v, _ in found] == sorted({v for v, _ in found}, reverse=True)
    assert [d for _, d in found] == sorted((d for _, d in found), reverse=True)


def test_subsections_are_keep_a_changelog_categories():
    """Entries sit under the six categories, each at most once per release, in order."""
    for section in re.split(r"^## ", CHANGELOG, flags=re.M)[1:]:
        names = re.findall(r"^### (.+)$", section, flags=re.M)
        assert set(names) <= set(CATEGORIES), names
        assert names == sorted(set(names), key=CATEGORIES.index), names


def test_every_version_has_a_compare_link():
    """Each heading links to its diff, and Unreleased compares the last tag with HEAD."""
    found = versions()
    newest = ".".join(map(str, found[0][0]))
    assert f"[Unreleased]: {REPO}/compare/v{newest}...HEAD" in CHANGELOG
    for (version, _), (previous, _) in zip(found, found[1:], strict=False):
        new, old = ".".join(map(str, version)), ".".join(map(str, previous))
        assert f"[{new}]: {REPO}/compare/v{old}...v{new}" in CHANGELOG
    first = ".".join(map(str, found[-1][0]))
    assert f"[{first}]: {REPO}/releases/tag/v{first}" in CHANGELOG


def test_agents_md_points_at_the_changelog():
    """The workflow every agent follows names the file."""
    assert "CHANGELOG.md" in (ROOT / "AGENTS.md").read_text(encoding="utf-8")
