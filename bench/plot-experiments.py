"""Draws the plots of the performance evaluation (docs/design.md) from the
results of bench/experiments.sh, and prints a Markdown table of each
experiment's job times for bench/README.md.

For each of E0 to E5 whose bench/results/EXPERIMENT/runs.csv
exists, this writes OUT_DIR/EXPERIMENT.png on a light background and
OUT_DIR/EXPERIMENT-dark.png on a dark one. Every point is the median of
the valid runs, with the range as an error bar; runs whose output differed
from the normal run's (valid=0) are left out and counted in the table.

- E0: speed-up over one worker against workers, one line per program, with
  ideal linear speed-up dashed.
- E1: bytes shuffled and job time per Zipf exponent, combiner on and off,
  as grouped bars labeled with the saving.
- E2: tasks completed against time for the median run of each setting, as
  in the paper's Figure 3, and job-time bars in the same colors.
- E3: job time against kill time, with the no-failure time as a line and
  each point labeled with its cost; and input, shuffle and output rates
  against time for the median run killed at 50%, with the loss marked, as
  Figure 3(c).
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
        "font.size": 10,
        "savefig.dpi": 150,
    }


def heading(fig, t, title, subtitle):
    """A left-aligned title and a subtitle in secondary ink, which may run
    to two lines."""
    fig.text(0.012, 0.975, title, ha="left", va="top", fontsize=13, weight="bold", color=t["ink"])
    fig.text(
        0.012, 0.905, subtitle, ha="left", va="top", fontsize=9.5, color=t["ink2"], linespacing=1.5
    )


def finish(fig, out, top=0.84):
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


def ranges(ax, t, xs, med, lo, hi, horizontal=False):
    """Thin range bars (lowest to highest run) in secondary ink."""
    err = [
        [m - a for m, a in zip(med, lo, strict=True)],
        [b - m for m, b in zip(med, hi, strict=True)],
    ]
    args = {"fmt": "none", "ecolor": t["ink2"], "elinewidth": 1, "capsize": 3, "zorder": 3}
    if horizontal:
        ax.errorbar(med, xs, xerr=err, **args)
    else:
        ax.errorbar(xs, med, yerr=err, **args)


def label_note(rows):
    r = rows[0]
    return (
        f"Input {int(r['input_bytes']) / 2**30:g} GiB, M = {r['m']}, R = {r['r']}; "
        "medians of the valid runs, bars span the range"
    )


def e0(rows, out, t):
    """Saves E0's speed-up against worker count to out, in theme t.

    Speed-up is the median one-worker job time over the median job time on
    N workers, with the range from the slowest and fastest N-worker runs.
    Only valid="1" runs count; a program without a valid one-worker run, or
    with a time of zero, is skipped. Missing fields, invalid numbers, and
    file errors propagate.
    """
    fig, ax = plt.subplots(figsize=(7.5, 4.8))
    ax.plot([1, 4], [1, 4], "--", color=t["muted"], linewidth=1, zorder=1)
    ax.text(2.55, 2.75, "ideal (linear)", color=t["ink2"], rotation=33, fontsize=9, ha="center")
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
        ranges(ax, t, workers, speedup, [one / s[2] for s in stats], [one / s[1] for s in stats])
        dots(ax, t, workers, speedup, color)
        ax.annotate(
            f"{name}  {speedup[-1]:.1f}×",
            (workers[-1], speedup[-1]),
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
    ax.legend(loc="upper left")
    heading(
        fig,
        t,
        "E0: scaling over workers",
        "Speed-up = job time on 1 worker ÷ job time on N.\n" + label_note(rows) + ".",
    )
    finish(fig, out, top=0.8)


def grouped(ax, t, groups, stats_on, stats_off, scale):
    """Combiner-on and -off bars per group, with a 2px surface gap."""
    width = 0.32
    for offset, stats, color, label in (
        (-width / 2, stats_on, t["series"][0], "combiner on"),
        (width / 2, stats_off, t["series"][1], "combiner off"),
    ):
        kept = [(j + offset, st) for j, st in enumerate(stats) if st]
        xs, med = [x for x, _ in kept], [st[0] / scale for _, st in kept]
        ax.bar(
            xs,
            med,
            width=width,
            color=color,
            edgecolor=t["surface"],
            linewidth=2,
            label=label,
            zorder=2,
        )
        ranges(ax, t, xs, med, [st[1] / scale for _, st in kept], [st[2] / scale for _, st in kept])
    ax.set_xticks(range(len(groups)), [f"S = {g[1:]}" for g in groups])
    ax.set_xlabel("Zipf exponent of the word frequencies (higher: more skew)")
    ax.grid(axis="x", visible=False)
    ax.set_ylim(bottom=0)


def e1(rows, out, t):
    """Saves E1's combiner comparison to out, in theme t: bytes shuffled
    and job time, combiner on and off, per Zipf exponent, each group labeled
    with the saving. Only valid="1" runs count. Missing fields, invalid
    numbers, and file errors propagate."""
    exponents = sorted({r["setting"].rsplit("-", 1)[0] for r in rows}, key=lambda e: float(e[1:]))
    fig, (left, right) = plt.subplots(1, 2, figsize=(11, 4.6))
    for ax, field, scale, title, saving in (
        (
            left,
            "map_output_bytes",
            1e6,
            "Bytes shuffled (MB)",
            lambda on, off: f"{off / on:.0f}× fewer",
        ),
        (right, "seconds", 1, "Job time (s)", lambda on, off: f"{1 - on / off:.0%} faster"),
    ):
        on = [spread(valid(rows, setting=f"{e}-on"), field) for e in exponents]
        off = [spread(valid(rows, setting=f"{e}-off"), field) for e in exponents]
        grouped(ax, t, exponents, on, off, scale)
        # With no valid run, or only zeros, the axis still needs a height.
        top = max((st[2] for st in on + off if st), default=0) / scale or 1
        for j, (a, b) in enumerate(zip(on, off, strict=True)):
            if a and b and a[0] > 0 and b[0] > 0:
                ax.text(
                    j, top * 1.06, saving(a[0], b[0]), ha="center", color=t["ink"], fontsize=9.5
                )
            if a and ax is left:  # the combiner's bars are too short to read off the axis
                ax.annotate(
                    f"{a[0] / scale:,.0f}",
                    (j - 0.16, a[2] / scale),
                    xytext=(0, 4),
                    textcoords="offset points",
                    ha="center",
                    color=t["ink2"],
                    fontsize=9,
                )
        ax.set_ylim(0, top * 1.18)
        ax.set_title(title)
        ax.yaxis.set_major_formatter("{x:,.0f}")
    fig.legend(
        *left.get_legend_handles_labels(), loc="upper right", ncols=2, bbox_to_anchor=(0.99, 0.985)
    )
    heading(fig, t, "E1: what the combiner saves in word count", label_note(rows) + ".")
    finish(fig, out)


def timeline_and_bars(rows, results, exp, settings, field, t, title, event=None, styles=None):
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
    drawn with a dashed line and a hatched bar. The palette has three
    validated slots; omitting styles when settings outnumber the colors
    raises ValueError. Each slot indexes t["series"].

    Returns the unsaved, open figure. Missing data or style keys, invalid
    numbers or color slots, and file or plotting errors propagate.
    """
    if styles is None:
        if len(settings) > len(t["series"]):
            raise ValueError(f"{len(settings)} settings, {len(t['series'])} colors: pass styles")
        styles = {s: (i, False) for i, s in enumerate(settings)}
    fig, (left, right) = plt.subplots(
        1, 2, figsize=(11, 4.6), gridspec_kw={"width_ratios": [1.7, 1]}
    )
    labels, stats, colors, hatched, marked = [], [], [], [], False
    for setting, label in settings.items():
        slot, dashed = styles[setting]
        color = t["series"][slot]
        runs = valid(rows, setting=setting)
        if not runs:
            continue
        run = median_run(runs, field)
        path = results / exp / f"tasks-{setting}-{run['run']}.csv"
        if path.exists():
            t0 = first_seen(path, run["job"])
            times, done = completed(path, run["job"])
            end = done.index(max(done)) + 1 if done else 0  # the curve stops when the job does
            xs = [x - t0 for x in times[:end]]
            style = "--" if dashed else "-"
            left.step(xs, done[:end], where="post", color=color, ls=style, label=label, zorder=3)
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
        hatched.append(dashed)
    left.set_xlabel("Seconds since submission")
    left.set_ylabel("Tasks completed (map + reduce)")
    left.set_xlim(left=0)
    left.set_ylim(bottom=0)
    left.legend(loc="lower right")
    left.set_title("Progress of the median run")

    ys = list(range(len(labels)))[::-1]  # the first setting on top
    med = [s[0] for s in stats]
    bars = right.barh(
        ys, med, height=0.5, color=colors, edgecolor=t["surface"], linewidth=2, zorder=2
    )
    for i in (i for i, hatch in enumerate(hatched) if hatch):
        # the series color as a hatch on the surface
        bars[i].set(facecolor=t["surface"], edgecolor=colors[i], hatch="///", linewidth=1.5)
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
    right.set_xlim(0, (max((s[2] for s in stats), default=0) or 1) * 1.3)  # no valid run: empty
    right.set_xlabel("Job time (s)")
    right.grid(axis="y", visible=False)
    right.tick_params(axis="y", length=0)
    right.set_title("Job time")
    heading(fig, t, title, label_note(rows) + ".")
    return fig


