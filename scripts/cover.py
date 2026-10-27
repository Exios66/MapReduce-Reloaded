"""Checks test coverage against a floor for each package (make check, #161).

Usage:
  cover.py go PROFILE FLOORS       a `go test -coverprofile` file
  cover.py python REPORT FLOORS    a `coverage json` report

FLOORS has one package per line and its floor, a percentage of statements:
"internal/wal 85". Text after # is a comment; a floor of "-"
leaves a package unchecked (generated code, test helpers, code that other
packages' tests run). It fails if a package falls below its floor, or has
statements but no line, so a new package must be given a floor. With
--report, it also prints each package's coverage and floor. With
--write, it prints a FLOORS file for today's numbers, each one point
under, rounded down.
"""

import json
import math
import sys
from collections import defaultdict

GO_MODULE = "github.com/exios66/mapreduce-reloaded/go/"


def go_packages(path):
    """Returns {package: (statements, covered)} from a Go cover profile."""
    blocks = {}  # a block listed by several test binaries counts once
    with open(path, encoding="utf-8") as f:
        for line in f:
            if line.startswith("mode:") or not line.strip():
                continue
            loc, n, count = line.rsplit(" ", 2)
            prev = blocks.get(loc, (0, 0))
            blocks[loc] = (int(n), max(prev[1], int(count)))
    pkgs = defaultdict(lambda: [0, 0])
    for loc, (n, count) in blocks.items():
        pkg = loc.split(":", 1)[0].rsplit("/", 1)[0].removeprefix(GO_MODULE)
        pkgs[pkg][0] += n
        pkgs[pkg][1] += n if count > 0 else 0
    return pkgs


def python_packages(path):
    """Returns {package: (statements, covered)} from a coverage JSON report."""
    with open(path, encoding="utf-8") as f:
        report = json.load(f)
    pkgs = defaultdict(lambda: [0, 0])
    for name, data in report["files"].items():
        pkg = name.rsplit("/", 1)[0] if "/" in name else "."
        summary = data["summary"]
        pkgs[pkg][0] += summary["num_statements"]
        pkgs[pkg][1] += summary["covered_lines"]
    return pkgs


def read_floors(path):
    floors = {}
    with open(path, encoding="utf-8") as f:
        for line in f:
            line = line.split("#", 1)[0].strip()
            if line:
                pkg, floor = line.split()
                floors[pkg] = None if floor == "-" else float(floor)
    return floors


def main():
    args = [a for a in sys.argv[1:] if not a.startswith("--")]
    lang, report, floors_path = args
    pkgs = (go_packages if lang == "go" else python_packages)(report)
    pct = {p: 100 * c / n for p, (n, c) in pkgs.items() if n}
    if "--write" in sys.argv:
        for p in sorted(pct):
            print(f"{p} {max(0, math.floor(pct[p] - 1))}")
        return
    floors = read_floors(floors_path)
    if "--report" in sys.argv:
        for p in sorted(pct):
            floor = floors.get(p, "none")
            print(f"{pct[p]:5.1f}%  floor {'-' if floor is None else floor}  {p}")
    failed = []
    for p in sorted(pct):
        if p not in floors:
            failed.append(f"{p}: {pct[p]:.1f}%, and no floor in {floors_path}")
        elif floors[p] is not None and pct[p] < floors[p]:
            failed.append(f"{p}: {pct[p]:.1f}%, under its floor of {floors[p]:g}%")
    checked = [pkgs[p] for p in pct if floors.get(p) is not None]
    n = sum(s for s, _ in checked)
    total = 100 * sum(c for _, c in checked) / n if n else 100
    print(f"cover: {lang} {total:.1f}% of {n} statements in {len(checked)} checked packages")
    if failed:
        sys.exit("cover: " + "\ncover: ".join(failed))


if __name__ == "__main__":
    main()
