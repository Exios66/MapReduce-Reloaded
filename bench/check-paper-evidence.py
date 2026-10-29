"""Reject trials whose collectors survived but missed terminal measurements."""

import csv
import json
import sys
from pathlib import Path

COUNTERS = {
    "mr_worker_map_input_bytes_total",
    "mr_worker_shuffle_fetched_bytes_total",
    "mr_worker_reduce_output_bytes_total",
}


def check(directory, job, maps, reduces, completed_mono, completed_epoch):
    path = Path(directory)
    origin = json.loads((path / "clock.json").read_text())["monotonic"]
    completed = completed_mono - origin
    workers = json.loads((path / "workers.json").read_text())
    names = {worker["Name"].lstrip("/") for worker in workers}
    assert len(names) == 4, "expected four workers"
    rounds = {}
    with (path / "samples.csv").open() as stream:
        for row in csv.DictReader(stream):
            if row["metric"] in COUNTERS:
                key = (row["url"], float(row["seconds"]))
                rounds.setdefault(key, set()).add(row["metric"])
    terminal = {
        url for (url, seconds), metrics in rounds.items()
        if seconds >= completed and metrics == COUNTERS
    }
    baselines = {
        url for (url, seconds), metrics in rounds.items()
        if seconds < completed and metrics == COUNTERS
    }
    assert len(terminal) == 4 and terminal == baselines, "missing worker rate coverage"
    done = set()
    with (path / "tasks.csv").open() as stream:
        for row in csv.DictReader(stream):
            if row["job"] == job and row["state"] == "completed" and float(row["seconds"]) >= completed:
                if int(row["value"]) == {"map": maps, "reduce": reduces}[row["kind"]]:
                    done.add(row["kind"])
    assert done == {"map", "reduce"}, "missing terminal task counts for this job"
    resources = set()
    with (path / "resources.jsonl").open() as stream:
        for line in stream:
            row = json.loads(line)
            if float(row["epoch"]) >= completed_epoch and row["memory"] and row["cpu"]:
                resources.add(row["name"])
    assert resources == names, "missing terminal resource samples"
    return {"verified": True, "workers": 4, "completed_seconds": completed}


if __name__ == "__main__":
    print(json.dumps(check(sys.argv[1], sys.argv[2], int(sys.argv[3]), int(sys.argv[4]), float(sys.argv[5]), float(sys.argv[6]))))
