"""Draws the plots of the performance evaluation (docs/design.md) from the
results of bench/experiments.sh, and prints a Markdown table of each
experiment's job times for bench/README.md.

For each of E0 to E7 whose bench/results/EXPERIMENT/runs.csv
exists, this writes OUT_DIR/EXPERIMENT.png on a light background and
OUT_DIR/EXPERIMENT-dark.png on a dark one. Each title states the finding,
computed from the data. Points are medians of the valid runs, drawn with
each run as a ring or with the range as a bar, as each subtitle says; runs
whose output differed from the undisturbed run's (valid=0) are left out and
counted in the table.

- E0: speed-up over one worker against workers, one line per program, each
  run a ring, with ideal linear speed-up dashed and a far outlier named.
- E1: bytes shuffled (log axis) and job time per Zipf exponent as
  dumbbells joining the combiner-on and combiner-off medians, each labeled
  with the saving.
- E2: tasks completed against time for the median run of each setting, as
  in the paper's Figure 3, and job-time bars in the same colors: color marks
  backups on or off; dashes, and a gap between the bars, the runs without a
  straggler.
- E2-comparison: the same two panels, but of two datasets: the two
  no-straggler settings come from the ten paired runs in
  RESULTS_DIR/E2-pairs (the drift-controlled comparison), the two straggler
  settings from the main runs in RESULTS_DIR/E2 (the only straggler data).
  The image says so in its subtitle. It is drawn, as E2-comparison.png and
  E2-comparison-dark.png, only when both directories have a runs.csv.
- E3: job time at each kill point as a stem from the no-failure median,
  labeled with its cost; and input, shuffle and output rates against time
  for the median run killed at 50%, with the loss marked, as Figure 3(c).
- E4: tasks completed against time with the restart marked, and job-time
  bars with and without the restart.
- E5: job time and map-phase time for the compiled program and the two job
  files the workers interpret.
- E6: bytes shuffled and job time per input (crawled English, crawled pages
  in every language, and the book), combiner on and off, as E1 does.
- faults.png (and faults-dark.png), when E2, E3 and E4 all have results:
  the job time each disturbance adds to its experiment's undisturbed
  median, one bar per disturbance on one axis.

E0, E1, E2 and E6 use the coordinator's job time (seconds); E3 and E4 use
the time from submission until mr status saw the job finish
(wall_seconds), since a restarted coordinator measures its jobs from when
it replays them.

Usage: python3 bench/plot-experiments.py [RESULTS_DIR [OUT_DIR]]
(defaults: bench/results, and the same directory). Needs matplotlib.
"""

import csv
import json
import logging
import statistics
import subprocess
import sys
from pathlib import Path

import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt  # noqa: E402

# The font list falls back past families a machine lacks; say nothing for each one.
logging.getLogger("matplotlib.font_manager").setLevel(logging.ERROR)


def use_bundled_fonts():
    """Registers bench/fonts (Inter, SIL Open Font License), so every
    machine sets the figures in the same face."""
    try:
        from matplotlib import font_manager
    except ImportError:  # matplotlib replaced, as in the tests
        return
    for path in sorted((Path(__file__).resolve().parent / "fonts").glob("*.ttf")):
        font_manager.fontManager.addfont(str(path))


BENCH = Path(__file__).resolve().parent
PROGRAMS = {"grep": "grep", "distsort": "sort", "wordcount": "Zipf word count"}


# An experiment rerun to replace its data: E0-isolated reran E0 with only the
# running program's input in the page cache, so its runs are E0's (#347).
REPLACED_BY = {"E0": "E0-isolated"}


def source(results, exp):
    """Returns the directory under results that holds exp's data: its
    replacement in REPLACED_BY when that has a runs.csv, else results/exp."""
    other = REPLACED_BY.get(exp)
    if other and (results / other / "runs.csv").exists():
        return results / other
    return results / exp


def load(results, exp):
    """Returns the rows of exp's runs.csv as dicts, read from source(), or
    None if the file does not exist."""
    path = source(results, exp) / "runs.csv"
    if not path.exists():
        return None
    with open(path, newline="") as f:
        return list(csv.DictReader(f))


def disk_reads(results, exp):
    """Returns the bytes the whole machine read from disk during each run,
    keyed by (setting, program, run), from exp's cache.jsonl in source(),
    or {} if the file does not exist. A run without both its start and end rows,
    or with a null counter, is left out. The counter counts every process's
    reads, so a large value suggests, but cannot prove, that the run read
    its own input from disk."""
    path = source(results, exp) / "cache.jsonl"
    if not path.exists():
        return {}
    phases = {}
    with open(path) as f:
        for line in f:
            if line.strip():
                r = json.loads(line)
                key = (r["setting"], r["program"], str(r["run"]))
                phases.setdefault(key, {})[r["phase"]] = r["disk_read_sectors"]
    return {
        key: (p["end"] - p["start"]) * 512
        for key, p in phases.items()
        if p.get("start") is not None and p.get("end") is not None
    }


def likely_cold(rows, reads):
    """The (setting, program, run) keys of the rows during which the machine
    read at least a quarter of the input more from disk than the median run
    of the same program and worker count. The counter covers the whole VM,
    so this marks a measurement, not a cause: a cold input is one, and
    sort's re-reads under the worker memory limit are another, which
    comparing within a worker count leaves out (#347). Rows without a
    reading are never marked."""
    groups = {}
    for r in rows:
        key = (r["setting"], r["program"], r.get("run"))
        if key in reads:
            groups.setdefault((r["program"], r["workers"]), []).append((key, r))
    cold = set()
    for members in groups.values():
        typical = statistics.median(reads[key] for key, _ in members)
        for key, r in members:
            if reads[key] - typical >= int(r["input_bytes"]) / 4:
                cold.add(key)
    return cold


def valid(rows, **match):
    """The rows whose output matched the undisturbed run's (valid == "1")
    and whose fields equal every keyword given, e.g. setting="normal"."""
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


# Chart themes: the reference palette of the dataviz method, validated for
# three series (scripts/validate_palette.js, all pairs, both modes). Each
# chart is drawn twice, EXPERIMENT.png on a light surface and
# EXPERIMENT-dark.png on a dark one, and the READMEs pick by the reader's
# theme. A series keeps its slot in every panel of a figure.
THEMES = {
    "light": {
        "surface": "#fcfcfb",
        "ink": "#0b0b0b",
        "ink2": "#52514e",
        "muted": "#898781",
        "grid": "#e1e0d9",
        "axis": "#c3c2b7",
        "series": ("#2a78d6", "#eb6834", "#1baf7a"),
    },
    "dark": {
        "surface": "#1a1a19",
        "ink": "#f0efec",
        "ink2": "#c3c2b7",
        "muted": "#898781",
        "grid": "#2c2c2a",
        "axis": "#383835",
        "series": ("#3987e5", "#d95926", "#199e70"),
    },
}


