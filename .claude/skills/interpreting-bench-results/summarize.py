#!/usr/bin/env python3
"""Summarizes bench/experiments.sh results for reading, not plotting.

Usage: python3 summarize.py DIR [DIR...]

Each DIR is one experiment's results directory (bench/results/E2,
bench/evaluation/E0, ...) holding runs.csv and, when present, cache.jsonl
and machine.json. For each setting, program and worker count it prints the
valid runs' median and range of the experiment's reported clock (the
coordinator's job time, `seconds`, except `wall_seconds` for E3 and E4), the
wall clock, median backups launched, total backups won across the valid
runs, bytes shuffled and the disk reads during the run. Then
it lists runs to look at (invalid, or far from their group's median), groups
whose bytes shuffled differ between valid runs (deterministic for an input),
measured settings with fewer valid runs than the most any has, and, within
each program and worker count, whether every pair of measured settings has
separate or overlapping ranges. Medians, ranges, slow runs and overlaps all
use the reported clock.

disk_read_sectors in cache.jsonl counts the whole machine's reads, so the
disk column suggests, but cannot prove, what one run read. Standard library
only; reads files and writes nothing.
"""

import csv
import json
import statistics
import sys
from itertools import combinations
from pathlib import Path

# Settings that only prepare the measured ones; the figures leave them out.
UNMEASURED = {"warmup", "calibrate"}
# Experiments that report wall_seconds: a restarted coordinator times its
# jobs from the replay, so its `seconds` misses the time before the restart.
WALL_CLOCK = {"E3", "E4"}


def load(d):
    with open(d / "runs.csv", newline="") as f:
        rows = list(csv.DictReader(f))
    reads = {}
    cache = d / "cache.jsonl"
    if cache.exists():
        phases = {}
        for line in cache.read_text().splitlines():
            if line.strip():
                r = json.loads(line)
                key = (r["setting"], r["program"], str(r["run"]))
                phases.setdefault(key, {})[r["phase"]] = r.get("disk_read_sectors")
        for key, p in phases.items():
            if p.get("start") is not None and p.get("end") is not None:
                reads[key] = (p["end"] - p["start"]) * 512 / 1e6
    machine = None
    if (d / "machine.json").exists():
        machine = json.loads((d / "machine.json").read_text())
    return rows, reads, machine


def span(values, fmt="{:.2f}"):
    if not values:
        return "-"
    med = statistics.median(values)
    return f"{fmt.format(med)} [{fmt.format(min(values))}-{fmt.format(max(values))}]"


