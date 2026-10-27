"""Draws the plots of the performance evaluation (docs/design.md) from the
results of bench/experiments.sh, and prints a Markdown table of each
experiment's job times for bench/README.md.

For each of E0 to E4 whose bench/results/EXPERIMENT/runs.csv
exists, this writes OUT_DIR/EXPERIMENT.png. Every point is the median of
the valid runs, with the range as an error bar; runs whose output differed
from the normal run's (valid=0) are left out and counted in the table.

- E0: job time against workers, one line per program, with ideal linear
  speed-up from one worker dashed.
- E1: bytes shuffled and job time per Zipf exponent, combiner on and off,
  as grouped bars.
- E2: tasks completed against time for the median run of each setting, as
  in the paper's Figure 3, and job-time bars.
- E3: input, shuffle and output rates against time for the median run that
  killed a worker at 50%, with the kill marked, as Figure 3(c); and job
  time against kill time, with the no-failure time as a line.
- E4: tasks completed against time with the restart marked, and job-time
  bars with and without the restart.

E0, E1 and E2 use the coordinator's job time (seconds); E3 and E4 use the time
from submission until mr status saw the job finish (wall_seconds), since a
restarted coordinator measures its jobs from when it replays them.

Usage: python3 bench/plot-experiments.py [RESULTS_DIR [OUT_DIR]]
(defaults: bench/results, and the same directory). Needs matplotlib.
"""

import csv
import statistics
import subprocess
import sys
from pathlib import Path

import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt  # noqa: E402

BENCH = Path(__file__).resolve().parent
PROGRAMS = {"grep": "grep", "distsort": "sort", "wordcount": "Zipf word count"}


def load(results, exp):
    path = results / exp / "runs.csv"
    if not path.exists():
        return None
    with open(path, newline="") as f:
        return list(csv.DictReader(f))


def valid(rows, **match):
    return [r for r in rows if r["valid"] == "1" and all(r[k] == v for k, v in match.items())]


def spread(rows, field):
    """Returns (median, low, high) of a field over rows, or None."""
    values = sorted(float(r[field]) for r in rows)
    if not values:
        return None
    return statistics.median(values), values[0], values[-1]


