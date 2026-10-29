#!/usr/bin/env bash
# Runs one of the paper's benchmarks (#57) on the Compose cluster and writes
# its data rates over time, its counters and the machine it ran on to
# bench/results/SCENARIO-STAMP/.
#
# Usage: bench/run.sh grep|sort|zipf|book
#
# grep runs the grep example over bench/gen-grep.py's records for the
# pattern XYZ (§5.2), sort runs distsort over bench/gen-sort.py's records
# (§5.3), zipf runs word count over bench/gen-zipf.py's skewed words, and
# book runs it over bench/gen-book.py's lines of War and Peace, repeated to
# SIZE, a fixed vocabulary (scripts/fetch-data.sh warpeace fetches the book).
# The environment sets the run: WORKERS (default 4), SIZE in bytes (default
# 256 MiB), M (default 16) and R (default 4) tasks, SEED (default 42) and
# INTERVAL, the seconds between samples of /metrics (default 0.5).
#
# The cluster is started, or scaled, to WORKERS workers. While the job runs,
# scrape-metrics.py samples every worker's /metrics from the host, at the
# worker containers' addresses; seconds in the samples count from just
# before the job is submitted. The run's directory holds:
#
# - samples.csv: the samples, from scrape-metrics.py;
# - rates.csv: the input, shuffle and output rates, from plot-rates.py;
# - rates.png: the same in three panels, titled with the run's program, size,
#   M, R and commit, if matplotlib is installed;
# - status.txt: what mr submit -wait printed;
# - result.json: the run's settings, the job's counters and the machine.
#
# The input is written once, to the host's data/ directory, which every
# container sees at /data (ADR 0016), so a 10 GB input needs 10 GB of disk,
# not twice that. The input and the job's output are removed at the end. If the job fails, the files are still written, and run.sh exits
# with mr's status.
set -euo pipefail
cd "$(dirname "$0")/.."

scenario=${1:-}
case $scenario in
grep) gen=gen-grep.py program=grep params=(-param pattern=XYZ) ;;
sort) gen=gen-sort.py program=distsort params=() ;;
zipf) gen=gen-zipf.py program=wordcount params=() ;;
book) gen=gen-book.py program=wordcount params=() ;;
*)
    echo "usage: bench/run.sh grep|sort|zipf|book" >&2
    exit 2
    ;;
esac
workers=${WORKERS:-4}
size=${SIZE:-268435456}
m=${M:-16}
r=${R:-4}
seed=${SEED:-42}
interval=${INTERVAL:-0.5}

compose=(docker compose -p mapreduce -f deploy/compose/compose.yaml)
# The process ID keeps runs that start in the same second apart.
stamp=$(date -u +%Y%m%dT%H%M%SZ)-$$
host_input=data/bench-input-$scenario-$stamp.txt
input=/data/bench-input-$scenario-$stamp.txt
dir=bench/results/$scenario-$stamp
out=/out/bench-$scenario-$stamp
scraper=
# shellcheck disable=SC2317 # called by cleanup, from the EXIT trap
rm_out() { docker run --rm -v mapreduce_output:/out golang:1.26-alpine rm -rf "$@"; }
# shellcheck disable=SC2317 # called by the EXIT trap
cleanup() {
    [ -z "$scraper" ] || kill "$scraper" 2>/dev/null || true
    rm -f "$host_input"
    rm_out "$out"
}
trap cleanup EXIT

mkdir -p data
chmod a+x data
"${compose[@]}" up -d --build --scale worker="$workers"
mkdir -p "$dir"

python3 "bench/$gen" "$size" "$seed" > "$host_input"
chmod a+r "$host_input"
input_bytes=$(wc -c < "$host_input")

urls=()
for id in $("${compose[@]}" ps -q worker); do
    ip=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$id")
    urls+=("http://$ip:8081/metrics")
done

# Background jobs of a non-interactive shell ignore SIGINT, so the scraper
# is stopped with SIGTERM; it flushes its rows after every round.
python3 bench/scrape-metrics.py "$interval" 86400 "${urls[@]}" > "$dir/samples.csv" &
scraper=$!
start=$(date +%s.%N)
# A failed job makes mr exit nonzero after printing its status. Its samples
# and counters are still worth recording, so the exit status is kept for the end.
status=0
"${compose[@]}" run --rm mr submit -coordinator coordinator:7070 -m "$m" -r "$r" -wait \
    "${params[@]}" "$program" "$out" "$input" > "$dir/status.txt" || status=$?
end=$(date +%s.%N)
# Two more rounds, so the samples hold the counters of the last tasks.
sleep "$(awk -v i="$interval" 'BEGIN { print 2 * i }')"
kill "$scraper"
wait "$scraper" 2>/dev/null || true
scraper=

python3 bench/plot-rates.py "$dir/samples.csv" > "$dir/rates.csv"
if python3 -c "import matplotlib" 2>/dev/null; then
    python3 bench/plot-rates.py "$dir/samples.csv" "$dir/rates.png" \
        "$program, $size bytes, M = $m, R = $r, workers = $workers, commit $(git rev-parse --short HEAD)"
fi

SCENARIO=$scenario PROGRAM=$program WORKERS=$workers SIZE=$size M=$m R=$r SEED=$seed \
    INTERVAL=$interval INPUT_BYTES=$input_bytes START=$start END=$end \
    DOCKER=$(docker version --format '{{.Server.Version}}') \
    COMMIT=$(git rev-parse HEAD) DIRTY=$(git status --porcelain | wc -l) \
    python3 - "$dir/status.txt" > "$dir/result.json" <<'EOF'
import json, os, platform, sys

env = os.environ


def number(s):
    try:
        return int(s)
    except ValueError:
        try:
            return float(s)
        except ValueError:
            return s


def meminfo():
    with open("/proc/meminfo") as f:
        for line in f:
            if line.startswith("MemTotal:"):
                return int(line.split()[1]) * 1024
    return None


def cpu():
    with open("/proc/cpuinfo") as f:
        for line in f:
            if line.startswith("model name"):
                return line.partition(":")[2].strip()
    return platform.processor()


# The first line is the job's id and state, each other a name and value.
with open(sys.argv[1]) as f:
    lines = [line.split() for line in f if line.strip()]
# mr prints nothing if it could not reach the coordinator.
job = {"id": lines[0][0], "state": lines[0][1]} if lines else {}
job.update((name, number(value)) for name, value in lines[1:])
json.dump(
    {
        "scenario": env["SCENARIO"],
        "program": env["PROGRAM"],
        "workers": int(env["WORKERS"]),
        "size": int(env["SIZE"]),
        "input_bytes": int(env["INPUT_BYTES"]),
        "m": int(env["M"]),
        "r": int(env["R"]),
        "seed": int(env["SEED"]),
        "interval": float(env["INTERVAL"]),
        "wall_seconds": round(float(env["END"]) - float(env["START"]), 3),
        "job": job,
        "commit": env["COMMIT"],
        "dirty": int(env["DIRTY"]) > 0,
        "machine": {
            "cpus": os.cpu_count(),
            "cpu": cpu(),
            "memory_bytes": meminfo(),
            "kernel": platform.release(),
            "docker": env["DOCKER"],
        },
    },
    sys.stdout,
    indent=2,
)
print()
EOF
echo "$dir"
exit "$status"