def rc(t):
    """Matplotlib settings for theme t: hairline recessive axes and grid,
    no box, 2px lines, text in ink tokens, never in a series color."""
    return {
        "figure.facecolor": t["surface"],
        "axes.facecolor": t["surface"],
        "savefig.facecolor": t["surface"],
        "axes.edgecolor": t["axis"],
        "axes.linewidth": 1,
        "axes.spines.top": False,
        "axes.spines.right": False,
        "axes.labelcolor": t["ink2"],
        "axes.titlecolor": t["ink"],
        "axes.titlesize": 11,
        "axes.titlelocation": "left",
        "axes.titlepad": 10,
        "axes.grid": True,
        "axes.axisbelow": True,
        "grid.color": t["grid"],
        "grid.linewidth": 1,
        "xtick.color": t["axis"],
        "ytick.color": t["axis"],
        "xtick.labelcolor": t["ink2"],
        "ytick.labelcolor": t["ink2"],
        "lines.linewidth": 2,
        "lines.solid_capstyle": "round",
        "lines.solid_joinstyle": "round",
        "legend.frameon": False,
        "legend.labelcolor": t["ink2"],
        "text.color": t["ink"],
        # Inter, from bench/fonts: one face whichever machine draws them.
        "font.family": ["Inter", "DejaVu Sans"],
        "font.size": 10,
        "savefig.dpi": 150,
    }


def heading(fig, t, title, subtitle):
    """A left-aligned title and a subtitle in secondary ink, which may run
    to two lines."""
    fig.text(
        0.012,
        0.975,
        title,
        ha="left",
        va="top",
        fontsize=13,
        weight="semibold",
        color=t["ink"],
    )
    fig.text(
        0.012,
        0.905,
        subtitle,
        ha="left",
        va="top",
        fontsize=9.5,
        color=t["ink2"],
        linespacing=1.5,
    )


def finish(fig, out, top=0.84):
    """Lays the axes out below the heading, which takes the top 1 - top of
    the figure, saves the figure to out, and closes it."""
    fig.tight_layout(rect=(0, 0, 1, top))
    fig.savefig(out)
    plt.close(fig)


def dots(ax, t, xs, ys, color):
    """End markers: 8px, filled with the series color, with a 2px ring in
    the surface color."""
    ax.plot(
        xs,
        ys,
        "o",
        markersize=8,
        color=color,
        markeredgecolor=t["surface"],
        markeredgewidth=2,
        zorder=4,
    )


def runs(ax, t, xs, ys, color):
    """Single runs behind a median: small hollow markers in the series
    color, so a reader sees each run (n is three) instead of a whisker that
    one cold run can stretch."""
    ax.plot(
        xs,
        ys,
        "o",
        markersize=5,
        markerfacecolor=t["surface"],
        markeredgecolor=color,
        markeredgewidth=1.2,
        linestyle="none",
        zorder=2,
    )


def span(values, fmt):
    """'lo–hi' with fmt applied to each end, or one value when they agree
    after formatting."""
    lo, hi = fmt(min(values)), fmt(max(values))
    return lo if lo == hi else f"{lo}–{hi}"


def ranges(ax, t, xs, med, lo, hi, horizontal=False):
    """Thin range bars (lowest to highest run) in secondary ink."""
    err = [
        [m - a for m, a in zip(med, lo, strict=True)],
        [b - m for m, b in zip(med, hi, strict=True)],
    ]
    args = {
        "fmt": "none",
        "ecolor": t["ink2"],
        "elinewidth": 1,
        "capsize": 3,
        "zorder": 3,
    }
    if horizontal:
        ax.errorbar(med, xs, xerr=err, **args)
    else:
        ax.errorbar(xs, med, yerr=err, **args)


def label_note(rows, ranges=True):
    """The run's setup and how to read the marks: "Input 1 GiB, M = 32,
    R = 4; medians of the valid runs", and ", whiskers span the range"
    when the figure draws range whiskers."""
    r = rows[0]
    return (
        f"Input {int(r['input_bytes']) / 2**30:g} GiB, M = {r['m']}, R = {r['r']}; "
        "medians of the valid runs" + (", whiskers span the range" if ranges else "")
    )


RING_SHIFT = 0.07  # workers: E0 draws each run's ring this far right of its median


