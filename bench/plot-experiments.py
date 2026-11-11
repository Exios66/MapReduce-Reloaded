"""Draws the plots of the performance evaluation (docs/design.md) from the
results of bench/experiments.sh, and prints a Markdown table of each
experiment's job times for bench/README.md.

For each of E0 to E7 whose bench/results/EXPERIMENT/runs.csv
exists, this writes OUT_DIR/EXPERIMENT.png on a light background and
OUT_DIR/EXPERIMENT-dark.png on a dark one. Each title states the finding,
computed from the data. Points are medians of the valid runs only; runs
whose output differed from the undisturbed run's (valid=0) are left out and
counted in the table. Blue is the system as it ships or undisturbed, orange
the changed or disturbed setting, in every figure.

- E0: speed-up over one worker against workers, one line per program with
  whiskers right of each dot spanning its runs (none at the one-worker
  baseline or where narrower than a dot), and perfect scaling dashed.
- E1: bytes shuffled (log axis) and job time per Zipf exponent as
  dumbbells joining the combiner-on and combiner-off medians, each labeled
  with the saving, or no clear difference when the ranges overlap.
- E2: job time with backups on and off, without and with a straggler, as
  dumbbells; and tasks finished against time for the median straggler runs.
- E2-comparison: the same two panels, but of two datasets: the
  no-straggler row comes from the ten paired runs in RESULTS_DIR/E2-pairs
  (the drift-controlled comparison), the straggler row from the main runs
  in RESULTS_DIR/E2 (the only straggler data). The image says so in its
  subtitle. It is drawn, as E2-comparison.png and E2-comparison-dark.png,
  only when both directories have a runs.csv.
- E3: job time at each kill point as a dumbbell from the no-failure
  median, labeled with its cost; and the cluster's total data rate against
  time for the median undisturbed run and the median run killed at 50%,
  with the loss marked.
- E4: job time with and without a coordinator restart as a dumbbell, and
  tasks finished against time with the restart marked.
- E5: job time of the compiled program and the two job files the workers
  interpret, as horizontal bars labeled with their multiple and map phase.
- E6: bytes shuffled and job time per input (crawled English, crawled pages
  in every language, and the book), combiner on and off, as E1 does.
- faults.png (and faults-dark.png), when E2, E3 and E4 all have results:
  the job time each disturbance adds to its experiment's undisturbed
  median, one bar per disturbance on one axis.

E0, E1, E2 and E6 use the coordinator's job time (seconds); E3 and E4 use
the time from when mr submit returned until mr status saw the job finish
(wall_seconds), since a restarted coordinator measures its jobs from when
it replays them.

Usage: python3 bench/plot-experiments.py [RESULTS_DIR [OUT_DIR]]
(defaults: bench/results, and the same directory). Needs matplotlib.
"""

import csv
import logging
import math
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
    """Registers bench/fonts (Atkinson Hyperlegible, SIL Open Font License),
    so every machine sets the figures in the same face."""
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
        "wash": 0.35,  # opacity of a pale band in a series color
    },
    "dark": {
        "surface": "#1a1a19",
        "ink": "#f0efec",
        "ink2": "#c3c2b7",
        "muted": "#898781",
        "grid": "#2c2c2a",
        "axis": "#383835",
        "series": ("#3987e5", "#d95926", "#199e70"),
        "wash": 0.6,  # a dark surface swallows a paler band
    },
}


# Type sizes, in points. Figures are at most 10 inches wide, so text set
# here stays legible when a README shrinks the image to the page.
TITLE, SUBTITLE, PANEL, LABEL, TICK, NOTE = 17, 11.5, 12.5, 11.5, 10.5, 10.5
MARGIN = 0.02  # figure fraction left clear at each side of the heading


def rc(t):
    """Matplotlib settings for theme t: hairline recessive axes and grid,
    no box, 2px lines, bold left-aligned panel titles, text in ink tokens,
    never in a series color."""
    return {
        "figure.facecolor": t["surface"],
        "axes.facecolor": t["surface"],
        "savefig.facecolor": t["surface"],
        "axes.edgecolor": t["axis"],
        "axes.linewidth": 1,
        "axes.spines.top": False,
        "axes.spines.right": False,
        "axes.labelcolor": t["ink2"],
        "axes.labelsize": LABEL,
        "axes.labelpad": 6,
        "axes.titlecolor": t["ink"],
        "axes.titlesize": PANEL,
        "axes.titleweight": "bold",
        "axes.titlelocation": "left",
        "axes.titlepad": 12,
        "axes.grid": True,
        "axes.axisbelow": True,
        "grid.color": t["grid"],
        "grid.linewidth": 1,
        "xtick.color": t["axis"],
        "ytick.color": t["axis"],
        "xtick.labelcolor": t["ink2"],
        "ytick.labelcolor": t["ink2"],
        "xtick.labelsize": TICK,
        "ytick.labelsize": TICK,
        "lines.linewidth": 2,
        "lines.solid_capstyle": "round",
        "lines.solid_joinstyle": "round",
        "legend.frameon": False,
        "legend.labelcolor": t["ink2"],
        "legend.fontsize": LABEL,
        "text.color": t["ink"],
        # Atkinson Hyperlegible, from bench/fonts: drawn for legibility (a
        # distinct 0 and O, 1, l and I), one face whichever machine draws.
        "font.family": ["Atkinson Hyperlegible", "DejaVu Sans"],
        "font.size": NOTE,
        "savefig.dpi": 200,
    }


