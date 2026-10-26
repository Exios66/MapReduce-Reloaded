"""Samples the coordinator's task counts during an experiment
(bench/experiments.sh) and writes them to stdout as CSV, for
plot-experiments.py.

Every INTERVAL seconds, for DURATION seconds or until interrupted, this
fetches URL and writes one row per mr_job_tasks series it serves:
seconds,job,kind,state,value. seconds counts from the first round. A round
in which the coordinator does not answer, as while it restarts, is left out.

Usage: python3 bench/scrape-tasks.py INTERVAL DURATION URL
"""

import csv
import re
import sys
import time
import urllib.request

SERIES = re.compile(r'^mr_job_tasks\{job="([^"]*)",kind="([^"]*)",state="([^"]*)"\} (\d+)$')


def main():
    interval, duration, url = float(sys.argv[1]), float(sys.argv[2]), sys.argv[3]
    out = csv.writer(sys.stdout, lineterminator="\n")
    out.writerow(["seconds", "job", "kind", "state", "value"])
    start = time.monotonic()
    tick = start
    try:
        while tick - start <= duration:
            now = time.monotonic() - start
            try:
                with urllib.request.urlopen(url, timeout=interval) as resp:
                    text = resp.read().decode()
            except OSError:
                text = ""
            for line in text.splitlines():
                m = SERIES.match(line)
                if m:
                    out.writerow([f"{now:.3f}", *m.groups()])
            sys.stdout.flush()
            tick = max(tick + interval, time.monotonic())
            time.sleep(max(0.0, tick - time.monotonic()))
    except KeyboardInterrupt:
        pass


main()
