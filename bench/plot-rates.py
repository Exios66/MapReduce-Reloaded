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

With only SAMPLES.csv, this writes the rates as CSV to stdout:
seconds,input,shuffle,output, one row per interval between samples, at the
interval's end. With OUT.png it draws them in three panels, one above the
other, as the paper does; that needs matplotlib.

Usage: python3 bench/plot-rates.py SAMPLES.csv [OUT.png]
"""

import csv
import sys

METRICS = {
    "input": "mr_worker_map_input_bytes_total",
    "shuffle": "mr_worker_shuffle_fetched_bytes_total",
    "output": "mr_worker_reduce_output_bytes_total",
}


def rates(rows):
    """Returns (seconds, {rate: [MB/s, ...]}) from scrape-metrics.py rows.
    A worker missing from a round counts with its last value."""
    rounds, last = {}, {}
    for row in rows:
        rounds.setdefault(float(row["seconds"]), []).append(row)
    times, totals = sorted(rounds), []
    for t in times:
        for row in rounds[t]:
            last[row["url"], row["metric"]] = int(row["value"])
        totals.append(
            {
                rate: sum(v for (_, m), v in last.items() if m == metric)
                for rate, metric in METRICS.items()
            }
        )
    series = {rate: [] for rate in METRICS}
    for i in range(1, len(times)):
        dt = times[i] - times[i - 1]
        for rate in METRICS:
            series[rate].append((totals[i][rate] - totals[i - 1][rate]) / dt / 1e6)
    return times[1:], series


def plot(seconds, series, path):
    import matplotlib

    matplotlib.use("Agg")
    import matplotlib.pyplot as plt

    fig, axes = plt.subplots(len(series), 1, sharex=True, figsize=(6, 6))
    for ax, (rate, values) in zip(axes, series.items(), strict=True):
        # Each rate is an average over the interval that ends at its time.
        ax.step(seconds, values, where="pre")
        ax.set_ylabel(f"{rate.capitalize()} (MB/s)")
        ax.set_ylim(bottom=0)
    axes[-1].set_xlabel("Seconds")
    fig.tight_layout()
    fig.savefig(path)


def main():
    with open(sys.argv[1], newline="") as f:
        seconds, series = rates(csv.DictReader(f))
    if len(sys.argv) > 2:
        plot(seconds, series, sys.argv[2])
        return
    out = csv.writer(sys.stdout, lineterminator="\n")
    out.writerow(["seconds", *METRICS])
    for i, t in enumerate(seconds):
        out.writerow([f"{t:.3f}", *(f"{series[r][i]:.3f}" for r in METRICS)])


main()
