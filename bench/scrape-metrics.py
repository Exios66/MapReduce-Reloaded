"""Samples workers' /metrics during a benchmark (#57) and writes the byte
counters to stdout as CSV, for plot-rates.py.

Every INTERVAL seconds, for DURATION seconds or until interrupted, this
fetches each URL and writes one row per byte counter it serves:
seconds,url,metric,value. seconds is when the round of fetches started,
from the first round, so the rows of one round share it. A round that
takes longer than INTERVAL delays the next one. A URL that does
not answer in a round is left out of that round.

Usage: python3 bench/scrape-metrics.py INTERVAL DURATION URL...
"""

import csv
import sys
import time
import urllib.request

PREFIX = "mr_worker_"
SUFFIX = "_bytes_total"


def counters(text):
    """Returns the byte counters in a Prometheus text exposition."""
    found = {}
    for line in text.splitlines():
        name, _, value = line.partition(" ")
        if name.startswith(PREFIX) and name.endswith(SUFFIX):
            found[name] = int(value)  # the workers write counters as integers
    return found


def main():
    interval, duration, urls = float(sys.argv[1]), float(sys.argv[2]), sys.argv[3:]
    out = csv.writer(sys.stdout, lineterminator="\n")
    out.writerow(["seconds", "url", "metric", "value"])
    start = time.monotonic()
    tick = start
    try:
        while tick - start <= duration:
            # Stamped when the round starts, which is later than its tick
            # if the round before took longer than INTERVAL.
            now = time.monotonic() - start
            for url in urls:
                try:
                    with urllib.request.urlopen(url, timeout=interval) as resp:
                        text = resp.read().decode()
                except OSError:
                    continue
                for name, value in sorted(counters(text).items()):
                    out.writerow([f"{now:.3f}", url, name, value])
            sys.stdout.flush()
            tick = max(tick + interval, time.monotonic())
            time.sleep(max(0.0, tick - time.monotonic()))
    except KeyboardInterrupt:
        pass


main()