def e0(rows, out, t, reads=None):
    """Saves E0's speed-up against worker count to out, in theme t.

    Speed-up is the median one-worker job time over the median job time on
    N workers, with the range from the slowest and fastest N-worker runs.
    Only valid="1" runs count; a program without a valid one-worker run, or
    with a time of zero, is skipped. Missing fields, invalid numbers, and
    file errors propagate. Warm-up runs (setting="warmup") are left out.
    reads, from disk_reads, marks each likely_cold run with a cross instead
    of a ring; the typical read it is compared with is that of the valid
    runs.
    """
    rows = [r for r in rows if r["setting"] != "warmup"]
    cold = likely_cold(valid(rows), reads or {})
    fig, ax = plt.subplots(figsize=(7.5, 4.8))
    ax.plot([1, 4], [1, 4], "--", color=t["muted"], linewidth=1, zorder=1)
    ax.text(
        2.55,
        2.75,
        "ideal (linear)",
        color=t["ink2"],
        rotation=33,
        fontsize=9,
        ha="center",
    )
    best, shortest = (
        [],
        None,
    )  # (speed-up, name, workers) per program; (one-worker time, name)
    for (program, name), color in zip(PROGRAMS.items(), t["series"], strict=True):
        workers = sorted({int(r["workers"]) for r in valid(rows, program=program)})
        if not workers or workers[0] != 1:
            continue
        stats = [spread(valid(rows, program=program, workers=str(w)), "seconds") for w in workers]
        if any(st[1] <= 0 for st in stats):  # a time that rounds to zero has no ratio
            continue
        one = stats[0][0]
        speedup = [one / s[0] for s in stats]
        ax.plot(workers, speedup, color=color, label=name, zorder=3)
        for w, median in zip(workers, speedup, strict=True):
            group = valid(rows, program=program, workers=str(w))
            times = [float(r["seconds"]) for r in group]
            each = [one / x for x in times]
            marked = [(r["setting"], r["program"], r.get("run")) in cold for r in group]
            warm = [y for y, m in zip(each, marked, strict=True) if not m]
            # Just right of the median, so no ring hides behind its dot.
            runs(ax, t, [w + RING_SHIFT] * len(warm), warm, color)
            crossed = [y for y, m in zip(each, marked, strict=True) if m]
            if crossed:
                ax.plot(
                    [w + RING_SHIFT] * len(crossed),
                    crossed,
                    "x",
                    markersize=6,
                    markeredgecolor=color,
                    markeredgewidth=1.4,
                    linestyle="none",
                    zorder=2,
                )
            # Name the runs far below their median, so they are not read as
            # the trend; those at one worker count share one label.
            far = [(x, y) for x, y in zip(times, each, strict=True) if y < median / 2]
            if far:
                label = f"one {name} run: {far[0][0]:.1f} s"
                if len(far) > 1:
                    label = (
                        f"{len(far)} {name} runs: {span([x for x, _ in far], '{:.1f}'.format)} s"
                    )
                ax.annotate(
                    label,
                    (w + RING_SHIFT, min(y for _, y in far)),
                    xytext=(8, 0),
                    textcoords="offset points",
                    va="center",
                    color=t["ink2"],
                    fontsize=9,
                )
        dots(ax, t, workers, speedup, color)
        best.append((speedup[-1], name, workers[-1]))
        shortest = min(shortest or (float("inf"), ""), (one, name))
        ax.annotate(
            f"{name}  {speedup[-1]:.1f}×",
            (workers[-1] + RING_SHIFT, speedup[-1]),
            xytext=(10, 0),
            textcoords="offset points",
            va="center",
            color=t["ink2"],
        )
    ax.set_xticks([1, 2, 4])
    ax.set_xlim(0.8, 5.6)
    ax.set_ylim(0, 4.4)
    ax.set_xlabel("Workers, one CPU each")
    ax.set_ylabel("Speed-up over one worker (×)")
    ax.grid(axis="x", visible=False)
    # One row above the plot, clear of the lines and of the rings below them.
    ax.legend(loc="lower left", bbox_to_anchor=(0, 1.0), ncols=3, borderaxespad=0.2)
    title = "E0: scaling over workers"
    if best:
        top = max(w for *_, w in best)
        each = sorted(((s, n) for s, n, w in best if w == top), reverse=True)
        title = f"E0: {top} workers speed up " + ", ".join(f"{n} {s:.1f}×" for s, n in each)
    note = "Speed-up = job time on 1 worker ÷ job time on N; lines join medians, rings are runs."
    if cold:
        note += (
            "\nCrosses: runs that read ¼ of the input more from disk than the"
            " median run of their program and worker count (VM-wide counter)."
        )
    if shortest and shortest[0] < 5:
        note += (
            f"\n{shortest[1].capitalize()} takes {shortest[0]:.1f} s on one worker,"
            " so fixed per-job costs cap its speed-up."
        )
    heading(fig, t, title, note + "\n" + label_note(rows, ranges=False) + ".")
    # The subtitle has the note's lines and the setup line; the legend row
    # sits just below it.
    finish(fig, out, top=0.92 - 0.045 * (note.count("\n") + 2))


def dumbbell(ax, t, labels, stats_on, stats_off, runs_on, runs_off, scale, log=False):
    """One row per group, the first on top: the combiner-on median (slot 1)
    and the combiner-off median (slot 2), joined by a hairline, each run a
    hollow ring behind its median, and each median labeled outside the pair.

    stats_on and stats_off align with labels and hold (median, low, high)
    triples, or None to omit that end; runs_on and runs_off hold each
    group's run values. Values are divided by scale. On a log axis, where
    equal ratios have equal lengths, values of zero or less cannot be
    placed and are omitted. Returns the rows' y positions.
    """
    ys = list(range(len(labels)))[::-1]
    for stats, each, color, label in (
        (stats_on, runs_on, t["series"][0], "combiner on"),
        (stats_off, runs_off, t["series"][1], "combiner off"),
    ):
        kept = [(y, st) for y, st in zip(ys, stats, strict=True) if st and (st[0] > 0 or not log)]
        yk, med = [y for y, _ in kept], [st[0] / scale for _, st in kept]
        for y, values in zip(ys, each, strict=True):
            values = [v / scale for v in values if v > 0 or not log]
            # On a strip just below the row, so no ring hides behind its median.
            runs(ax, t, values, [y - 0.22] * len(values), color)
        ax.plot(
            med,
            yk,
            "o",
            markersize=9,
            color=color,
            markeredgecolor=t["surface"],
            markeredgewidth=2,
            linestyle="none",
            label=label,
            zorder=4,
        )
    for y, a, b in zip(ys, stats_on, stats_off, strict=True):
        ends = [st for st in (a, b) if st and (st[0] > 0 or not log)]
        if len(ends) == 2:
            ax.hlines(
                y,
                a[0] / scale,
                b[0] / scale,
                color=t["muted"],
                linewidth=2,
                zorder=1,
            )
        for st in ends:  # the smaller value to the left of its dot, the larger to the right
            low = st is min(ends, key=lambda e: e[0]) and len(ends) == 2
            ax.annotate(
                f"{st[0] / scale:,.0f}",
                (st[1 if low else 2] / scale, y),
                xytext=(-8 if low else 8, 0),
                textcoords="offset points",
                ha="right" if low else "left",
                va="center",
                color=t["ink2"],
                fontsize=9,
            )
    ax.set_yticks(ys, labels)
    ax.set_ylim(-0.6, len(labels) - 0.4)
    ax.tick_params(axis="y", length=0)
    ax.grid(axis="y", visible=False)
    if log:
        ax.set_xscale("log")
        ax.minorticks_off()  # labeled decades only; minor ticks crowd the axis
    ax.xaxis.set_major_formatter("{x:,.0f}")
    return ys


