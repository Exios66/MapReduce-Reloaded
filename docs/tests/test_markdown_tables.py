"""Every Markdown table uses the compact style that .markdownlint.jsonc sets
(markdownlint's MD060), which pymarkdown in make md-lint does not check: one
space inside each pipe, an empty cell as a single space, and delimiter cells
written ---, :---, ---: or :---:. Editors running markdownlint then show no
table warnings."""

import re
import subprocess
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]
PIPE = re.compile(r"(?<!\\)\|")  # GFM splits cells on every unescaped pipe
FENCE = re.compile(r"\s*(```|~~~)")


def compact(line):
    """The table row in compact style, keeping its indentation."""
    indent = line[: len(line) - len(line.lstrip())]
    body = line.strip()
    cells = PIPE.split(body)
    if body.startswith("|"):
        cells = cells[1:]
    if body.endswith("|") and not body.endswith("\\|"):
        cells = cells[:-1]
    cells = [cell.strip() for cell in cells]
    if all(re.fullmatch(r":?-+:?", cell) for cell in cells):
        cells = [
            (":" if c.startswith(":") else "")
            + "---"
            + (":" if len(c) > 1 and c.endswith(":") else "")
            for c in cells
        ]
    return indent + "|" + "|".join(f" {cell} " if cell else " " for cell in cells) + "|"


def table_rows(text):
    """Each table row outside fenced code, with its line number."""
    fenced = False
    for number, line in enumerate(text.splitlines(), 1):
        if FENCE.match(line):
            fenced = not fenced
        elif not fenced and line.lstrip().startswith("|"):
            yield number, line


def markdown_files():
    tracked = subprocess.run(
        ["git", "ls-files", "-z", "--", "*.md"],
        cwd=ROOT,
        capture_output=True,
        text=True,
        check=False,
    )
    if tracked.returncode != 0:
        pytest.skip("not a git checkout")
    return [name for name in tracked.stdout.split("\0") if name]


def test_compact_rewrites_a_row():
    assert compact("|a|`b`|") == "| a | `b` |"
    assert compact("  |---|:--|--:|:-:|") == "  | --- | :--- | ---: | :---: |"
    assert compact("| x |  | y \\| z |") == "| x | | y \\| z |"


def test_every_table_is_compact():
    wrong = [
        f"{name}:{number}: {line.strip()}"
        for name in markdown_files()
        for number, line in table_rows((ROOT / name).read_text())
        if line != compact(line)
    ]
    assert not wrong, "tables not in compact style (one space inside each pipe):\n" + "\n".join(
        wrong
    )
