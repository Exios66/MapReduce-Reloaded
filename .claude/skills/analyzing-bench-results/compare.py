#!/usr/bin/env python3
"""Compares two runs of one experiment, group by group.

Usage: python3 compare.py OLD_DIR NEW_DIR

Each directory holds one experiment's runs.csv (for example
bench/evaluation/E1 and bench/results/E1). For every setting, program and
worker count found in either, it prints the valid runs' median and range of
the coordinator's job time (`seconds`) in OLD and NEW, the change in the
median, and whether the two ranges overlap. It also compares the commits in
machine.json and the bytes shuffled, which are deterministic for a given
input. Standard library only; reads files and writes nothing.
"""

import csv
import json
import statistics
import sys
from pathlib import Path


def load(d):
    with open(d / "runs.csv", newline="") as f:
        rows = list(csv.DictReader(f))
    groups = {}
    for r in rows:
        groups.setdefault((r["setting"], r["program"], r["workers"]), []).append(r)
    machine = {}
    if (d / "machine.json").exists():
        machine = json.loads((d / "machine.json").read_text())
    return groups, machine


def stats(group):
    secs = [float(r["seconds"]) for r in group if r["valid"] == "1"]
    shuffled = {
        r["map_output_bytes"] for r in group if r["valid"] == "1" and r.get("map_output_bytes")
    }
    if not secs:
        return None
    return statistics.median(secs), min(secs), max(secs), len(secs), len(group), shuffled


def main():
    if len(sys.argv) != 3:
        sys.exit(__doc__.split("\n\n")[1])
    old_dir, new_dir = Path(sys.argv[1]), Path(sys.argv[2])
    for d in (old_dir, new_dir):
        if not (d / "runs.csv").exists():
            sys.exit(f"compare: {d}/runs.csv not found")
    old, old_m = load(old_dir)
    new, new_m = load(new_dir)
    for label, m in (("old", old_m), ("new", new_m)):
        dirty = " (dirty tree)" if m.get("dirty") else ""
        print(f"{label}: {m.get('commit', 'unknown commit')[:7]}{dirty}")
    print(
        f"{'setting':22} {'program':10} {'w':>2}  {'old med [range] n':28} "
        f"{'new med [range] n':28} {'change':>7}  ranges"
    )
    for key in sorted(set(old) | set(new)):
        a = stats(old.get(key, []))
        b = stats(new.get(key, []))

        def cell(s):
            if s is None:
                return "-"
            return f"{s[0]:.2f} [{s[1]:.2f}-{s[2]:.2f}] {s[3]}/{s[4]}"

        change = verdict = ""
        if a and b:
            change = f"{100 * (b[0] - a[0]) / a[0]:+.1f}%"
            verdict = "overlap" if a[1] <= b[2] and b[1] <= a[2] else "separate"
            if a[5] and b[5] and a[5] != b[5]:
                verdict += "; shuffle bytes differ"
        print(
            f"{key[0]:22} {key[1]:10} {key[2]:>2}  {cell(a):28} {cell(b):28} {change:>7}  {verdict}"
        )


if __name__ == "__main__":
    main()