def combiner_dumbbells(rows, out, t, groups, ticks, ylabel, title):
    """Saves a combiner comparison to out, in theme t: for each group, bytes
    shuffled and job time with the combiner off and on, as a dumbbell.

    Bytes use a log axis, so each row's length is the factor the combiner
    saves and rows compare directly; job time uses a linear axis from zero.
    groups gives setting prefixes matched as GROUP-on and GROUP-off, in
    display order, top to bottom; ticks supplies their labels and ylabel
    names them. Dots are valid="1" medians and rings their runs, in decimal
    MB and coordinator seconds. A group without a valid
    run on one side omits that dot; savings need both medians positive.
    Rows must be nonempty. Saves to out and closes the figure on success.
    Missing fields, invalid numbers, and plotting or file errors propagate.
    """
    fig, (left, right) = plt.subplots(1, 2, figsize=(10, 4.4), sharey=True)
    found = {"bytes": [], "time": []}  # off/on byte ratios, and job-time savings
    for ax, field, scale, log, axis_title, saving in (
        (
            left,
            "map_output_bytes",
            1e6,
            True,
            "Bytes shuffled (MB, log scale)",
            lambda on, off: f"{off / on:.0f}× fewer",
        ),
        (
            right,
            "seconds",
            1,
            False,
            "Job time (s)",
            lambda on, off: f"{abs(1 - on / off):.0%} " + ("faster" if on <= off else "slower"),
        ),
    ):
        on = [spread(valid(rows, setting=f"{g}-on"), field) for g in groups]
        off = [spread(valid(rows, setting=f"{g}-off"), field) for g in groups]
        each = {
            side: [[float(r[field]) for r in valid(rows, setting=f"{g}-{side}")] for g in groups]
            for side in ("on", "off")
        }
        ys = dumbbell(ax, t, ticks, on, off, each["on"], each["off"], scale, log=log)
        ends = [st for st in on + off if st and (st[0] > 0 or not log)]
        lo = min((st[1] for st in ends), default=0) / scale
        hi = max((st[2] for st in ends), default=0) / scale
        if log:  # room for the labels at both ends, a factor of four each way
            ax.set_xlim(lo / 4, hi * 4) if lo > 0 else ax.set_xlim(0.1, 10)
        else:
            ax.set_xlim(0, (hi or 1) * 1.25)  # with no valid run, the axis still needs a width
        for y, a, b in zip(ys, on, off, strict=True):
            if a and b and a[0] > 0 and b[0] > 0:
                if log:
                    found["bytes"].append(b[0] / a[0])
                    mid = (a[0] * b[0]) ** 0.5 / scale  # the middle of the line on a log axis
                else:
                    found["time"].append(1 - a[0] / b[0])
                    mid = (a[0] + b[0]) / 2 / scale
                ax.annotate(
                    saving(a[0], b[0]),
                    (mid, y),
                    xytext=(0, 9),
                    textcoords="offset points",
                    ha="center",
                    color=t["ink"],
                    fontsize=9.5,
                )
        ax.set_title(axis_title)
    left.set_ylabel(ylabel)
    fig.legend(
        *left.get_legend_handles_labels(),
        loc="upper right",
        ncols=2,
        bbox_to_anchor=(0.99, 0.985),
    )
    if found["bytes"] and found["time"]:
        title = title.format(
            bytes=span(found["bytes"], lambda x: f"{x:.0f}") + "×",
            time=span(found["time"], lambda x: f"{x:.0%}".rstrip("%")) + "%",
        )
        if min(found["time"]) < 0:  # a saving below zero is not "faster"
            title = title.split(",", 1)[0]
    else:
        title = title.split(":", 1)[0] + ": the combiner on and off"
    heading(fig, t, title, label_note(rows, ranges=False) + "; rings are single runs.")
    finish(fig, out)


def e1(rows, out, t):
    """Saves E1's combiner comparison to out, in theme t: bytes shuffled
    and job time, combiner on and off, per Zipf exponent in numeric order.

    Settings have the form sEXPONENT-on or sEXPONENT-off. Only valid="1"
    runs contribute to medians and ranges; savings require positive medians
    for both settings. Rows must be nonempty. Missing fields, invalid numbers,
    and plotting or file errors propagate.
    """
    exponents = sorted({r["setting"].rsplit("-", 1)[0] for r in rows}, key=lambda e: float(e[1:]))
    combiner_dumbbells(
        rows,
        out,
        t,
        exponents,
        [f"S = {e[1:]}" for e in exponents],
        "Zipf exponent (higher: more skew)",
        "E1: the combiner shuffles {bytes} fewer bytes, {time} faster",
    )


def e6(rows, out, t):
    """Saves E6's combiner comparison to out, in theme t: bytes shuffled
    and job time, combiner on and off, per input: crawled English, crawled
    pages in every language, and the book, in that order.

    Matches crawl-eng, crawl, and book settings with -on or -off suffixes.
    Only valid="1" runs contribute to medians and ranges; missing valid runs
    omit a bar. Rows must be nonempty. Missing fields, invalid numbers, and
    plotting or file errors propagate.
    """
    inputs = ["crawl-eng", "crawl", "book"]
    combiner_dumbbells(
        rows,
        out,
        t,
        inputs,
        ["crawled\nEnglish", "crawled,\nevery language", "book"],
        "Word count's input",
        "E6: on real text, {bytes} fewer bytes, {time} faster",
    )


def timeline_and_bars(
    rows, results, exp, settings, field, t, title, event=None, styles=None, note=None
):
    """Tasks completed against time for each setting's median run, ending
    at its first maximum completed-task count, and its median job time as
    a horizontal bar in the same color, with minimum-to-maximum range bars.

    settings maps setting names to labels; field names the job-time column
    in seconds. Only valid="1" rows count; rows must be nonempty. Even run
    counts select the lower median run. Task CSVs are read from results/exp
    as tasks-SETTING-RUN.csv; missing files omit only the curve. Settings
    without valid runs are skipped. Timeline seconds start at the job's
    first recorded sample. event labels the first selected run with a
    nonempty event_at, in seconds since submission.

    Each setting takes the next color slot, unless styles maps it to
    (slot, dashed): then settings can share a color, and a dashed one is
    drawn with a dashed line, its bar set apart by a gap. The palette has three
    validated slots; omitting styles when settings outnumber the colors
    raises ValueError. Each slot indexes t["series"].

    A row with a "_dir" key has its task CSVs in results/_dir instead of
    results/exp, so one chart can draw runs from two result directories.
    note, if given, is a second line of the subtitle.

    Returns the unsaved, open figure. Missing data or style keys, invalid
    numbers or color slots, and file or plotting errors propagate.
    """
    if styles is None:
        if len(settings) > len(t["series"]):
            raise ValueError(f"{len(settings)} settings, {len(t['series'])} colors: pass styles")
        styles = {s: (i, False) for i, s in enumerate(settings)}
    fig, (left, right) = plt.subplots(
        1, 2, figsize=(10, 4.6), gridspec_kw={"width_ratios": [1.7, 1]}
    )
    labels, stats, colors, dashes, marked = [], [], [], [], False
    for setting, label in settings.items():
        slot, dashed = styles[setting]
        color = t["series"][slot]
        runs = valid(rows, setting=setting)
        if not runs:
            continue
        run = median_run(runs, field)
        path = results / run.get("_dir", exp) / f"tasks-{setting}-{run['run']}.csv"
        if path.exists():
            t0 = first_seen(path, run["job"])
            times, done = completed(path, run["job"])
            end = done.index(max(done)) + 1 if done else 0  # the curve stops when the job does
            xs = [x - t0 for x in times[:end]]
            style = "--" if dashed else "-"
            left.step(
                xs,
                done[:end],
                where="post",
                color=color,
                ls=style,
                label=label,
                zorder=3,
            )
            if xs:
                dots(left, t, [xs[-1]], [done[end - 1]], color)
        if event and run["event_at"] and not marked:
            at = float(run["event_at"])
            left.axvline(at, color=t["muted"], linewidth=1, zorder=1)
            left.annotate(
                event,
                (at, 1),
                xycoords=("data", "axes fraction"),
                xytext=(4, -4),
                textcoords="offset points",
                va="top",
                color=t["ink2"],
                fontsize=9,
            )
            marked = True
        labels.append(label)
        stats.append(spread(runs, field))
        colors.append(color)
        dashes.append(dashed)
    left.set_xlabel("Seconds since submission")
    left.set_ylabel("Tasks completed (map + reduce)")
    left.set_xlim(left=0)
    left.set_ylim(bottom=0)
    left.legend(loc="lower right")
    left.set_title("Progress of the median run")

    # The first setting on top; a gap, not a texture, sets dashed settings
    # apart from solid ones (dataviz: texture is never on by default).
    ys, y = [], 0.0
    for i in reversed(range(len(labels))):
        ys.insert(0, y)
        y += 1.6 if i and dashes[i - 1] != dashes[i] else 1
    med = [s[0] for s in stats]
    right.barh(ys, med, height=0.4, color=colors, edgecolor=t["surface"], linewidth=2, zorder=2)
    ranges(right, t, ys, med, [s[1] for s in stats], [s[2] for s in stats], horizontal=True)
    for y, s in zip(ys, stats, strict=True):
        right.annotate(
            f"{s[0]:.1f} s",
            (s[2], y),
            xytext=(6, 0),
            textcoords="offset points",
            va="center",
            color=t["ink"],
        )
    right.set_yticks(ys, labels)
    pad = max(0, 3 - (ys[0] if ys else 0)) / 2  # two bars look as thin as E2's four
    right.set_ylim(-0.7 - pad, (ys[0] if ys else 0) + 0.7 + pad)
    right.set_xlim(0, (max((s[2] for s in stats), default=0) or 1) * 1.3)  # no valid run: empty
    right.set_xlabel("Job time (s)")
    right.grid(axis="y", visible=False)
    right.tick_params(axis="y", length=0)
    right.set_title("Job time")
    heading(fig, t, title, label_note(rows) + "." + (f"\n{note}" if note else ""))
    return fig