def heading(fig, t, title, subtitle, key=None):
    """The figure's heading: a bold title, a subtitle in secondary ink that
    says what is measured and how to read the marks, a hairline rule that
    sets the heading off from the panels, and, when key is given as
    (handles, labels), the legend in one row below the rule. finish stacks
    them from the top by their drawn heights, so a longer subtitle never
    runs into a panel."""
    head = fig.text(
        MARGIN, 0.985, title, ha="left", va="top", fontsize=TITLE, weight="bold", color=t["ink"]
    )
    sub = fig.text(
        MARGIN,
        0.93,
        subtitle,
        ha="left",
        va="top",
        fontsize=SUBTITLE,
        color=t["ink2"],
        linespacing=1.5,
        wrap=True,
    )
    legend = None
    if key and key[1]:
        legend = fig.legend(
            *key,
            loc="upper left",
            ncols=len(key[1]),
            borderaxespad=0,
            handlelength=1.8,
            columnspacing=2.2,
        )
    fig.__dict__["_heading"] = (t, head, sub, legend)


def stack_heading(fig, t, head, sub, legend):
    """Places the subtitle below the title, a rule below the subtitle and
    the legend below the rule, measured as drawn, and returns the figure
    fraction left for the panels below them."""
    renderer = fig.canvas.get_renderer()
    height = fig.bbox.height
    pt = fig.dpi / 72 / height  # one point, as a fraction of the figure's height

    def bottom(artist):
        return artist.get_window_extent(renderer).y0 / height

    sub.set_y(bottom(head) - 8 * pt)
    y = bottom(sub) - 10 * pt
    fig.add_artist(
        plt.Line2D(
            [MARGIN, 1 - MARGIN], [y, y], transform=fig.transFigure, color=t["axis"], linewidth=1
        )
    )
    y -= 10 * pt
    if legend is not None:
        legend.set_bbox_to_anchor((MARGIN, y), transform=fig.transFigure)
        y = bottom(legend) - 4 * pt
    return y - 6 * pt


def finish(fig, out):
    """Stacks the heading, lays the panels out below it, saves the figure to
    out, and closes it."""
    top = 0.84
    stack = fig.__dict__.get("_heading")
    if stack:
        try:
            top = stack_heading(fig, *stack)
        except (TypeError, AttributeError):  # matplotlib replaced by mocks, as in the tests
            pass
    fig.tight_layout(rect=(0, 0, 1, top))
    fig.savefig(out)
    plt.close(fig)


def dots(ax, t, xs, ys, color):
    """End markers: 8px, filled with the series color, with a 2px outline
    in the surface color so they stand off the lines behind them."""
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


def span(values, fmt):
    """'lo–hi' with fmt applied to each end, or one value when they agree
    after formatting."""
    lo, hi = fmt(min(values)), fmt(max(values))
    return lo if lo == hi else f"{lo}–{hi}"


