"""Checks that every bench chart a Markdown page shows has a dark variant (make lint).

Usage: check-dark-charts.py [FILE.md ...]   default: every tracked *.md

A chart sits in a <picture> whose <source media="(prefers-color-scheme:
dark)"> names its dark variant; that file must exist. A chart from
bench/evaluation/ shown outside a <picture>, as a bare <img> or a Markdown
image, has no dark variant and is reported. It prints one line per problem
and exits 1 if there are any.

The lint job runs this rather than the Python tests, because it reads every
Markdown page: as a test it would make every Markdown change run the Python
jobs (.circleci/mapping.txt).
"""

import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]

PICTURE = re.compile(r"<picture>.*?</picture>", re.S)
DARK = re.compile(r'<source media="\(prefers-color-scheme: dark\)" srcset="([^"]+)"')
BARE_IMG = re.compile(r'<img [^>]*src="([^"]+\.png)"')
BARE_MD = re.compile(r"!\[[^\]]*\]\(([^)]+\.png)\)")


def check(page: Path) -> tuple[list[str], int]:
    """Returns the page's problems and how many dark variants it names."""
    text = page.read_text()
    problems, checked = [], 0
    for block in PICTURE.findall(text):
        for dark in DARK.findall(block):
            checked += 1
            if not (page.parent / dark).is_file():
                problems.append(f"{page}: dark variant {dark} does not exist")
    rest = PICTURE.sub("", text)
    bare = BARE_IMG.findall(rest) + BARE_MD.findall(rest)
    for chart in bare:
        if "/evaluation/" in chart or chart.startswith("evaluation/"):
            problems.append(f"{page}: shows {chart} without a dark variant")
    return problems, checked


def tracked() -> list[Path]:
    out = subprocess.run(
        ["git", "ls-files", "-z", "*.md"],
        cwd=ROOT,
        capture_output=True,
        text=True,
        check=True,
    ).stdout
    return [ROOT / p for p in out.split("\0") if p]


def main(argv: list[str]) -> int:
    files = [Path(a).resolve() for a in argv] or tracked()
    problems, checked = [], 0
    for page in files:
        p, n = check(page)
        problems += p
        checked += n
    for p in problems:
        print(p)
    print(
        f"check-dark-charts: {len(files)} files, {checked} dark variants, {len(problems)} problems",
        file=sys.stderr,
    )
    if not argv and checked == 0:
        print(
            "check-dark-charts: no page names a dark variant; the scan found nothing",
            file=sys.stderr,
        )
        return 1
    return 1 if problems else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