def median_of(rows, setting, field):
    """The median of field over setting's valid runs, or None."""
    st = spread(valid(rows, setting=setting), field)
    return st[0] if st else None


def e2_title(rows):
    """E2's finding: what backups do to a straggler's job time."""
    on = median_of(rows, "straggler-backups", "seconds")
    off = median_of(rows, "straggler-no-backups", "seconds")
    if on and off:
        return f"E2: backups cut a straggler's job from {off:.1f} s to {on:.1f} s"
    return "E2: backup tasks and a straggler"


def e2(rows, results, out, t):
    """Saves E2's backup-task comparison to out, in theme t, and closes it.

    Compares backups on and off, with and without a worker at 0.2 CPU.
    Color marks backups on (slot 1) or off (slot 2); dashed lines, and a
    gap between the bars, mark the runs without a straggler. Only valid="1" runs
    count; coordinator seconds determine median-run progress and median
    job times with minimum-to-maximum range bars.
    Task CSVs are read from results / "E2"; missing files omit only curves.
    Missing fields, invalid numbers, and file or plotting errors propagate.
    """
    settings = {
        "normal": "no straggler, backups on",
        "normal-no-backups": "no straggler, backups off",
        "straggler-backups": "straggler, backups on",
        "straggler-no-backups": "straggler, backups off",
    }
    # Four settings, two factors: the color says whether backups are on
    # (blue, as shipped) or off (orange), as color marks the combiner in E1
    # and E6; a dashed line and a gap between the bars mark the no-straggler
    # reference.
    styles = {
        "normal": (0, True),
        "normal-no-backups": (1, True),
        "straggler-backups": (0, False),
        "straggler-no-backups": (1, False),
    }
    fig = timeline_and_bars(
        rows,
        results,
        "E2",
        settings,
        "seconds",
        t,
        e2_title(rows),
        styles=styles,
    )
    finish(fig, out)


def e2_comparison(pairs, rows, results, out, t):
    """Saves the E2 chart that mixes two datasets to out, in theme t, and
    closes it: no straggler from pairs (the rows of E2-pairs/runs.csv, ten
    runs taken in turn per setting, so drift hits both settings alike), a
    straggler from rows (the rows of E2/runs.csv, three runs per setting,
    the only straggler data). Both are read from results, the task CSVs of
    each from its own directory, and the image says so in its subtitle.
    Raises ValueError if the two ran different input sizes, M, R or worker
    counts, since the chart would then compare unlike jobs.

    Drawn as e2 draws its chart, from the same settings and styles; only
    valid="1" runs count. Missing fields, invalid numbers, and file or
    plotting errors propagate.
    """
    settings = {
        "normal": "no straggler, backups on",
        "normal-no-backups": "no straggler, backups off",
        "straggler-backups": "straggler, backups on",
        "straggler-no-backups": "straggler, backups off",
    }
    styles = {
        "normal": (0, True),
        "normal-no-backups": (1, True),
        "straggler-backups": (0, False),
        "straggler-no-backups": (1, False),
    }
    # Each setting comes only from its stated source, as the subtitle says.
    mixed = [
        dict(r, _dir="E2-pairs") for r in pairs if r["setting"] in ("normal", "normal-no-backups")
    ] + [
        dict(r, _dir="E2")
        for r in rows
        if r["setting"] in ("straggler-backups", "straggler-no-backups")
    ]
    # The subtitle states one input size, M and R for both datasets, so the
    # plotted runs must have run the same job.
    shapes = {(r["input_bytes"], r["m"], r["r"], r["workers"]) for r in valid(mixed)}
    if len(shapes) > 1:
        raise ValueError(f"E2 and E2-pairs ran different jobs: {sorted(shapes)}")
    fig = timeline_and_bars(
        mixed,
        results,
        "E2",
        settings,
        "seconds",
        t,
        e2_title(mixed),
        styles=styles,
        note="No-straggler rows: paired runs (E2-pairs); straggler rows: main E2 runs.",
    )
    finish(fig, out, top=0.8)