def ranges(ax, t, xs, med, lo, hi, horizontal=False, color=None):
    """Thin whiskers from the lowest to the highest run, in secondary ink
    or in color."""
    err = [
        [m - a for m, a in zip(med, lo, strict=True)],
        [b - m for m, b in zip(med, hi, strict=True)],
    ]
    args = {
        "fmt": "none",
        "ecolor": color or t["ink2"],
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


WHISKER_SHIFT = 0.06  # workers: E0 sets each program's whiskers this far apart
MIN_WHISKER = 0.1  # speed-up: E0 draws no whisker narrower than a dot
MIN_BAR_WHISKER = 0.02  # share of the axis: E5's shortest visible whisker


def e0(rows, out, t):
    """Saves E0's speed-up against worker count to out, in theme t.

    Speed-up is the median one-worker job time over the median job time on
    N workers, drawn as a line per program with a whisker from the slowest
    to the fastest N-worker run. Only valid="1" runs count; a program
    without a valid one-worker run, or with a time of zero, is skipped.
    Missing fields, invalid numbers, and file errors propagate. Warm-up
    runs (setting="warmup") are left out.
    """
    rows = [r for r in rows if r["setting"] != "warmup"]
    fig, ax = plt.subplots(figsize=(8, 5.6))
    ax.plot([1, 4], [1, 4], "--", color=t["muted"], linewidth=1, zorder=1)
    ax.annotate(
        "perfect scaling",
        (2.5, 2.5),  # above the diagonal, where no measured line goes
        xytext=(-4, 6),
        textcoords="offset points",
        ha="right",
        va="bottom",
        color=t["muted"],
        fontsize=NOTE,
    )
    best, shortest = [], None  # (speed-up, name, workers) per program; (one-worker time, name)
    for k, ((program, name), color) in enumerate(zip(PROGRAMS.items(), t["series"], strict=True)):
        workers = sorted({int(r["workers"]) for r in valid(rows, program=program)})
        if not workers or workers[0] != 1:
            continue
        stats = [spread(valid(rows, program=program, workers=str(w)), "seconds") for w in workers]
        if any(st[1] <= 0 for st in stats):  # a time that rounds to zero has no ratio
            continue
        one = stats[0][0]
        speedup = [one / s[0] for s in stats]
        ax.plot(workers, speedup, color=color, label=name, zorder=3)
        # The slowest run gives the lowest speed-up, the fastest the highest.
        # One worker is the baseline, 1× by definition, so it has no whisker,
        # and a range narrower than a dot would only show as stray caps. Each
        # program's whiskers sit right of its dots, in program order, so none
        # crosses another program's line or stacks on its whisker.
        shown = [
            (w + WHISKER_SHIFT * (k + 1), x, one / s[2], one / s[1])
            for w, x, s in zip(workers, speedup, stats, strict=True)
            if w > 1 and one / s[1] - one / s[2] >= MIN_WHISKER
        ]
        if shown:
            ranges(ax, t, *map(list, zip(*shown, strict=True)), color=color)
        dots(ax, t, workers, speedup, color)
        best.append((speedup[-1], name, workers[-1]))
        shortest = min(shortest or (float("inf"), ""), (one, name))
        ax.annotate(
            f"{name}  {speedup[-1]:.1f}×",
            # Past the last program's whisker column.
            (workers[-1] + WHISKER_SHIFT * len(PROGRAMS), speedup[-1]),
            xytext=(10, 0),
            textcoords="offset points",
            va="center",
            color=t["ink"],
            fontsize=LABEL,
        )
    ax.set_xticks([1, 2, 4])
    ax.set_xlim(0.8, 5.8)
    ax.set_ylim(0, 4.4)
    ax.set_xlabel("Workers (one CPU each)")
    ax.set_ylabel("Speed-up over one worker (higher is better)")
    ax.yaxis.set_major_formatter("{x:g}×")
    ax.grid(axis="x", visible=False)
    title = "E0: scaling over workers"
    if best:
        top = max(w for *_, w in best)
        each = sorted(((s, n) for s, n, w in best if w == top), reverse=True)
        title = f"E0: {top} workers speed up " + ", ".join(f"{n} {s:.1f}×" for s, n in each)
    note = "Speed-up = job time on 1 worker ÷ job time on N workers."
    if shortest and shortest[0] < 5:
        note += (
            f" {shortest[1].capitalize()} takes {shortest[0]:.1f} s on one worker,"
            " so fixed per-job costs cap its speed-up."
        )
    heading(
        fig,
        t,
        title,
        note
        + "\n"
        + label_note(rows, ranges=False)
        + "; whiskers span the range where wider than a dot.",
        key=ax.get_legend_handles_labels(),
    )
    finish(fig, out)


def dumbbell(
    ax,
    t,
    labels,
    stats_on,
    stats_off,
    scale,
    log=False,
    names=("combiner on", "combiner off"),
    fmt="{:,.0f}".format,
):
    """One row per group, the first on top: the as-shipped median (slot 1,
    blue) and the changed or disturbed median (slot 2, orange), named by
    names, joined by a hairline, and each median labeled with fmt outside
    the pair.

    stats_on and stats_off align with labels and hold (median, low, high)
    triples of valid runs, or None to omit that end. Values are divided by
    scale. On a log axis, where equal ratios have equal lengths, values of
    zero or less cannot be placed and are omitted. On a linear axis, a row
    whose medians are too close for two dots to show apart draws the first
    dot a little above the row and the second a little below, with no line
    between them; their x positions stay exact. Returns the rows' y
    positions.
    """
    ys = list(range(len(labels)))[::-1]
    nudge = [
        DODGE if crowded(a, b, stats_on + stats_off, log) else 0
        for a, b in zip(stats_on, stats_off, strict=True)
    ]
    for stats, color, label, sign in (
        (stats_on, t["series"][0], names[0], 1),
        (stats_off, t["series"][1], names[1], -1),
    ):
        kept = [
            (y + sign * d, st)
            for y, d, st in zip(ys, nudge, stats, strict=True)
            if st and (st[0] > 0 or not log)
        ]
        ax.plot(
            [st[0] / scale for _, st in kept],
            [y for y, _ in kept],
            "o",
            markersize=11,
            color=color,
            markeredgecolor=t["surface"],
            markeredgewidth=2,
            linestyle="none",
            label=label,
            zorder=4,
        )
    for y, d, a, b in zip(ys, nudge, stats_on, stats_off, strict=True):
        ends = [st for st in (a, b) if st and (st[0] > 0 or not log)]
        if len(ends) == 2 and not d:
            ax.hlines(y, a[0] / scale, b[0] / scale, color=t["muted"], linewidth=3, zorder=1)
        for st in ends:  # the smaller value to the left of its dot, the larger to the right
            low = st is min(ends, key=lambda e: e[0]) and len(ends) == 2
            ax.annotate(
                fmt(st[0] / scale),
                (st[0] / scale, y + (d if st is a else -d)),
                xytext=(-11 if low else 11, 0),
                textcoords="offset points",
                ha="right" if low else "left",
                va="center",
                color=t["ink2"],
                fontsize=NOTE,
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


DODGE = 0.12  # rows: how far dumbbell() moves each of two crowded dots off the row
CROWDED = 0.05  # share of the largest median: closer medians are crowded


def crowded(a, b, every, log=False):
    """Whether medians a and b, on a linear axis scaled to the largest of
    every's medians, sit too close for two dots to show apart."""
    if log or not (a and b):
        return False
    return abs(a[0] - b[0]) < CROWDED * max(st[0] for st in every if st)


def gap_labels(ax, t, ys, on, off, text, scale=1, log=False):
    """Labels each row whose two medians are both positive with text(on,
    off) above the middle of the line that joins them, or above the upper
    dot of a crowded row; on a log axis the middle is the geometric mean."""
    for y, a, b in zip(ys, on, off, strict=True):
        if a and b and a[0] > 0 and b[0] > 0:
            mid = ((a[0] * b[0]) ** 0.5 if log else (a[0] + b[0]) / 2) / scale
            ax.annotate(
                text(a, b),
                (mid, y + (DODGE if crowded(a, b, on + off, log) else 0)),
                xytext=(0, 10),
                textcoords="offset points",
                ha="center",
                color=t["ink"],
                fontsize=LABEL,
                weight="bold",
            )


def overlap(a, b):
    """Whether two (median, low, high) ranges overlap: then the medians'
    difference is reported as no clear difference (docs/evaluation.md)."""
    return a[1] <= b[2] and b[1] <= a[2]


def combiner_dumbbells(rows, out, t, groups, ticks, ylabel, title):
    """Saves a combiner comparison to out, in theme t: for each group, bytes
    shuffled and job time with the combiner off and on, as a dumbbell.

    Bytes use a log axis, so each row's length is the factor the combiner
    saves and rows compare directly; job time uses a linear axis from zero.
    groups gives setting prefixes matched as GROUP-on and GROUP-off, in
    display order, top to bottom; ticks supplies their labels and ylabel
    names them. Dots are valid="1" medians, in decimal
    MB and coordinator seconds. A group without a valid
    run on one side omits that dot; savings need both medians positive.
    Rows must be nonempty. Saves to out and closes the figure on success.
    Missing fields, invalid numbers, and plotting or file errors propagate.
    """
    fig, (left, right) = plt.subplots(1, 2, figsize=(10, 5.4), sharey=True)
    # Off/on byte ratios and job-time savings; None marks a pair whose run
    # ranges overlap, which the title may not claim either way.
    found = {"bytes": [], "time": []}
    for ax, field, scale, log, axis_title, saving in (
        (
            left,
            "map_output_bytes",
            1e6,
            True,
            "Bytes shuffled, MB (log scale; fewer is better)",
            lambda on, off: f"{off / on:.0f}× fewer",
        ),
        (
            right,
            "seconds",
            1,
            False,
            "Job time, seconds (lower is better)",
            lambda on, off: f"{abs(1 - on / off):.0%} " + ("faster" if on <= off else "slower"),
        ),
    ):
        on = [spread(valid(rows, setting=f"{g}-on"), field) for g in groups]
        off = [spread(valid(rows, setting=f"{g}-off"), field) for g in groups]
        ys = dumbbell(ax, t, ticks, on, off, scale, log=log)
        ends = [st for st in on + off if st and (st[0] > 0 or not log)]
        lo = min((st[1] for st in ends), default=0) / scale
        hi = max((st[2] for st in ends), default=0) / scale
        if log:  # room for the labels at both ends, a factor of four each way
            ax.set_xlim(lo / 4, hi * 4) if lo > 0 else ax.set_xlim(0.1, 10)
        else:
            ax.set_xlim(0, (hi or 1) * 1.25)  # with no valid run, the axis still needs a width
        for a, b in zip(on, off, strict=True):
            if a and b and a[0] > 0 and b[0] > 0:
                found["bytes" if log else "time"].append(
                    None if overlap(a, b) else b[0] / a[0] if log else 1 - a[0] / b[0]
                )
        gap_labels(
            ax,
            t,
            ys,
            on,
            off,
            lambda a, b, saving=saving: (
                "no clear difference" if overlap(a, b) else saving(a[0], b[0])
            ),
            scale,
            log,
        )
        ax.set_title(axis_title)
    left.set_ylabel(ylabel)
    clear = {k: v and None not in v for k, v in found.items()}
    if clear["bytes"]:
        times = found["time"] if clear["time"] else [0]
        title = title.format(
            bytes=span(found["bytes"], lambda x: f"{x:.0f}") + "×",
            time=span(times, lambda x: f"{x:.0%}".rstrip("%")) + "%",
        )
        # A saving below zero is not "faster", and an overlap is no claim.
        if not clear["time"] or min(found["time"]) < 0:
            title = title.split(",", 1)[0]
    else:
        title = title.split(":", 1)[0] + ": the combiner on and off"
    heading(
        fig,
        t,
        title,
        "Each row joins the medians with the combiner on and off.\n"
        + label_note(rows, ranges=False)
        + "; ranges that overlap are called no clear difference.",
        key=left.get_legend_handles_labels(),
    )
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


def timeline(ax, t, rows, results, exp, settings, field, event=None):
    """Tasks finished against time for each setting's median run, ending
    at its first maximum finished-task count, on ax.

    settings maps setting names to (label, color slot). Only valid="1" rows
    count; even run counts select the lower median run. Task CSVs are read
    from results/exp as tasks-SETTING-RUN.csv, or from results/_dir for a
    row with a "_dir" key; a missing file omits only that curve. Timeline
    seconds start at the job's first recorded sample. event labels the
    first selected run with a nonempty event_at, in seconds since
    submission, with a dashed vertical rule. Colors match the left panel's,
    whose legend the figure shows, so the timeline draws none. Missing
    fields, invalid numbers and file errors propagate."""
    marked = False
    for setting, (label, slot) in settings.items():
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
            ax.step(xs, done[:end], where="post", color=color, label=label, zorder=3)
            if xs:
                dots(ax, t, [xs[-1]], [done[end - 1]], color)
        if event and run["event_at"] and not marked:
            at = float(run["event_at"])
            ax.axvline(at, color=t["muted"], linewidth=1, linestyle="--", zorder=1)
            ax.annotate(
                event,
                (at, 1),
                xycoords=("data", "axes fraction"),
                xytext=(5, -4),
                textcoords="offset points",
                va="top",
                color=t["ink2"],
                fontsize=NOTE,
            )
            marked = True
    ax.set_xlabel("Seconds since the job started")
    ax.set_ylabel("Tasks finished (map + reduce)")
    ax.set_xlim(left=0)
    ax.set_ylim(bottom=0)


def cost_rows(ax, t, rows, field, cases, names, text):
    """The left panel of E2, E3 and E4: one dumbbell row per case, from the
    as-shipped or undisturbed median (blue) to the changed or disturbed
    one (orange), each row labeled with text(base,
    changed) on (median, low, high) triples. cases holds (label, base
    setting, changed setting). Only valid="1" runs count; a case without
    valid runs on one side omits that dot. Returns the (base, changed)
    triples."""
    base = [spread(valid(rows, setting=b), field) for _, b, _ in cases]
    changed = [spread(valid(rows, setting=c), field) for *_, c in cases]
    ys = dumbbell(
        ax,
        t,
        [label for label, *_ in cases],
        base,
        changed,
        1,
        names=names,
        fmt="{:.1f} s".format,
    )
    gap_labels(ax, t, ys, base, changed, text)
    ends = [st for st in base + changed if st]
    ax.set_xlim(0, (max((st[2] for st in ends), default=0) or 1) * 1.3)  # room for the labels
    ax.set_xlabel("Job time, seconds (lower is better)")
    ax.set_title("Job time: undisturbed vs disturbed" if "no failure" in names[0] else "Job time")
    return list(zip(base, changed, strict=True))


def added(a, b):
    """The time a disturbance adds, or no clear difference when the ranges overlap."""
    if overlap(a, b):
        return "no clear difference"
    return f"{b[0] - a[0]:+.1f} s ({b[0] / a[0] - 1:+.0%})"


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


def saved(on, off):
    """What backups save, from the backups-on and backups-off triples."""
    if overlap(on, off):
        return "no clear difference"
    return f"backups {'cut' if on[0] < off[0] else 'add'} {abs(1 - on[0] / off[0]):.0%}"


def backups_figure(rows, results, out, t, title, note):
    """E2's chart, from rows: left, job time with backups on (blue) and off
    (orange), without and with a worker slowed to 0.2 CPU; right, how the
    straggler runs' tasks finish over time. note ends the subtitle."""
    fig, (left, right) = plt.subplots(
        1, 2, figsize=(10, 5.4), gridspec_kw={"width_ratios": [1, 1.15]}
    )
    cost_rows(
        left,
        t,
        rows,
        "seconds",
        [
            ("no straggler", "normal", "normal-no-backups"),
            ("one worker\nat 0.2 CPU", "straggler-backups", "straggler-no-backups"),
        ],
        ("backups on", "backups off"),
        saved,
    )
    timeline(
        right,
        t,
        rows,
        results,
        "E2",
        {
            "straggler-backups": ("backups on", 0),
            "straggler-no-backups": ("backups off", 1),
        },
        "seconds",
    )
    right.set_title("With the straggler: tasks finished over time")
    heading(
        fig,
        t,
        title,
        "Backup tasks rerun a slow worker's last tasks elsewhere; the first copy to finish wins.\n"
        + label_note(rows, ranges=False)
        + "; ranges that overlap are called no clear difference."
        + (f"\n{note}" if note else ""),
        key=left.get_legend_handles_labels(),
    )
    finish(fig, out)


def e2(rows, results, out, t):
    """Saves E2's backup-task comparison to out, in theme t, and closes it.

    Left: coordinator job time with backups on (blue, as shipped) and off
    (orange), without a straggler and with one worker at 0.2 CPU, as
    dumbbells labeled with the saving, or with no clear difference when the
    ranges overlap. Right: tasks finished over time for the median
    straggler run with backups on and off. Only valid="1" runs count. Task
    CSVs are read from results / "E2"; missing files omit only curves.
    Missing fields, invalid numbers, and file or plotting errors propagate.
    """
    backups_figure(rows, results, out, t, e2_title(rows), None)


def e2_comparison(pairs, rows, results, out, t):
    """Saves the E2 chart that mixes two datasets to out, in theme t, and
    closes it: no straggler from pairs (the rows of E2-pairs/runs.csv, ten
    runs taken in turn per setting, so drift hits both settings alike), a
    straggler from rows (the rows of E2/runs.csv, the only straggler data).
    Both are read from results, the task CSVs of each from its own
    directory, and the image says so in its subtitle. Raises ValueError if
    the two ran different input sizes, M, R or worker counts, since the
    chart would then compare unlike jobs.

    Drawn as e2 draws its chart; only valid="1" runs count. Missing fields,
    invalid numbers, and file or plotting errors propagate.
    """
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
    backups_figure(
        mixed,
        results,
        out,
        t,
        e2_title(mixed),
        "No-straggler row: paired runs (E2-pairs); straggler row: main E2 runs.",
    )


def total_rate(path):
    """Seconds since sampling began and the cluster's total MB/s (read,
    shuffled and written) from a samples file, through plot-rates.py."""
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
    return (
        [float(r["seconds"]) for r in rates],
        [sum(float(r[k]) for k in ("input", "shuffle", "output")) for r in rates],
    )


def e3(rows, results, out, t):
    """Saves E3's worker-kill comparison to out, in theme t, and closes it.

    Left: for each kill point (25, 50 and 75% of the normal job time), a
    dumbbell from the no-failure median wall time (blue) to the killed
    median (orange), each row labeled with the time a failure adds. Right:
    the cluster's total data rate over time for the median undisturbed run
    and the median run killed at 50%, from their samples files through
    plot-rates.py, with the observed worker loss shaded. Only valid="1"
    runs count; a missing samples file omits its line. The title says every
    output matched the undisturbed run's only if every kill run is valid.
    Missing fields, invalid numbers, and file, subprocess or plotting
    errors propagate.
    """
    fig, (left, right) = plt.subplots(
        1, 2, figsize=(10, 5.4), gridspec_kw={"width_ratios": [1, 1.15]}
    )
    pcts = [p for p in (25, 50, 75) if valid(rows, setting=f"kill-{p}")]
    pairs = cost_rows(
        left,
        t,
        rows,
        "wall_seconds",
        [(f"killed {p}% of\nthe way through", "normal", f"kill-{p}") for p in pcts],
        ("no failure", "one worker killed"),
        added,
    )
    ends = []
    for setting, slot in (("normal", 0), ("kill-50", 1)):
        run = median_run(valid(rows, setting=setting), "wall_seconds")
        path = run and results / "E3" / f"samples-{setting}-{run['run']}.csv"
        if not (path and path.exists()):
            continue
        times, total = total_rate(path)
        moving = [x for x, v in zip(times, total, strict=True) if v > 0]
        right.plot(times, total, color=t["series"][slot], linewidth=1.8)
        ends.append((moving[-1] + 3) if moving else 1)  # until the data stops
        if setting == "kill-50":
            # event_at is relative to submission, which starts after sampling.
            # Historical runs lack that offset; mark the observed loss instead.
            interval = failure_interval(path, int(run["workers"]))
            if interval:
                right.axvspan(*interval, color=t["muted"], alpha=0.35, linewidth=0, zorder=1)
                right.annotate(
                    # Top left of the band, where both lines are low.
                    "worker\nlost",
                    (interval[0], 1),
                    xycoords=("data", "axes fraction"),
                    xytext=(-6, -6),
                    textcoords="offset points",
                    ha="right",
                    va="top",
                    color=t["ink2"],
                    fontsize=NOTE,
                )
    if ends:
        right.set_xlim(0, max(ends))
        right.set_xlabel("Seconds since sampling began")
        right.set_ylabel("MB/s read + shuffled + written")
        right.set_ylim(bottom=0)
        right.set_title("Why: the killed run stalls, then redoes lost work")
    title = "E3: losing a worker during distributed sort"
    if pairs and all(a and b for a, b in pairs):
        cost = [b[0] - a[0] for a, b in pairs]
        title = f"E3: a lost worker adds {span(cost, lambda x: f'{x:.0f}')} s"
        killed = [r for r in rows if r["setting"].startswith("kill-")]
        if killed and all(r["valid"] == "1" for r in killed):
            title += "; output unchanged"
    heading(
        fig,
        t,
        title,
        "One of four workers is killed partway through; the coordinator notices after a 10 s"
        " timeout and reruns its work.\n"
        + label_note(rows, ranges=False)
        + ". Job time runs from when mr submit returned.",
        key=left.get_legend_handles_labels(),
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
    """Saves E4's restart comparison to out, in theme t, and closes it.

    Top: a dumbbell from the median wall time without a restart (blue) to
    the median with the coordinator restarted mid-job (orange), labeled
    with the time added; one comparison needs only a short strip. Below:
    tasks finished over time for the median run of each, the restart
    marked. Only valid="1" runs count.
    Task CSVs are read from results / "E4"; missing files omit only curves.
    Missing fields, invalid numbers, and file or plotting errors propagate.
    """
    fig, (left, right) = plt.subplots(
        2, 1, figsize=(10, 6.6), gridspec_kw={"height_ratios": [1, 3.2]}
    )
    cost_rows(
        left,
        t,
        rows,
        "wall_seconds",
        [("coordinator\nrestarted", "normal", "restart")],
        ("no failure", "coordinator restarted"),
        added,
    )
    timeline(
        right,
        t,
        rows,
        results,
        "E4",
        {"normal": ("no failure", 0), "restart": ("coordinator restarted", 1)},
        "wall_seconds",
        "restart",
    )
    right.set_title("Why: finished tasks survive the restart")
    heading(
        fig,
        t,
        e4_title(rows),
        "The coordinator is stopped mid-job and restarted; it replays its log and keeps"
        " every finished task.\n"
        + label_note(rows, ranges=False)
        + ". Job time runs from when mr submit returned.",
        key=left.get_legend_handles_labels(),
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
    """Saves E5's comparison to out, in theme t: the median job time of
    word count compiled into the workers (blue, as shipped), as the example
    job file, which calls the compiled helpers, and as the naive job file,
    which does not (orange), one horizontal bar each with a whisker from the
    fastest to the slowest valid run. When every whisker would be too short
    to see (under 2% of the axis), none is drawn and the subtitle gives the
    widest spread instead. Each job file's label gives how many times
    slower than compiled it is and its median map phase, where the
    interpreter's cost lies. Settings with no valid run are left out, so
    older results with two settings still plot. Only valid="1" runs count;
    a run without a tasks file has no map-phase time. Missing fields,
    invalid numbers, and file errors propagate."""
    settings = {
        "compiled": "compiled into\nthe workers",
        "interpreted": "job file using\napi.Words, api.Sum",
        "naive": "job file with its\nown word test",
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
    stats = [spread(valid(rows, setting=k), "seconds") for k in present]
    fig, ax = plt.subplots(figsize=(9, 4.6))
    ys = list(range(len(present)))[::-1]
    med = [st[0] for st in stats]
    colors = [t["series"][0 if k == "compiled" else 1] for k in present]
    ax.barh(ys, med, height=0.5, color=colors, edgecolor=t["surface"], linewidth=2, zorder=2)
    top = (max((st[2] for st in stats), default=0) or 1) * 1.35
    # Whiskers a few pixels long read as specks, not ranges: say the spread.
    if any(st[2] - st[1] >= MIN_BAR_WHISKER * top for st in stats):
        ranges(ax, t, ys, med, [st[1] for st in stats], [st[2] for st in stats], horizontal=True)
        spread_note = label_note(rows) + "."
    else:
        # A zero median has no relative spread; its range is zero wide here.
        widest = max(
            (max(st[0] - st[1], st[2] - st[0]) / st[0] for st in stats if st[0] > 0), default=0
        )
        spread_note = (
            label_note(rows, ranges=False)
            + f". Every run is within {math.ceil(widest * 100):.0f}% of its setting's median."
        )
    base = stats[0][0] if present and present[0] == "compiled" else 0
    for y, k, st in zip(ys, present, stats, strict=True):
        label = f"{st[0]:.1f} s"
        if k != "compiled" and base > 0:
            label += f"  ({st[0] / base:.1f}× slower)"
        if phases[k] and k != "compiled":
            label += f"\nmap phase {phases[k][0]:.1f} s"
        ax.annotate(
            label,
            (st[2], y),
            xytext=(8, 0),
            textcoords="offset points",
            va="center",
            color=t["ink"],
            fontsize=LABEL,
            linespacing=1.3,
        )
    ax.set_yticks(ys, [settings[k] for k in present])
    ax.tick_params(axis="y", length=0)
    ax.set_ylim(-0.6, len(present) - 0.4)
    ax.set_xlim(0, top)
    ax.set_xlabel("Job time, seconds (lower is better)")
    ax.grid(axis="y", visible=False)
    heading(
        fig,
        t,
        e5_title(rows, present),
        "The same word count, compiled into the workers or sent as a job file they interpret.\n"
        + spread_note,
    )
    finish(fig, out)


def faults(e2_rows, e3_rows, e4_rows, out, t):
    """Saves the fault-tolerance summary to out, in theme t: for each
    disturbance in E2, E3 and E4, the median job time as a share added to
    the same experiment's undisturbed median, one horizontal bar each, on
    one axis, with a whisker from its fastest to its slowest valid run.
    Experiments are grouped with a gap between them, as each has its own
    undisturbed baseline. Their labels name the
    disturbance, and every bar is orange, the color of a disturbed run in
    E2, E3 and E4; each experiment's group is headed with what it disturbs.
    Each bar is labeled with both medians. The title gives the
    range of medians added and claims the undisturbed output only if every
    disturbed run is valid, drawn or not. Disturbances without valid runs, or experiments without
    an undisturbed median, are left out. Missing fields, invalid numbers
    and plotting or file errors propagate."""
    groups = []  # per experiment: (heading, [(label, baseline, (median, low, high), off)])
    disturbed = []  # every run of every disturbance, drawn or not
    for head, rows, field, base, items in (
        (
            "E2: one worker slowed to 0.2 CPU",
            e2_rows,
            "seconds",
            "normal",
            [
                ("straggler, backups on", "straggler-backups", False),
                ("straggler, backups off", "straggler-no-backups", True),
            ],
        ),
        (
            "E3: one of four workers killed",
            e3_rows,
            "wall_seconds",
            "normal",
            [(f"worker killed at {p}%", f"kill-{p}", False) for p in (25, 50, 75)],
        ),
        (
            "E4: the coordinator restarted",
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
            groups.append((head, group))
    cases, ys, heads, y = [], [], [], 0.0
    for head, group in reversed(groups):  # the first experiment on top, a gap for its heading
        for case in reversed(group):
            cases.insert(0, case)
            ys.insert(0, y)
            y += 1
        heads.insert(0, (head, y - 0.35))
        y += 0.75
    fig, ax = plt.subplots(figsize=(9.5, 6.2))
    share = [[100 * (v / b - 1) for v in d] for _, b, d, _ in cases]  # median, low, high
    added = [s[0] for s in share]
    # Orange: every bar is a disturbed run, as in E2, E3 and E4.
    ax.barh(ys, added, height=0.5, color=t["series"][1], edgecolor=t["surface"], linewidth=2)
    for head, hy in heads:
        ax.text(
            0,
            hy,
            head,
            transform=ax.get_yaxis_transform(),
            ha="left",
            va="center",
            color=t["ink"],
            fontsize=LABEL,
            weight="bold",
        )
    ranges(ax, t, ys, added, [s[1] for s in share], [s[2] for s in share], horizontal=True)
    for yy, s, (_, b, d, _) in zip(ys, share, cases, strict=True):
        ax.annotate(
            f"{s[0]:+.0f}%   {d[0]:.1f} s vs {b:.1f} s",
            (max(s[2], 0), yy),
            xytext=(10, 0),  # clear of the whisker cap
            textcoords="offset points",
            va="center",
            color=t["ink"],
            fontsize=LABEL,
        )
    ax.set_yticks(ys, [c[0] for c in cases])
    ax.tick_params(axis="y", length=0)
    # A disturbance that sped a job up is drawn left of zero, not clipped;
    # zero stays in view even when every disturbance sped its job up.
    low = min((s[1] for s in share), default=0)
    high = max((s[2] for s in share), default=0)
    ax.set_xlim(min(low, 0) * 1.45, (high if high > 0 else 1) * 1.38)
    ax.xaxis.set_major_formatter("{x:+.0f}%")
    ax.set_xlabel("Job time added over the same experiment without the fault (lower is better)")
    ax.set_ylim(-0.6, (heads[0][1] + 0.4) if heads else 0.6)
    ax.grid(axis="y", visible=False)
    # A disturbance whose runs all failed has no bar, but still fails the claim.
    correct = bool(cases) and all(r["valid"] == "1" for r in disturbed)
    title = "Fault tolerance: the time each disturbance adds"
    if correct:
        lo, hi = f"{min(added):+.0f}%", f"{max(added):+.0f}%"
        cost = lo if lo == hi else f"{lo} to {hi}"
        title = f"Faults add {cost} to job time; outputs unchanged"
    heading(
        fig,
        t,
        title,
        "Each bar is one fault, measured against the same experiment's undisturbed runs"
        " on four one-CPU workers.\n"
        + label_note(e3_rows or e2_rows or e4_rows)
        + ". E2 is timed by the coordinator; E3 and E4 from when mr submit returned.",
    )
    finish(fig, out)


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

    Each row has its valid="1" median as a dot on a pale bar spanning its
    runs' range; its label gives the median backups launched and won.
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
    fig, ax = plt.subplots(figsize=(9, 4.6))
    ys = list(range(len(drawn)))[::-1]
    off = median_of(rows, "mixed-off", "seconds")
    labels, saved, spans = [], [], {}
    for y, (setting, label, slot, group) in zip(ys, drawn, strict=True):
        color = t["series"][slot]
        st = spread(group, "seconds")
        spans[setting] = st
        ax.hlines(y, st[1], st[2], color=color, linewidth=5, alpha=t["wash"], zorder=2)
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
            fontsize=LABEL,
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
    ax.set_xlabel("Job time, seconds (lower is better)")
    title = "E7: backup rules on workers of mixed speeds"
    if saved and min(saved) >= 0:
        cut = span(saved, lambda x: f"{x:.0%}".rstrip("%"))
        title = f"E7: backups cut a mixed-speed sort by {cut}%"
    note = (
        f"Four workers limited to {cpus} CPUs."
        " Dots are medians; the shaded bar spans the fastest to the slowest run."
    )
    paper, late = spans.get("mixed-paper"), spans.get("mixed-late")
    if paper and late and paper[1] <= late[2] and late[1] <= paper[2]:
        note += "\nThe paper's rule and LATE: the ranges overlap, so no clear difference."
    heading(fig, t, title, note + "\n" + label_note(rows, ranges=False) + ".")
    finish(fig, out)


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
        "E0": lambda rows, png, t: e0(rows, png, t),
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
