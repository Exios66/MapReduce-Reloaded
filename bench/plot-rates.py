"""Turns the samples of scrape-metrics.py into the data rates of a job over
time, as in the paper's Figures 2 and 3 (#57).

The paper plots three rates against seconds since the job started (§5.3):
input read by map tasks, data shuffled from map to reduce tasks, and output
written by reduce tasks. Here they are the sums over all workers of
mr_worker_map_input_bytes_total, mr_worker_shuffle_fetched_bytes_total and
mr_worker_reduce_output_bytes_total, differentiated between samples, in MB/s
(10^6 bytes). Workers count input and output as each task ends, so those
two rates come in steps of whole tasks; the shuffle is counted as it is
fetched.

Each worker's counters count from its first sample, and a counter that
drops, as when a worker restarts, counts from zero again.

With fewer than two samples there is no interval, and no chart is drawn.

With only SAMPLES.csv, this writes the rates as CSV to stdout:
seconds,input,shuffle,output, one row per interval between samples, at the
interval's end. With OUT.png it draws them in three panels, one above the
other, as the paper does; that needs matplotlib.

With a TITLE after OUT.png, the chart carries it as an identification
line (program, input size, M, R, commit), so the image is readable apart
from the page that embeds it. Only the annotation changes: the plotted
rates are the same with or without it.

Usage: python3 bench/plot-rates.py SAMPLES.csv [OUT.png [TITLE]]
"""

import csv
import sys
import textwrap

METRICS = {
    "input": "mr_worker_map_input_bytes_total",
    "shuffle": "mr_worker_shuffle_fetched_bytes_total",
    "output": "mr_worker_reduce_output_bytes_total",
}


def rates(rows):
    """Returns (seconds, {rate: [MB/s, ...]}) from scrape-metrics.py rows:
    the times of the rounds, and the rates over the intervals between them,
    one fewer. Each worker's counters count from its first sample, so one
    first seen late adds no bytes it counted before; a worker missing from
    a round counts with its last value; and a counter that drops, as when a
    worker restarts, counts from zero again."""
    by_metric = {metric: rate for rate, metric in METRICS.items()}
    rounds, last = {}, {}
    for row in rows:
        if row["metric"] in by_metric:  # a metric that is never plotted keeps no state
            rounds.setdefault(float(row["seconds"]), []).append(row)
    times, totals = sorted(rounds), []
    sums = dict.fromkeys(METRICS, 0)
    for t in times:
        for row in rounds[t]:
            key, value = (row["url"], row["metric"]), int(row["value"])
            if key in last:
                sums[by_metric[key[1]]] += value - last[key] if value >= last[key] else value
            last[key] = value
        totals.append(dict(sums))
    series = {rate: [] for rate in METRICS}
    for i in range(1, len(times)):
        dt = times[i] - times[i - 1]
        for rate in METRICS:
            series[rate].append((totals[i][rate] - totals[i - 1][rate]) / dt / 1e6)
    return times, series


# The light theme of bench/plot-experiments.py, so a rates plot reads as
# part of the same figure set: text in ink tokens, hairline recessive axes
# and grid, one series color per panel.
SURFACE, INK, INK2, GRID, AXIS = "#fcfcfb", "#0b0b0b", "#52514e", "#e1e0d9", "#c3c2b7"
SERIES = {"input": "#2a78d6", "shuffle": "#eb6834", "output": "#1baf7a"}


def plot(seconds, series, path, title=None):
    if len(seconds) < 2:  # no interval to draw, as when no worker answered
        print("plot-rates: fewer than two samples; no chart drawn", file=sys.stderr)
        return
    import matplotlib

    matplotlib.use("Agg")
    import matplotlib.pyplot as plt

    # A long title wraps instead of running past the figure's edge; each
    # line adds height, so the panels keep their size.
    lines = textwrap.wrap(title, 68) if title else []
    height = 6 + (0.3 + 0.2 * len(lines) if lines else 0)
    fig, axes = plt.subplots(len(series), 1, sharex=True, figsize=(6, height), facecolor=SURFACE)
    for ax, (rate, values) in zip(axes, series.items(), strict=True):
        ax.set_facecolor(SURFACE)
        # Each rate is an average over the interval between two rounds.
        ax.stairs(values, seconds, color=SERIES[rate], linewidth=1.5, zorder=3)
        ax.set_ylabel(f"{rate.capitalize()} (MB/s)", color=INK2)
        ax.set_ylim(bottom=0)
        ax.grid(color=GRID, linewidth=1, zorder=0)
        ax.set_axisbelow(True)
        ax.tick_params(axis="both", color=AXIS, labelcolor=INK2)
        for side, spine in ax.spines.items():
            spine.set_visible(side in ("left", "bottom"))
            spine.set_color(AXIS)
    axes[-1].set_xlabel("Seconds", color=INK2)
    if lines:
        fig.text(
            0.012,
            1 - 0.1 / height,
            "\n".join(lines),
            ha="left",
            va="top",
            fontsize=10,
            weight="bold",
            color=INK,
            linespacing=1.4,
        )
        fig.tight_layout(rect=(0, 0, 1, 1 - (0.25 + 0.2 * len(lines)) / height))
    else:
        fig.tight_layout()
    fig.savefig(path, facecolor=SURFACE)


def main():
    with open(sys.argv[1], newline="") as f:
        seconds, series = rates(csv.DictReader(f))
    if len(sys.argv) > 2:
        plot(seconds, series, sys.argv[2], sys.argv[3] if len(sys.argv) > 3 else None)
        return
    out = csv.writer(sys.stdout, lineterminator="\n")
    out.writerow(["seconds", *METRICS])
    for i, t in enumerate(seconds[1:]):
        out.writerow([f"{t:.3f}", *(f"{series[r][i]:.3f}" for r in METRICS)])


main()