def e3(rows, results, out, t):
    """Saves E3's worker-kill comparison to out, in theme t, and closes it.

    Left: the median wall time of each kill point (25, 50 and 75% of the
    normal job time) as a stem from the no-failure median, each run a ring
    and each stem labeled with the time it adds. Right: input, shuffle and
    output rates of the median run killed at 50%, from its samples file
    through plot-rates.py, with the observed worker loss shaded. Only
    valid="1" runs count; a missing samples file leaves the right panel
    empty. The title says every output matched the undisturbed run's only
    if every kill run is valid. Missing fields, invalid numbers, and file,
    subprocess or plotting errors propagate.
    """
    fig, (left, right) = plt.subplots(1, 2, figsize=(10, 4.6))
    normal = spread(valid(rows, setting="normal"), "wall_seconds")
    pcts = [p for p in (25, 50, 75) if valid(rows, setting=f"kill-{p}")]
    stats = [spread(valid(rows, setting=f"kill-{p}"), "wall_seconds") for p in pcts]
    color = t["series"][1]  # a disturbed run, as in E2 and E4
    if normal:
        left.axhline(normal[0], color=t["muted"], linewidth=1, zorder=1)
        left.annotate(
            f"no failure, {normal[0]:.1f} s",
            (pcts[0] if pcts else 25, normal[0]),
            xytext=(0, -14),
            textcoords="offset points",
            color=t["ink2"],
            fontsize=9,
        )
    if pcts:
        med = [s[0] for s in stats]
        if normal:  # each kill point is its own setting: a stem from the baseline, no line between
            left.vlines(pcts, normal[0], med, color=color, linewidth=2, zorder=2)
        for p in pcts:
            each = [float(r["wall_seconds"]) for r in valid(rows, setting=f"kill-{p}")]
            runs(left, t, [p] * len(each), each, color)
        dots(left, t, pcts, med, color)
        if normal:
            for p, m in zip(pcts, med, strict=True):
                left.annotate(
                    f"+{m - normal[0]:.1f} s",
                    (p, m),
                    xytext=(-8, 8),
                    textcoords="offset points",
                    ha="right",
                    color=t["ink"],
                    fontsize=9.5,
                )
        left.set_xticks(pcts, [f"{p}%" for p in pcts])
        left.set_xlim(pcts[0] - 12, pcts[-1] + 12)
        left.set_ylim(0, max(s[2] for s in stats) * 1.2)
    left.set_xlabel("When the worker is killed, as a share of an undisturbed job's time")
    left.set_ylabel("Job time (s)")
    left.grid(axis="x", visible=False)
    left.set_title("Job time with one of four workers killed")
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
        moving = [
            x
            for x, r in zip(times, rates, strict=True)
            if any(float(r[k]) > 0 for k in ("input", "shuffle", "output"))
        ]
        for rate, c in zip(("input", "shuffle", "output"), t["series"], strict=True):
            ys = [float(r[rate]) for r in rates]
            right.plot(times, ys, label=rate, color=c, linewidth=1.5)
        # event_at is relative to submission, which starts after sampling.
        # Historical runs lack that offset; mark the observed loss instead.
        interval = failure_interval(path, int(run["workers"]))
        if interval:
            right.axvspan(*interval, color=t["muted"], alpha=0.35, linewidth=0, zorder=1)
            right.annotate(
                "worker lost (approx.)",
                (interval[1], 1),
                xycoords=("data", "axes fraction"),
                xytext=(4, -4),
                textcoords="offset points",
                va="top",
                color=t["ink2"],
                fontsize=9,
            )
        right.set_xlabel("Seconds since sampling began")
        right.set_ylabel("MB/s")
        right.set_xlim(0, (moving[-1] + 3) if moving else None)  # until the data stops
        right.set_ylim(bottom=0)
        right.legend(loc="upper left", bbox_to_anchor=(0.32, 0.92))  # clear of the data
        right.set_title("Data rates of the median run killed at 50%")
    title = "E3: losing a worker during distributed sort"
    if normal and pcts:
        cost = [s[0] - normal[0] for s in stats]
        title = f"E3: a lost worker adds {span(cost, lambda x: f'{x:.0f}')} s"
        killed = [r for r in rows if r["setting"].startswith("kill-")]
        if killed and all(r["valid"] == "1" for r in killed):
            title += "; output unchanged"
    heading(
        fig,
        t,
        title,
        label_note(rows, ranges=False) + "; each stem is the time a failure adds.\n"
        "Job time runs from when mr submit returned; a kill does not reset it.",
    )
    finish(fig, out)


def e4_title(rows):
    """E4's finding: what a coordinator restart costs."""
    normal = median_of(rows, "normal", "wall_seconds")
    restart = median_of(rows, "restart", "wall_seconds")
    if normal and restart:
        return f"E4: a coordinator restart adds {restart - normal:.1f} s; the job resumes"
    return "E4: a coordinator restart, recovered from its log"


def e4(rows, results, out, t):
    """Saves E4's restart comparison to out, in theme t, and closes it:
    tasks completed over time for the median run with and without a
    coordinator restart, the restart marked, and median wall times as bars.
    Only valid="1" runs count. Task CSVs are read from results / "E4";
    missing files omit only curves. Missing fields, invalid numbers, and
    file or plotting errors propagate.
    """
    settings = {"normal": "no restart", "restart": "coordinator restarted"}
    fig = timeline_and_bars(
        rows,
        results,
        "E4",
        settings,
        "wall_seconds",
        t,
        e4_title(rows),
        "coordinator restarted",
        note="Job time runs from when mr submit returned; a restart does not reset it.",
    )
    finish(fig, out)


def map_phase(path, job):
    """Seconds from a job's first sample in a scrape-tasks.py file until all
    its map tasks are completed, or None if the file never shows that."""
    total, done, t0 = None, {}, None
    with open(path, newline="") as f:
        for row in csv.DictReader(f):
            if row["job"] != job or row["kind"] != "map":
                continue
            t = float(row["seconds"])
            t0 = t if t0 is None else t0
            if row["state"] == "total":
                total = int(row["value"])
            elif row["state"] == "completed":
                done[t] = int(row["value"])
    end = next((t for t in sorted(done) if total and done[t] >= total), None)
    return None if end is None else end - t0


def e5_title(rows, present):
    """E5's finding: each job file's multiple of the compiled job time."""
    base = median_of(rows, "compiled", "seconds")
    times = {k: median_of(rows, k, "seconds") for k in present if k in ("interpreted", "naive")}
    found = {k: x / base for k, x in times.items() if x and base}
    if "interpreted" in found and "naive" in found:
        return (
            f"E5: a job file runs {found['interpreted']:.1f}× slower,"
            f" {found['naive']:.1f}× without helpers"
        )
    if "interpreted" in found:
        return f"E5: a job file runs {found['interpreted']:.1f}× slower than compiled"
    if "naive" in found:
        return f"E5: a job file without helpers runs {found['naive']:.1f}× slower"
    return "E5: what an interpreted job file costs in word count"


