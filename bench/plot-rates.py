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

Usage: python3 bench/plot-rates.py SAMPLES.csv [OUT.png] [--restart-dir RUN_DIR]
The campaign's recorded restart resets its worker's series even when the
first observed new counter exceeds the last old counter.
"""

import argparse
import csv
import json
import sys
from pathlib import Path

METRICS = {
    "input": "mr_worker_map_input_bytes_total",
    "shuffle": "mr_worker_shuffle_fetched_bytes_total",
    "output": "mr_worker_reduce_output_bytes_total",
}


def rates(rows, restart=None):
    """Returns (seconds, {rate: [MB/s, ...]}) from scrape-metrics.py rows:
    the times of the rounds, and the rates over the intervals between them,
    one fewer. Each worker's counters count from its first sample, so one
    first seen late adds no bytes it counted before; a worker missing from
    a round counts with its last value; and a counter that drops, as when a
    worker restarts, counts from zero again."""
    by_metric = {metric: rate for rate, metric in METRICS.items()}
    rounds, last = {}, {}
    reset = set()
    for row in rows:
        if row["metric"] in by_metric:  # a metric that is never plotted keeps no state
            rounds.setdefault(float(row["seconds"]), []).append(row)
    times, totals = sorted(rounds), []
    sums = dict.fromkeys(METRICS, 0)
    for t in times:
        for row in rounds[t]:
            key, value = (row["url"], row["metric"]), int(row["value"])
            if key in last:
                restarted = (
                    restart is not None
                    and key[0] == restart[0]
                    and t >= restart[1]
                    and key not in reset
                )
                delta = value if restarted or value < last[key] else value - last[key]
                sums[by_metric[key[1]]] += delta
                if restarted:
                    reset.add(key)
            last[key] = value
        totals.append(dict(sums))
    series = {rate: [] for rate in METRICS}
    for i in range(1, len(times)):
        dt = times[i] - times[i - 1]
        for rate in METRICS:
            series[rate].append((totals[i][rate] - totals[i - 1][rate]) / dt / 1e6)
    return times, series


def plot(seconds, series, path):
    if len(seconds) < 2:  # no interval to draw, as when no worker answered
        print("plot-rates: fewer than two samples; no chart drawn", file=sys.stderr)
        return
    import matplotlib

    matplotlib.use("Agg")
    import matplotlib.pyplot as plt

    fig, axes = plt.subplots(len(series), 1, sharex=True, figsize=(6, 6))
    for ax, (rate, values) in zip(axes, series.items(), strict=True):
        # Each rate is an average over the interval between two rounds.
        ax.stairs(values, seconds)
        ax.set_ylabel(f"{rate.capitalize()} (MB/s)")
        ax.set_ylim(bottom=0)
    axes[-1].set_xlabel("Seconds")
    fig.tight_layout()
    fig.savefig(path)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("samples")
    parser.add_argument("output", nargs="?")
    parser.add_argument("--restart-dir", type=Path)
    args = parser.parse_args()
    restart = None
    if args.restart_dir is not None:
        directory = args.restart_dir
        url = json.loads((directory / "restart.json").read_text())["url"]
        origin = json.loads((directory / "clock.json").read_text())["epoch"]
        with (directory / "events.csv").open() as stream:
            events = {r["event"]: float(r["epoch"]) for r in csv.DictReader(stream)}
        restart = (url, events["killed"] - origin)
    with open(args.samples, newline="") as f:
        seconds, series = rates(csv.DictReader(f), restart)
    if args.output is not None:
        plot(seconds, series, args.output)
        return
    out = csv.writer(sys.stdout, lineterminator="\n")
    out.writerow(["seconds", *METRICS])
    for i, t in enumerate(seconds[1:]):
        out.writerow([f"{t:.3f}", *(f"{series[r][i]:.3f}" for r in METRICS)])


main()
