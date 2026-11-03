"""Plan or execute the capacity-admitted 10 GB E0–E5 suite, one input at a time.

Planning starts no services. Execution requires prepared input, an explicit
remaining-space budget, and a full-size baseline for timed fault groups.
No inputs or prior evidence are deleted by this scheduler.
"""

import argparse
import json
import os
import subprocess
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


def plan():
    rows = []

    def add(group, experiment, setting, trial, scenario, workers=4, runner="normal", fraction=None):
        rows.append(
            dict(
                group=group,
                experiment=experiment,
                setting=setting,
                run=trial,
                scenario=scenario,
                workers=workers,
                runner_setting=runner,
                fraction=fraction,
            )
        )

    for trial in range(1, 4):
        order = [4, 2, 1]
        order = order[trial - 1 :] + order[: trial - 1]
        for group, scenario in (("grep", "grep"), ("sort", "sort"), ("zipf-1.0", "wordcount")):
            for workers in order:
                add(group, "E0", f"w{workers}", trial, scenario, workers)
        for source in ("zipf-0.5", "zipf-1.0", "zipf-1.5"):
            for combiner in ["on", "off"] if trial % 2 else ["off", "on"]:
                add(
                    source,
                    "E1",
                    f"s{source[5:]}-{combiner}",
                    trial,
                    "wordcount",
                    runner="normal" if combiner == "on" else "no-combiner",
                )
        settings = ["normal", "no-backups", "straggler", "straggler-no-backups"]
        for setting in settings[trial - 1 :] + settings[: trial - 1]:
            add("sort", "E2", setting, trial, "sort", runner=setting)
        faults = [("normal", None), ("kill-25", 0.25), ("kill-50", 0.5), ("kill-75", 0.75)]
        for setting, fraction in faults[trial - 1 :] + faults[: trial - 1]:
            add(
                "sort",
                "E3",
                setting,
                trial,
                "sort",
                runner="kill" if fraction else "normal",
                fraction=fraction,
            )
        for setting in (
            ["normal", "coordinator-restart"] if trial % 2 else ["coordinator-restart", "normal"]
        ):
            add(
                "sort",
                "E4",
                setting,
                trial,
                "sort",
                runner=setting,
                fraction=0.5 if setting == "coordinator-restart" else None,
            )
        kinds = ["compiled", "interpreted", "naive"]
        for kind in kinds[trial - 1 :] + kinds[: trial - 1]:
            add(
                "zipf-1.0",
                "E5",
                kind,
                trial,
                "wordcount",
                runner="normal" if kind == "compiled" else kind,
            )
        settings = ["normal", "no-backups", "restart"]
        for setting in settings[trial - 1 :] + settings[: trial - 1]:
            add(
                "paper-sort",
                "paper",
                setting,
                trial,
                "sort",
                runner=setting,
                fraction=0.25 if setting == "restart" else None,
            )
    # Corpus variants keep their own identity; they never replace Zipf controls.
    for row in list(rows):
        if row["group"] == "zipf-1.0":
            rows.append({**row, "group": "crawl-eng", "setting": "corpus-" + row["setting"]})
    return rows


def execute(rows, args):
    selected = [row for row in rows if row["group"] == args.group]
    if any(row["fraction"] for row in selected) and not args.baseline_seconds:
        raise ValueError("timed faults require a full-size calibration baseline")
    env = dict(
        os.environ,
        SIZE="10000000000",
        M="160",
        SPACE_BUDGET_BYTES=str(args.space_budget_bytes),
        JOB_TIMEOUT_SECONDS="10800",
    )
    commit = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip()
    args.ledger.parent.mkdir(parents=True, exist_ok=True)
    with args.ledger.open("x") as ledger:
        for row in selected:
            variant = (
                "native"
                if row["scenario"] == "grep"
                else "zstdkv"
                if row["scenario"] == "sort"
                else "zstd"
            )
            trial_env = dict(
                env,
                WORKERS=str(row["workers"]),
                SORT_PROGRAM="papersort" if args.group == "paper-sort" else "distsort",
            )
            if row["scenario"] == "wordcount":
                trial_env["TEXT_SOURCE"] = row["group"]
            if row["fraction"]:
                trial_env["EVENT_AFTER"] = str(args.baseline_seconds * row["fraction"])
            command = ["bench/paper.sh", "run", row["scenario"], variant, row["runner_setting"]]
            print(json.dumps({"starting": row}), flush=True)
            completed = subprocess.run(
                command, cwd=ROOT, env=trial_env, stdout=subprocess.PIPE, text=True
            )
            if completed.returncode:
                raise RuntimeError(f"trial failed; stopped without advancing: {row}")
            directory = ROOT / completed.stdout.strip().splitlines()[-1]
            result = json.loads((directory / "result.json").read_text())
            evidence = json.loads((directory / "evidence.json").read_text())
            if (
                result["dirty"]
                or result["commit"] != commit
                or result["target_bytes"] != 10000000000
                or result["logical_bytes"] < 10000000000
                or result["workers"] != row["workers"]
                or not result["validation"]["verified"]
                or not evidence["verified"]
            ):
                raise ValueError("runner returned unverified evidence")
            ledger.write(
                json.dumps(
                    {
                        **row,
                        "directory": str(directory.relative_to(ROOT)),
                        "commit": result["commit"],
                    }
                )
                + "\n"
            )
            ledger.flush()
            os.fsync(ledger.fileno())
            print(json.dumps({"accepted": row, "wall_seconds": result["wall_seconds"]}), flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=("plan", "run"))
    parser.add_argument(
        "--group",
        choices=("grep", "sort", "zipf-0.5", "zipf-1.0", "zipf-1.5", "paper-sort", "crawl-eng"),
    )
    parser.add_argument("--space-budget-bytes", type=int)
    parser.add_argument("--baseline-seconds", type=float)
    parser.add_argument("--ledger", type=Path)
    args = parser.parse_args()
    rows = plan()
    if args.action == "plan":
        print(
            json.dumps(
                [r for r in rows if args.group is None or r["group"] == args.group], indent=2
            )
        )
    else:
        if (
            not args.group
            or not args.ledger
            or not args.space_budget_bytes
            or args.space_budget_bytes <= 0
        ):
            parser.error("run requires --group, --ledger and a positive --space-budget-bytes")
        if args.baseline_seconds is not None and not 0 < args.baseline_seconds < 10800:
            parser.error("baseline must be positive, finite, and less than the job timeout")
        execute(rows, args)


if __name__ == "__main__":
    main()