def e5(rows, results, out, t):
    """Saves E5's comparison to out, in theme t: job time and map-phase time
    of word count compiled into the workers, as the example job file (which
    calls the compiled helpers) and as the naive job file (which does not),
    each labelled with its multiple of the compiled time. Settings with no
    valid run are left out, so older results with two settings still plot.
    Only valid="1" runs count; a run without a tasks file has no map-phase
    time. Missing fields, invalid numbers, and file errors propagate."""
    settings = {
        "compiled": "compiled",
        "interpreted": "job file\n(api.Words,\napi.Sum)",
        "naive": "job file\n(own word\ntest)",
    }
    present = [k for k in settings if valid(rows, setting=k)]
    phases = {}
    for setting in present:
        times = []
        for r in valid(rows, setting=setting):
            path = results / "E5" / f"tasks-{setting}-{r['run']}.csv"
            m = map_phase(path, r["job"]) if path.exists() else None
            if m is not None:
                times.append({"phase": m})
        phases[setting] = spread(times, "phase")
    fig, axes = plt.subplots(1, 2, figsize=(9, 4.4))
    for ax, title, stats in (
        (
            axes[0],
            "Job time (s)",
            [spread(valid(rows, setting=k), "seconds") for k in present],
        ),
        (axes[1], "Map phase (s)", [phases[k] for k in present]),
    ):
        kept = [(j, st) for j, st in enumerate(stats) if st]
        xs, med = [j for j, _ in kept], [st[0] for _, st in kept]
        # compiled, the shipped path, in slot 1; both job files in slot 2
        colors = [t["series"][0 if present[j] == "compiled" else 1] for j in xs]
        ax.bar(
            xs,
            med,
            width=0.4,
            color=colors,
            edgecolor=t["surface"],
            linewidth=2,
            zorder=2,
        )
        ranges(ax, t, xs, med, [st[1] for _, st in kept], [st[2] for _, st in kept])
        base = stats[0][0] if stats and stats[0] and present[0] == "compiled" else 0
        for j, st in kept:
            label = f"{st[0]:.1f} s"
            if j > 0 and base > 0:
                label += f" ({st[0] / base:.1f}×)"
            ax.annotate(
                label,
                (j, st[2]),
                xytext=(0, 4),
                textcoords="offset points",
                ha="center",
                color=t["ink2"],
                fontsize=9,
            )
        top = max((st[2] for _, st in kept), default=0) or 1
        ax.set_xticks(range(len(present)), [settings[k] for k in present])
        ax.set_xlim(-0.6, len(present) - 0.4)
        ax.set_ylim(0, top * 1.15)
        ax.set_title(title)
        ax.grid(axis="x", visible=False)
    heading(
        fig,
        t,
        e5_title(rows, present),
        "The same program, compiled into the workers or sent as a job file the workers interpret.\n"
        + label_note(rows)
        + "; labels give the multiple of the compiled time.",
    )
    finish(fig, out, top=0.8)


def faults(e2_rows, e3_rows, e4_rows, out, t):
    """Saves the fault-tolerance summary to out, in theme t: for each
    disturbance in E2, E3 and E4, the median job time as a share added to
    the same experiment's undisturbed median, one horizontal bar each, on
    one axis, with a range bar from its fastest to its slowest valid run.
    Experiments are grouped with a gap between them, as each has its own
    undisturbed baseline. Bars share slot 1; their labels name the
    disturbance. Each bar is labeled with both medians. The title gives the
    range of medians added and claims the undisturbed output only if every
    disturbed run is valid, drawn or not. Disturbances without valid runs, or experiments without
    an undisturbed median, are left out. Missing fields, invalid numbers
    and plotting or file errors propagate."""
    groups = []  # per experiment: [(label, baseline, (median, low, high), backups off)]
    disturbed = []  # every run of every disturbance, drawn or not
    for rows, field, base, items in (
        (
            e2_rows,
            "seconds",
            "normal",
            [
                ("straggler, backups on", "straggler-backups", False),
                ("straggler, backups off", "straggler-no-backups", True),
            ],
        ),
        (
            e3_rows,
            "wall_seconds",
            "normal",
            [(f"worker killed at {p}%", f"kill-{p}", False) for p in (25, 50, 75)],
        ),
        (
            e4_rows,
            "wall_seconds",
            "normal",
            [("coordinator restarted", "restart", False)],
        ),
    ):
        b = median_of(rows, base, field)
        group = []
        for label, setting, off in items:
            disturbed += [r for r in rows if r["setting"] == setting]
            d = spread(valid(rows, setting=setting), field)
            if b and d:
                group.append((label, b, d, off))
        if group:
            groups.append(group)
    cases, ys, y = [], [], 0.0
    for group in reversed(groups):  # the first experiment on top, a gap between groups
        for case in reversed(group):
            cases.insert(0, case)
            ys.insert(0, y)
            y += 1
        y += 0.6
    fig, ax = plt.subplots(figsize=(9, 4.6))
    share = [[100 * (v / b - 1) for v in d] for _, b, d, _ in cases]  # median, low, high
    added = [s[0] for s in share]
    # One color: the labels name each disturbance, so color would only repeat them.
    ax.barh(ys, added, height=0.4, color=t["series"][0], edgecolor=t["surface"], linewidth=2)
    ranges(ax, t, ys, added, [s[1] for s in share], [s[2] for s in share], horizontal=True)
    for yy, s, (_, b, d, _) in zip(ys, share, cases, strict=True):
        ax.annotate(
            f"{s[0]:+.0f}%   {d[0]:.1f} s vs {b:.1f} s",
            (max(s[2], 0), yy),
            xytext=(6, 0),
            textcoords="offset points",
            va="center",
            color=t["ink"],
            fontsize=9.5,
        )
    ax.set_yticks(ys, [c[0] for c in cases])
    ax.tick_params(axis="y", length=0)
    # A disturbance that sped a job up is drawn left of zero, not clipped;
    # zero stays in view even when every disturbance sped its job up.
    low = min((s[1] for s in share), default=0)
    high = max((s[2] for s in share), default=0)
    ax.set_xlim(min(low, 0) * 1.45, (high if high > 0 else 1) * 1.38)
    ax.xaxis.set_major_formatter("{x:+.0f}%")
    ax.set_xlabel("Job time added, against the same experiment's undisturbed median")
    ax.grid(axis="y", visible=False)
    # A disturbance whose runs all failed has no bar, but still fails the claim.
    correct = bool(cases) and all(r["valid"] == "1" for r in disturbed)
    title = "Fault tolerance: the time each disturbance adds"
    if correct:
        title = (
            f"Faults add {min(added):+.0f}% to {max(added):+.0f}% to job time; outputs unchanged"
        )
    heading(
        fig,
        t,
        title,
        "Backups (E2), a worker killed mid-job (E3), and a coordinator restart (E4),"
        " on four one-CPU workers.\nE2 is timed by the coordinator; E3 and E4 from when"
        " mr submit returned, which a kill or restart does not reset.\n"
        + label_note(e3_rows or e2_rows or e4_rows)
        + ".",
    )
    finish(fig, out, top=0.75)