def e2(rows, results, out, t):
    """Saves E2's backup-task comparison to out, in theme t, and closes it.

    Compares backups on and off, with and without a worker at 0.2 CPU.
    Color marks the straggler; dashed lines and hatched bars mark backups
    off. Only valid="1" runs count; coordinator seconds determine median-run
    progress and median job times with minimum-to-maximum range bars.
    Task CSVs are read from results / "E2"; missing files omit only curves.
    Missing fields, invalid numbers, and file or plotting errors propagate.
    """
    settings = {
        "normal": "no straggler, backups on",
        "normal-no-backups": "no straggler, backups off",
        "straggler-backups": "straggler, backups on",
        "straggler-no-backups": "straggler, backups off",
    }
    # Four settings, two factors: the color says whether a worker straggles,
    # a dashed line and a hatched bar that backups are off.
    styles = {
        "normal": (0, False),
        "normal-no-backups": (0, True),
        "straggler-backups": (1, False),
        "straggler-no-backups": (1, True),
    }
    fig = timeline_and_bars(
        rows,
        results,
        "E2",
        settings,
        "seconds",
        t,
        "E2: backup tasks against a worker throttled to 0.2 CPU",
        styles=styles,
    )
    finish(fig, out)


def e3(rows, results, out, t):
    fig, (left, right) = plt.subplots(1, 2, figsize=(11, 4.6))
    normal = spread(valid(rows, setting="normal"), "wall_seconds")
    pcts = [p for p in (25, 50, 75) if valid(rows, setting=f"kill-{p}")]
    stats = [spread(valid(rows, setting=f"kill-{p}"), "wall_seconds") for p in pcts]
    color = t["series"][0]
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
        left.plot(pcts, med, color=color, zorder=3)
        ranges(left, t, pcts, med, [s[1] for s in stats], [s[2] for s in stats])
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
    left.set_xlabel("When the worker is killed, as a share of the normal job time")
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
            right.plot(times, [float(r[rate]) for r in rates], label=rate, color=c, linewidth=1.5)
        # event_at is relative to submission, which starts after sampling.
        # Historical runs lack that offset; mark the observed loss instead.
        interval = failure_interval(path, int(run["workers"]))
        if interval:
            right.axvspan(*interval, color=t["muted"], alpha=0.35, linewidth=0, zorder=1)
            right.annotate(
                "worker lost",
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
    heading(fig, t, "E3: losing a worker during distributed sort", label_note(rows) + ".")
    finish(fig, out)


def e4(rows, results, out, t):
    settings = {"normal": "no restart", "restart": "coordinator restarted"}
    fig = timeline_and_bars(
        rows,
        results,
        "E4",
        settings,
        "wall_seconds",
        t,
        "E4: a coordinator restart, recovered from its log",
        "coordinator restarted",
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
        "interpreted": "job file\n(api.Words, api.Sum)",
        "naive": "job file\n(own word test)",
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
        (axes[0], "Job time (s)", [spread(valid(rows, setting=k), "seconds") for k in present]),
        (axes[1], "Map phase (s)", [phases[k] for k in present]),
    ):
        kept = [(j, st) for j, st in enumerate(stats) if st]
        xs, med = [j for j, _ in kept], [st[0] for _, st in kept]
        colors = [t["series"][list(settings).index(present[j])] for j in xs]
        ax.bar(xs, med, width=0.55, color=colors, edgecolor=t["surface"], linewidth=2, zorder=2)
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
        "E5: what an interpreted job file costs in word count",
        "The same program, compiled into the workers or sent as a job file the workers interpret.\n"
        + label_note(rows)
        + "; labels give the multiple of the compiled time.",
    )
    finish(fig, out, top=0.8)


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
    EXPERIMENT-dark.png for each nonempty E0 to E5 runs.csv, replacing
    existing plots, and prints tables to stdout.
    Missing or empty CSVs are skipped. Filesystem, CSV parsing, plotting,
    and table errors propagate.
    """
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
    }
    for exp, fn in draw.items():
        rows = load(results, exp)
        if rows:
            for name, t in THEMES.items():
                with plt.rc_context(rc(t)):
                    fn(rows, out / (f"{exp}.png" if name == "light" else f"{exp}-{name}.png"), t)
            table(exp, rows)


main()
