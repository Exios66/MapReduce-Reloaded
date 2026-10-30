"""Reject trials whose collectors survived but missed terminal measurements."""

import csv
import itertools
import json
import math
import sys
from pathlib import Path

COUNTERS = {
    "mr_worker_map_input_bytes_total",
    "mr_worker_shuffle_fetched_bytes_total",
    "mr_worker_reduce_output_bytes_total",
}


def coverage(times, start, end, max_gap, label, interruption=None):
    times = sorted(set(times))
    if not times or not all(math.isfinite(t) for t in times):
        raise ValueError(f"missing or invalid {label} timestamps")
    if times[0] > start or times[-1] < end:
        raise ValueError(f"missing baseline or terminal {label} samples")
    window = [max(t for t in times if t <= start)]
    window.extend(t for t in times if start < t < end)
    window.append(min(t for t in times if t >= end))
    for left, right in itertools.pairwise(window):
        if right - left <= max_gap:
            continue
        if interruption is not None:
            killed, registered = interruption
            if killed - max_gap <= left <= registered and killed <= right <= registered + max_gap:
                continue
        raise ValueError(f"gap in {label} samples: {right - left:.3f}s")


def check(directory, job, maps, reduces, completed_mono, completed_epoch):
    path = Path(directory)
    clock = json.loads((path / "clock.json").read_text())
    submission = json.loads((path / "submission-clock.json").read_text())
    origin = clock["monotonic"]
    completed = completed_mono - origin
    started = submission["monotonic"] - origin
    interval = submission["interval"]
    if (
        not all(math.isfinite(v) for v in (started, completed, completed_epoch, interval))
        or interval <= 0
        or completed < started
    ):
        raise ValueError("invalid observation window")
    max_gap = max(1.0, 3 * interval)
    workers = json.loads((path / "workers.json").read_text())
    names = {worker["Name"].lstrip("/") for worker in workers}
    count = len(workers)
    if count not in (1, 2, 4) or len(names) != count:
        raise ValueError("expected one, two or four distinct workers")
    rounds = {}
    with (path / "samples.csv").open() as stream:
        for row in csv.DictReader(stream):
            if row["metric"] in COUNTERS:
                if int(row["value"]) < 0:
                    raise ValueError("negative worker counter")
                key = (row["url"], float(row["seconds"]))
                rounds.setdefault(key, set()).add(row["metric"])
    terminal = {
        url
        for (url, seconds), metrics in rounds.items()
        if seconds >= completed and metrics == COUNTERS
    }
    baselines = {
        url
        for (url, seconds), metrics in rounds.items()
        if seconds <= started and metrics == COUNTERS
    }
    fault = None
    events = {}
    if (path / "fault.json").exists():
        fault = json.loads((path / "fault.json").read_text())
        with (path / "events.csv").open() as stream:
            events = {r["event"]: float(r["epoch"]) for r in csv.DictReader(stream)}
        if fault["setting"] not in ("kill", "coordinator-restart"):
            raise ValueError("unknown recorded fault")
        if (
            not submission["epoch"]
            <= events["kill_requested"]
            <= events["killed"]
            <= completed_epoch
        ):
            raise ValueError("invalid fault observation window")
    lost = fault["url"] if fault and fault["setting"] == "kill" else None
    expected_terminal = baselines - {lost}
    if len(baselines) != count or not expected_terminal <= terminal <= baselines:
        raise ValueError("missing worker rate coverage")
    if lost is not None and lost not in baselines:
        raise ValueError("unknown killed worker")
    restart_url, interruption = None, None
    if (path / "restart.json").exists():
        restart_url = json.loads((path / "restart.json").read_text())["url"]
        with (path / "events.csv").open() as stream:
            events = {r["event"]: float(r["epoch"]) for r in csv.DictReader(stream)}
        interruption = (
            events["kill_requested"] - clock["epoch"],
            events["registered"] - clock["epoch"],
        )
        if (
            restart_url not in terminal
            or not started <= interruption[0] <= interruption[1] <= completed
        ):
            raise ValueError("invalid restart observation window")
    for url in baselines:
        times = [t for (u, t), metrics in rounds.items() if u == url and metrics == COUNTERS]
        gap = interruption if url == restart_url else None
        if url == lost:
            gap = (events["kill_requested"] - clock["epoch"], completed)
            times.append(completed)  # Only the explicitly killed endpoint may remain absent.
        coverage(
            times,
            started,
            completed,
            max_gap,
            "worker",
            gap,
        )
    done = set()
    task_times = []
    with (path / "tasks.csv").open() as stream:
        for row in csv.DictReader(stream):
            if row["job"] == job:
                task_times.append(float(row["seconds"]))
            if (
                row["job"] == job
                and row["state"] == "completed"
                and float(row["seconds"]) >= completed
            ) and int(row["value"]) == {"map": maps, "reduce": reduces}[row["kind"]]:
                done.add(row["kind"])
    if done != {"map", "reduce"}:
        raise ValueError("missing terminal task counts for this job")
    # This job has no task series before submission; bound the first sample too.
    coordinator_gap = None
    if fault and fault["setting"] == "coordinator-restart":
        coordinator_gap = (
            events["kill_requested"] - clock["epoch"],
            events["recovered"] - clock["epoch"],
        )
        if not started <= coordinator_gap[0] <= coordinator_gap[1] <= completed:
            raise ValueError("invalid coordinator recovery window")
    coverage([started, *task_times], started, completed, max_gap, "task", coordinator_gap)
    resources = {name: [] for name in names}
    with (path / "resources.jsonl").open() as stream:
        for line in stream:
            row = json.loads(line)
            if row["name"] not in resources or not row["memory"] or not row["cpu"]:
                raise ValueError("unexpected or empty resource sample")
            resources[row["name"]].append(float(row["epoch"]))
    for name, times in resources.items():
        gap = None
        if lost is not None and name == fault["name"].lstrip("/"):
            gap = (events["kill_requested"], completed_epoch)
            times.append(completed_epoch)
        # docker stats needs its own CPU measurement interval, plus CLI overhead.
        coverage(
            times,
            submission["epoch"],
            completed_epoch,
            max(6.0, 3 * interval),
            "resource",
            gap,
        )
    return {"verified": True, "workers": count, "completed_seconds": completed}


if __name__ == "__main__":
    print(
        json.dumps(
            check(
                sys.argv[1],
                sys.argv[2],
                int(sys.argv[3]),
                int(sys.argv[4]),
                float(sys.argv[5]),
                float(sys.argv[6]),
            )
        )
    )
