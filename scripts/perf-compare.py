"""Compares two `go test -bench` outputs by median time per op (make perf).

Usage: perf-compare.py BASE NEW THRESHOLD_PERCENT

Exits 1 if a benchmark in both files is more than THRESHOLD percent slower
in NEW, unless the files come from different CPUs, when the numbers are
printed but cannot be compared.
"""

import re
import statistics
import sys

LINE = re.compile(r"^(Benchmark\S+?)(?:-\d+)?\s+\d+\s+([\d.]+) ns/op")


def read(path):
    times, cpu, pkg = {}, None, ""
    with open(path, encoding="utf-8") as f:
        for line in f:
            if line.startswith("cpu:"):
                cpu = line.split(":", 1)[1].strip()
            elif line.startswith("pkg:"):
                pkg = line.split(":", 1)[1].strip().rsplit("/", 1)[-1]
            elif m := LINE.match(line):
                times.setdefault(f"{pkg}.{m[1]}", []).append(float(m[2]))
    return {k: statistics.median(v) for k, v in times.items()}, cpu


def main():
    base_path, new_path, threshold = sys.argv[1], sys.argv[2], float(sys.argv[3])
    base, base_cpu = read(base_path)
    new, new_cpu = read(new_path)
    if not new:
        sys.exit("perf: no benchmark results in this run")
    slower = []
    for name in sorted(new):
        if name not in base:
            print(f"{name}: {new[name]:.0f} ns/op, not in the baseline")
            continue
        change = 100 * (new[name] / base[name] - 1)
        mark = ""
        if change > threshold:
            mark = f"  SLOWER than {threshold:g}%"
            slower.append(name)
        print(
            f"{name}: {base[name]:.0f} -> {new[name]:.0f} ns/op ({change:+.1f}%){mark}"
        )
    if base_cpu != new_cpu:
        print(
            f"perf: the baseline is from {base_cpu!r}, this run {new_cpu!r}; not comparable"
        )
        return
    if slower:
        sys.exit(
            f"perf: {len(slower)} benchmarks more than {threshold:g}% slower than bench/baseline.txt"
        )
    print(f"perf: no benchmark more than {threshold:g}% slower")


if __name__ == "__main__":
    main()