def summarize(d):
    rows, reads, machine = load(d)
    print(f"== {d}")
    if machine:
        dirty = " DIRTY TREE" if machine.get("dirty") else ""
        print(f"commit {machine.get('commit', '?')[:7]}{dirty}, {machine.get('cpus', '?')} CPUs")
    else:
        print("no machine.json: commit unknown")
    experiments = {r.get("experiment") for r in rows} - {None, ""} or {d.name}
    col = "wall_seconds" if experiments & WALL_CLOCK else "seconds"
    print(f"clock: {col}")

    def clock(r):
        return float(r[col]) if r.get(col) else None

    groups = {}
    for r in rows:
        groups.setdefault((r["setting"], r["program"], r["workers"]), []).append(r)
    print(
        f"{'setting':24} {'program':10} {'w':>2} {'n':>3} {'valid':>5}  "
        f"{col + ' med [range]':24} {'wall med':>9} {'bk med':>7} {'won sum':>7} "
        f"{'shuffle MB':>11}  disk MB (machine-wide)"
    )
    look = []
    for (setting, program, workers), group in groups.items():
        ok = [r for r in group if r["valid"] == "1"]
        secs = [t for r in ok if (t := clock(r)) is not None]
        wall = [float(r["wall_seconds"]) for r in ok if r.get("wall_seconds")]
        backups = [int(r["backups"]) for r in ok if r.get("backups")]
        won = sum(int(r["backups_won"]) for r in ok if r.get("backups_won"))
        shuffled = {int(r["map_output_bytes"]) for r in ok if r.get("map_output_bytes")}
        disk = [
            reads[(setting, program, r["run"])] for r in ok if (setting, program, r["run"]) in reads
        ]
        if len(shuffled) > 1:
            look.append(
                f"{setting}/{program}/w{workers}: bytes shuffled differ between valid runs "
                f"({'/'.join(map(str, sorted(shuffled)))} bytes): suspect a different input"
            )
        mark = " (not measured)" if setting in UNMEASURED else ""
        print(
            f"{setting + mark:24} {program:10} {workers:>2} {len(group):>3} {len(ok):>5}  "
            f"{span(secs):24} {statistics.median(wall) if wall else 0:9.2f} "
            f"{statistics.median(backups) if backups else 0:7g} {won:7} "
            f"{'/'.join(str(round(b / 1e6)) for b in sorted(shuffled)) or '-':>11}  "
            f"{span(disk, '{:.0f}')}"
        )
        med = statistics.median(secs) if secs else None
        for r in group:
            t = clock(r)
            why = []
            if r["valid"] != "1":
                why.append(f"valid={r['valid']} state={r['state']}")
            if med and t and (t > 2 * med or t < med / 2):
                why.append(f"{t:.2f} s against a median of {med:.2f} s")
            if why:
                d_mb = reads.get((setting, program, r["run"]))
                extra = f", {d_mb:.0f} MB read machine-wide" if d_mb is not None else ""
                look.append(
                    f"{setting}/{program}/w{workers} run {r['run']}: {'; '.join(why)}{extra}"
                )
    counts = {
        key: sum(r["valid"] == "1" for r in group)
        for key, group in groups.items()
        if key[0] not in UNMEASURED
    }
    most = max(counts.values(), default=0)
    for (setting, program, workers), n in counts.items():
        if n < most:
            look.append(f"{setting}/{program}/w{workers}: {n} valid runs against {most} elsewhere")
    if look:
        print("runs to look at:")
        for line in look:
            print(f"  {line}")
    print("ranges between measured settings (same program and worker count):")
    by_cell = {}
    for (setting, program, workers), group in groups.items():
        if setting in UNMEASURED:
            continue
        secs = [t for r in group if r["valid"] == "1" and (t := clock(r)) is not None]
        if secs:
            by_cell.setdefault((program, workers), []).append((setting, min(secs), max(secs)))
    for (program, workers), cells in by_cell.items():
        for (a, alo, ahi), (b, blo, bhi) in combinations(cells, 2):
            verdict = "overlap: no clear difference" if alo <= bhi and blo <= ahi else "separate"
            print(f"  {program} w{workers}: {a} vs {b}: {verdict}")
    # E0-style data: one setting per worker count, so compare across workers.
    medians = {}
    for (setting, program, workers), group in groups.items():
        secs = [t for r in group if r["valid"] == "1" and (t := clock(r)) is not None]
        if setting not in UNMEASURED and secs:
            medians.setdefault(program, {})[int(workers)] = statistics.median(secs)
    for program, by_workers in medians.items():
        if len(by_workers) > 1 and 1 in by_workers:
            ratios = ", ".join(
                f"{w} workers {by_workers[1] / t:.2f}x"
                for w, t in sorted(by_workers.items())
                if w != 1
            )
            print(f"  {program} speed-up over 1 worker (ratio of medians): {ratios}")
    print()


def main():
    if len(sys.argv) < 2:
        sys.exit(__doc__.split("\n\n")[1])
    for arg in sys.argv[1:]:
        d = Path(arg)
        if not (d / "runs.csv").exists():
            sys.exit(f"summarize: {d}/runs.csv not found")
        summarize(d)


if __name__ == "__main__":
    main()
