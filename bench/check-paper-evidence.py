"""Reject trials whose collectors survived but missed terminal measurements."""

import csv
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
    for left, right in zip(window, window[1:]):
        if right - left <= max_gap:
            continue
        if interruption is not None:
            killed, registered = interruption
            if (
                killed - max_gap <= left <= registered
                and killed <= right <= registered + max_gap
            ):
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
    if len(workers) != 4 or len(names) != 4:
        raise ValueError("expected four workers")
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
    if len(terminal) != 4 or terminal != baselines:
        raise ValueError("missing worker rate coverage")
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
    for url in terminal:
        coverage(
            [t for (u, t), metrics in rounds.items() if u == url and metrics == COUNTERS],
            started,
            completed,
            max_gap,
            "worker",
            interruption if url == restart_url else None,
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
    coverage([started, *task_times], started, completed, max_gap, "task")
    resources = {name: [] for name in names}
    with (path / "resources.jsonl").open() as stream:
        for line in stream:
            row = json.loads(line)
            if row["name"] not in resources or not row["memory"] or not row["cpu"]:
                raise ValueError("unexpected or empty resource sample")
            resources[row["name"]].append(float(row["epoch"]))
    for times in resources.values():
        # docker stats needs its own CPU measurement interval, plus CLI overhead.
        coverage(
            times, submission["epoch"], completed_epoch, max(6.0, 3 * interval), "resource"
        )
    return {"verified": True, "workers": 4, "completed_seconds": completed}


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