def median_run(rows, field):
    """Returns the row whose field is the (lower) median."""
    ordered = sorted(rows, key=lambda r: float(r[field]))
    return ordered[(len(ordered) - 1) // 2] if ordered else None


def completed(path, job):
    """Returns (seconds, tasks completed) for a job from a scrape-tasks.py file."""
    by_time = {}
    with open(path, newline="") as f:
        for row in csv.DictReader(f):
            if row["job"] == job and row["state"] == "completed":
                t = float(row["seconds"])
                by_time[t] = by_time.get(t, 0) + int(row["value"])
    times = sorted(by_time)
    return times, [by_time[t] for t in times]


def first_seen(path, job):
    """Seconds into a scrape-tasks.py file at which the job first appears,
    so its curves start at its submission."""
    with open(path, newline="") as f:
        return next((float(r["seconds"]) for r in csv.DictReader(f) if r["job"] == job), 0.0)


def failure_interval(path, workers):
    """Sampling times bracketing the first observed loss of a worker."""
    rounds = {}
    with open(path, newline="") as stream:
        for row in csv.DictReader(stream):
            rounds.setdefault(float(row["seconds"]), set()).add(row["url"])
    previous = None
    for seconds, urls in sorted(rounds.items()):
        if len(urls) < workers:
            if previous is not None:
                return previous, seconds
        else:
            previous = seconds
    return None


def errorbar(ax, xs, stats, **kwargs):
    med = [s[0] for s in stats]
    lo = [s[0] - s[1] for s in stats]
    hi = [s[2] - s[0] for s in stats]
    return ax.errorbar(xs, med, yerr=[lo, hi], capsize=3, marker="o", **kwargs)


def bars(ax, labels, stats, ylabel):
    xs = range(len(labels))
    ax.bar(
        xs,
        [s[0] for s in stats],
        yerr=[[s[0] - s[1] for s in stats], [s[2] - s[0] for s in stats]],
        capsize=4,
        color="#4c72b0",
    )
    ax.set_xticks(list(xs), labels, rotation=15)
    ax.set_ylabel(ylabel)
    ax.set_ylim(bottom=0)


def caption(rows):
    r = rows[0]
    return f"input {int(r['input_bytes']) / 2**30:g} GiB, M = {r['m']}, R = {r['r']}"


def e0(rows, out):
    fig, ax = plt.subplots(figsize=(6, 4))
    for program, name in PROGRAMS.items():
        workers = sorted({int(r["workers"]) for r in valid(rows, program=program)})
        stats = [spread(valid(rows, program=program, workers=str(w)), "seconds") for w in workers]
        if not workers:
            continue
        line = errorbar(ax, workers, stats, label=name)
        if workers[0] == 1:
            ax.plot(
                workers,
                [stats[0][0] / w for w in workers],
                "--",
                color=line[0].get_color(),
                alpha=0.6,
            )
    ax.set_xticks([1, 2, 4])
    ax.set_xlabel("Workers (1 CPU each)")
    ax.set_ylabel("Job time (s)")
    ax.set_ylim(bottom=0)
    ax.set_title(f"E0: scaling over workers\n({caption(rows)}; dashed: ideal)")
    ax.legend()
    fig.tight_layout()
    fig.savefig(out)


def e1(rows, out):
    """Saves E1 combiner comparisons from runs.csv rows to out.

    Rows must be nonempty, with settings such as "s1.0-on" and "s1.0-off".
    Only valid="1" runs contribute to median bars and minimum/maximum error
    bars; groups without valid runs have no bar. Panels show map output in
    decimal MB and coordinator job time in seconds.
    Missing fields and invalid numbers propagate KeyError and ValueError;
    errors saving the figure also propagate.
    """
    exponents = sorted({r["setting"].rsplit("-", 1)[0] for r in rows}, key=lambda e: float(e[1:]))
    fig, axes = plt.subplots(1, 2, figsize=(10, 4))
    panels = (("map_output_bytes", 1e6, "Bytes shuffled (MB)"), ("seconds", 1, "Job time (s)"))
    for ax, (field, scale, ylabel) in zip(axes, panels, strict=True):
        for i, (combiner, label) in enumerate((("on", "combiner on"), ("off", "combiner off"))):
            stats = [spread(valid(rows, setting=f"{e}-{combiner}"), field) for e in exponents]
            xs = [j + (i - 0.5) * 0.4 for j in range(len(exponents))]
            kept = [(x, st) for x, st in zip(xs, stats, strict=True) if st]
            ax.bar(
                [x for x, _ in kept],
                [st[0] / scale for _, st in kept],
                width=0.4,
                yerr=[
                    [(st[0] - st[1]) / scale for _, st in kept],
                    [(st[2] - st[0]) / scale for _, st in kept],
                ],
                capsize=3,
                label=label,
            )
        ax.set_xticks(range(len(exponents)), [f"S = {e[1:]}" for e in exponents])
        ax.set_xlabel("Zipf exponent (higher: more skew)")
        ax.set_ylabel(ylabel)
        ax.set_ylim(bottom=0)
        ax.legend()
    fig.suptitle(f"E1: what the combiner saves in word count ({caption(rows)})")
    fig.tight_layout()
    fig.savefig(out)


def timeline_and_bars(rows, results, exp, settings, field, title, event_label=None):
    fig, (left, right) = plt.subplots(1, 2, figsize=(11, 4), gridspec_kw={"width_ratios": [2, 1]})
    labels, stats = [], []
    for setting, label in settings.items():
        runs = valid(rows, setting=setting)
        if not runs:
            continue
        run = median_run(runs, field)
        path = results / exp / f"tasks-{setting}-{run['run']}.csv"
        if path.exists():
            t0 = first_seen(path, run["job"])
            times, done = completed(path, run["job"])
            left.step([t - t0 for t in times], done, where="post", label=label)
        if event_label and run["event_at"]:
            left.axvline(float(run["event_at"]), color="red", linestyle=":", label=event_label)
        labels.append(label)
        stats.append(spread(runs, field))
    left.set_xlabel("Seconds since submission")
    left.set_ylabel("Tasks completed (map + reduce)")
    left.legend()
    bars(right, labels, stats, "Job time (s)")
    fig.suptitle(f"{title} ({caption(rows)})")
    fig.tight_layout()
    return fig


def e2(rows, results, out):
    settings = {
        "normal": "no straggler",
        "straggler-backups": "straggler, backups on",
        "straggler-no-backups": "straggler, backups off",
    }
    fig = timeline_and_bars(
        rows, results, "E2", settings, "seconds", "E2: a worker throttled to 0.2 CPU"
    )
    fig.savefig(out)


def e3(rows, results, out):
    fig, (left, right) = plt.subplots(1, 2, figsize=(11, 4))
    normal = spread(valid(rows, setting="normal"), "wall_seconds")
    pcts = [p for p in (25, 50, 75) if valid(rows, setting=f"kill-{p}")]
    stats = [spread(valid(rows, setting=f"kill-{p}"), "wall_seconds") for p in pcts]
    errorbar(left, pcts, stats, label="one worker killed")
    if normal:
        left.axhline(normal[0], color="gray", linestyle="--", label="no failure")
    left.set_xticks(pcts, [f"{p}%" for p in pcts])
    left.set_xlabel("Kill time, as a share of the normal job time")
    left.set_ylabel("Job time (s)")
    left.set_ylim(bottom=0)
    left.legend()
    run = median_run(valid(rows, setting="kill-50"), "wall_seconds")
    path = run and results / "E3" / f"samples-kill-50-{run['run']}.csv"
    if path and path.exists():
        rates = list(
            csv.DictReader(
                subprocess.run(
                    [sys.executable, BENCH / "plot-rates.py", path],
                    capture_output=True,
                    check=True,
                    text=True,
                ).stdout.splitlines()
            )
        )
        times = [float(r["seconds"]) for r in rates]
        for rate, color in (("input", "#4c72b0"), ("shuffle", "#dd8452"), ("output", "#55a868")):
            right.plot(times, [float(r[rate]) for r in rates], label=rate, color=color)
        # event_at is relative to submission, which starts after sampling.
        # Historical runs lack that offset; mark the observed loss instead.
        interval = failure_interval(path, int(run["workers"]))
        if interval:
            right.axvspan(*interval, color="red", alpha=0.2, label="worker loss observed")
        right.set_xlabel("Seconds since sampling began")
        right.set_ylabel("MB/s")
        right.set_ylim(bottom=0)
        right.legend()
        right.set_title("Data rates, killed at 50%")
    fig.suptitle(f"E3: losing a worker during distsort ({caption(rows)})")
    fig.tight_layout()
    fig.savefig(out)


def e4(rows, results, out):
    settings = {"normal": "no restart", "restart": "coordinator restarted"}
    fig = timeline_and_bars(
        rows,
        results,
        "E4",
        settings,
        "wall_seconds",
        "E4: a coordinator restart with the log",
        "restart",
    )
    fig.savefig(out)


def table(exp, rows):
    """Prints a Markdown job-time table grouped by setting, program, and workers.

    Uses coordinator seconds for E0, E1, and E2 and wall_seconds otherwise.
    Each group shows the median and range of valid="1" runs and their count
    out of all runs; groups without valid runs show an em dash for time.
    Missing fields, invalid numbers, and output errors propagate.
    """
    field = "seconds" if exp in ("E0", "E1", "E2") else "wall_seconds"
    print(f"\n{exp}\n")
    print("| Setting | Program | Workers | Job time (s) | Valid runs |")
    print("|---|---|---:|---|---:|")
    seen = []
    for r in rows:
        key = (r["setting"], r["program"], r["workers"])
        if key not in seen:
            seen.append(key)
    for setting, program, workers in seen:
        all_runs = [
            r
            for r in rows
            if (r["setting"], r["program"], r["workers"]) == (setting, program, workers)
        ]
        s = spread(valid(all_runs), field)
        t = f"{s[0]:.2f} ({s[1]:.2f}–{s[2]:.2f})" if s else "—"
        n = f"{len(valid(all_runs))} of {len(all_runs)}"
        print(f"| {setting} | {program} | {workers} | {t} | {n} |")


def main():
    """Reads optional results/output paths from sys.argv, plots, and prints tables.

    Results default to bench/results; output defaults to the results path.
    Creates the output directory, writes an EXPERIMENT.png for each nonempty
    E0 to E4 runs.csv, replacing existing plots, and prints tables to stdout.
    Missing or empty CSVs are skipped. Filesystem, CSV parsing, plotting,
    and table errors propagate.
    """
    results = Path(sys.argv[1]) if len(sys.argv) > 1 else BENCH / "results"
    out = Path(sys.argv[2]) if len(sys.argv) > 2 else results
    out.mkdir(parents=True, exist_ok=True)
    draw = {
        "E0": lambda rows: e0(rows, out / "E0.png"),
        "E1": lambda rows: e1(rows, out / "E1.png"),
        "E2": lambda rows: e2(rows, results, out / "E2.png"),
        "E3": lambda rows: e3(rows, results, out / "E3.png"),
        "E4": lambda rows: e4(rows, results, out / "E4.png"),
    }
    for exp, fn in draw.items():
        rows = load(results, exp)
        if rows:
            fn(rows)
            table(exp, rows)


main()