# E7's workers' CPU limits when the results record none: experiments.sh's
# E7_CPUS default, which the published runs at 8f0ddb9 used (their chain set
# no E7_CPUS). experiments.sh records the limits in cpus.txt since #349.
E7_CPUS = "1, 0.6, 0.3 and 0.15"


def e7_cpus(results):
    """Returns E7's CPU limits as text, from cpus.txt beside its runs.csv,
    or E7_CPUS when the results record none."""
    path = source(results, "E7") / "cpus.txt"
    if not path.exists():
        return E7_CPUS
    limits = path.read_text().split()
    if not limits:
        return E7_CPUS
    return ", ".join(limits[:-1]) + " and " + limits[-1] if len(limits) > 1 else limits[0]


def e7(rows, out, t, cpus=E7_CPUS):
    """Saves E7 to out, in theme t: job time on workers of mixed speeds
    with no backups (slot 2, as E2 colors backups off), the paper's backup
    rule (slot 1) and LATE (slot 3), one row each, top to bottom.

    Each row has its valid="1" median as a dot and each run as a ring on a
    strip below it; its label gives the median backups launched and won.
    Each rule's median is labeled with the time it saves against no
    backups, and the title gives that saving's range. When the two rules'
    run ranges overlap, the subtitle calls it no clear difference. A setting
    without a valid run is left out. Missing fields, invalid numbers and
    plotting or file errors propagate.
    """
    settings = (
        ("mixed-off", "no backups", 1),
        ("mixed-paper", "paper's rule", 0),
        ("mixed-late", "LATE", 2),
    )
    drawn = [(s, label, slot, valid(rows, setting=s)) for s, label, slot in settings]
    drawn = [d for d in drawn if d[3]]
    fig, ax = plt.subplots(figsize=(9, 3.9))
    ys = list(range(len(drawn)))[::-1]
    off = median_of(rows, "mixed-off", "seconds")
    labels, saved, spans = [], [], {}
    for y, (setting, label, slot, group) in zip(ys, drawn, strict=True):
        color = t["series"][slot]
        st = spread(group, "seconds")
        spans[setting] = st
        runs(ax, t, [float(r["seconds"]) for r in group], [y - 0.22] * len(group), color)
        dots(ax, t, [st[0]], [y], color)
        text = f"{st[0]:.1f} s"
        if setting != "mixed-off" and off:
            cut = 1 - st[0] / off
            saved.append(cut)
            text += f"  {-cut:+.0%}".replace("-", "−")
        ax.annotate(
            text,
            (st[2], y),
            xytext=(10, 0),
            textcoords="offset points",
            va="center",
            color=t["ink"],
            fontsize=9.5,
        )
        if setting != "mixed-off":
            launched = statistics.median(int(r["backups"]) for r in group)
            won = statistics.median(int(r["backups_won"]) for r in group)
            label += f"\n(median {launched:g} backup{'' if launched == 1 else 's'}, {won:g} won)"
        labels.append(label)
    ax.set_yticks(ys, labels)
    ax.set_ylim(-0.6, len(drawn) - 0.4)
    ax.tick_params(axis="y", length=0)
    ax.grid(axis="y", visible=False)
    ax.set_xlim(0, (max((st[2] for st in spans.values()), default=0) or 1) * 1.25)
    ax.set_xlabel("Job time (s)")
    title = "E7: backup rules on workers of mixed speeds"
    if saved and min(saved) >= 0:
        cut = span(saved, lambda x: f"{x:.0%}".rstrip("%"))
        title = f"E7: backups cut a mixed-speed sort by {cut}%"
    note = f"Four workers limited to {cpus} CPUs; dots are medians, rings are runs."
    paper, late = spans.get("mixed-paper"), spans.get("mixed-late")
    if paper and late and paper[1] <= late[2] and late[1] <= paper[2]:
        note += "\nThe paper's rule and LATE: the ranges overlap, so no clear difference."
    heading(fig, t, title, note + "\n" + label_note(rows, ranges=False) + ".")
    finish(fig, out, top=0.92 - 0.06 * (note.count("\n") + 2))


def table(exp, rows):
    """Prints a Markdown job-time table grouped by setting, program, and workers.

    Uses coordinator seconds, as the plots do, except for E3 and E4, whose
    kill or restart makes wall_seconds the job time.
    Each group shows the median and range of valid="1" runs and their count
    out of all runs; groups without valid runs show an em dash for time.
    Missing fields, invalid numbers, and output errors propagate.
    """
    field = "wall_seconds" if exp in ("E3", "E4") else "seconds"
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
    Creates the output directory, writes EXPERIMENT.png and
    EXPERIMENT-dark.png for each nonempty E0 to E7 runs.csv, replacing
    existing plots, and prints tables to stdout.
    Missing or empty CSVs are skipped. Filesystem, CSV parsing, plotting,
    and table errors propagate.
    """
    use_bundled_fonts()
    results = Path(sys.argv[1]) if len(sys.argv) > 1 else BENCH / "results"
    out = Path(sys.argv[2]) if len(sys.argv) > 2 else results
    out.mkdir(parents=True, exist_ok=True)
    draw = {
        "E0": lambda rows, png, t: e0(rows, png, t, disk_reads(results, "E0")),
        "E1": lambda rows, png, t: e1(rows, png, t),
        "E2": lambda rows, png, t: e2(rows, results, png, t),
        "E3": lambda rows, png, t: e3(rows, results, png, t),
        "E4": lambda rows, png, t: e4(rows, results, png, t),
        "E5": lambda rows, png, t: e5(rows, results, png, t),
        "E6": lambda rows, png, t: e6(rows, png, t),
        "E7": lambda rows, png, t: e7(rows, png, t, e7_cpus(results)),
    }
    for exp, fn in draw.items():
        rows = load(results, exp)
        if rows:
            for name, t in THEMES.items():
                with plt.rc_context(rc(t)):
                    fn(
                        rows,
                        out / (f"{exp}.png" if name == "light" else f"{exp}-{name}.png"),
                        t,
                    )
            table(exp, rows)
    disturbed = [load(results, exp) for exp in ("E2", "E3", "E4")]
    if all(disturbed):
        for name, t in THEMES.items():
            with plt.rc_context(rc(t)):
                png = "faults.png" if name == "light" else f"faults-{name}.png"
                faults(*disturbed, out / png, t)
    pairs = load(results, "E2-pairs")
    rows = load(results, "E2")
    if pairs and rows:
        for name, t in THEMES.items():
            with plt.rc_context(rc(t)):
                png = "E2-comparison.png" if name == "light" else f"E2-comparison-{name}.png"
                e2_comparison(pairs, rows, results, out / png, t)


main()
