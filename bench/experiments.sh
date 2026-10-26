#!/usr/bin/env bash
# The performance evaluation (design.md, "Performance evaluation") on
# the Compose cluster: experiments E0 to E4, each run REPEAT times
# (default 3) on SIZE bytes of input (default 1 GiB) with M (default 32)
# and R (default 4) tasks.
#
# Usage: bench/experiments.sh E0|E1|E2|E3|E4|all
#
# - E0: grep, distsort and Zipf word count on 1, 2 and 4 workers.
# - E1: Zipf word count with exponents 0.5, 1.0 and 1.5, with the combiner
#   and without it (-combiner=false).
# - E2: distsort with one worker throttled to 0.2 CPU, with the
#   coordinator's backup tasks on and off, and with no straggler.
# - E3: distsort with one worker killed at 25%, 50% and 75% of the normal
#   job time, and with no kill.
# - E4: distsort with the coordinator's write-ahead log, restarted at 50% of
#   the normal job time, and with no restart.
#
# Each experiment writes bench/results/EXPERIMENT/: runs.csv, one row per
# run; machine.json, the commit and the machine; and, for the plots over
# time, tasks-LABEL.csv (scrape-tasks.py) or samples-LABEL.csv
# (scrape-metrics.py) per run. bench/plot-experiments.py draws the plots.
#
# Every run's output is checksummed and compared with the first normal run
# on the same input; a run that differs has valid=0 and is not plotted.
# The inputs stay in the output volume at /out/exp-input-PROGRAM-SIZE.txt
# (-SIZE-S.txt for word count's Zipf exponent S) between
# experiments, and make cluster-down removes them.
set -euo pipefail
cd "$(dirname "$0")/.."

repeat=${REPEAT:-3}
zipf=1.0      # word count's Zipf exponent; E1 varies it
submit_flags=() # extra mr submit flags; E1 sets -combiner=false
size=${SIZE:-1073741824}
m=${M:-32}
r=${R:-4}
interval=${INTERVAL:-0.5}
base=(docker compose -p mapreduce -f deploy/compose/compose.yaml)
compose=("${base[@]}")
metrics=http://127.0.0.1:18780/metrics
tmp=$(mktemp -d)
sampler=
slow= # the worker E2 throttles, restored however the script exits
cleanup() {
    [ -z "$sampler" ] || kill "$sampler" 2>/dev/null || true
    [ -z "$slow" ] || docker update --cpus 1 "$slow" >/dev/null 2>&1 || true
    rm -rf "$tmp"
}
trap cleanup EXIT

# Jobs are submitted from the mr container, which sees /out as the cluster
# does (the host client expands inputs on the host's filesystem), and polled
# with a host client, which is faster than a container per call.
(cd go && go build -o "$tmp/mr" ./cmd/mr)
mr=$tmp/mr

in_volume() { docker run --rm -v mapreduce_output:/out golang:1.26-alpine "$@"; }
log() { echo "[$(date +%T)] $*" >&2; }

# configure WORKERS [OVERRIDE.yaml]: restarts the cluster with WORKERS
# workers and a new coordinator, which reopens its log (E4 deletes the old
# one first), and waits until that many workers are registered and no lost
# one is still counted.
configure() {
    compose=("${base[@]}")
    [ -z "${2:-}" ] || compose+=(-f "deploy/compose/$2")
    "${compose[@]}" up -d --build --force-recreate --remove-orphans --scale worker="$1" coordinator worker >/dev/null 2>&1
    # A recreated coordinator tells its workers to exit; start them again.
    "${compose[@]}" up -d --scale worker="$1" coordinator worker >/dev/null 2>&1
    wait_workers "$1"
}

wait_workers() {
    local n
    for _ in $(seq 120); do
        n=$(curl -fsS "$metrics" 2>/dev/null | awk '$1 == "mr_workers" { print $2 }')
        [ "$n" = "$1" ] && return 0
        sleep 0.5
    done
    echo "experiments: expected $1 workers, the coordinator has ${n:-none}" >&2
    exit 1
}

# input PROGRAM: generates PROGRAM's input once and prints its path.
input() {
    # The size is in the name, so a run with another SIZE makes its own input.
    local path=/out/exp-input-$1-$size.txt gen
    case $1 in
    grep) gen=(bench/gen-grep.py "$size" 42) ;;
    distsort) gen=(bench/gen-sort.py "$size" 42) ;;
    wordcount)
        gen=(bench/gen-zipf.py "$size" 42 "$zipf")
        path=/out/exp-input-$1-$size-$zipf.txt
        ;;
    esac
    if ! in_volume test -f "$path"; then
        log "generating $size bytes for $1"
        python3 "${gen[@]}" > "$tmp/input.txt"
        "${compose[@]}" cp "$tmp/input.txt" "coordinator:$path" >/dev/null 2>&1
        rm "$tmp/input.txt"
    fi
    echo "$path"
}

params() { [ "$1" = grep ] && echo "-param pattern=XYZ" || true; }

checksum() { in_volume sh -c "cat $1/part-r-* | sha256sum | cut -d' ' -f1"; }

# valid INPUT OUTPUT: the first normal run on each input sets its reference
# checksum, which every later run on that input must match.
valid() {
    local sum ref=$tmp/ref-${1##*/}
    sum=$(checksum "$2")
    [ -f "$ref" ] || echo "$sum" > "$ref"
    [ "$sum" = "$(cat "$ref")" ] && echo 1 || echo 0
}

counter() { awk -v name="$1" '$1 == name { print $2 }' "$tmp/status"; }

start_sampler() { # tasks|bytes FILE
    if [ "$1" = tasks ]; then
        python3 bench/scrape-tasks.py "$interval" 86400 "$metrics" > "$2" &
    else
        local urls=() id ip
        for id in $("${compose[@]}" ps -q worker); do
            ip=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$id")
            urls+=("http://$ip:8081/metrics")
        done
        python3 bench/scrape-metrics.py "$interval" 86400 "${urls[@]}" > "$2" &
    fi
    sampler=$!
}

stop_sampler() {
    [ -n "$sampler" ] || return 0
    sleep "$(awk -v i="$interval" 'BEGIN { print 2 * i }')"
    kill "$sampler"
    wait "$sampler" 2>/dev/null || true
    sampler=
}

# run EXPERIMENT SETTING PROGRAM RUN WORKERS EVENT_AT [EVENT]: runs one job
# and appends its row to runs.csv. EVENT is a command run in the background
# EVENT_AT seconds after the job is submitted. The job is polled with
# mr status, which, unlike submit -wait, outlives a coordinator restart.
run() {
    local exp=$1 setting=$2 program=$3 n=$4 workers=$5 at=$6 event=${7:-}
    local dir=bench/results/$exp out=/out/exp-$1-$2-$4 in start end state job
    in=$(input "$program")
    in_volume rm -rf "$out"
    log "$exp $setting $program run $n"
    # Timed from submission, after the mr container has started.
    # shellcheck disable=SC2046
    job=$("${compose[@]}" run --rm -q mr submit -coordinator coordinator:7070 -m "$m" -r "$r" \
        $(params "$program") "${submit_flags[@]}" "$program" "$out" "$in" 2>/dev/null | tail -1)
    if [ -z "$job" ]; then
        echo "experiments: mr submit failed for $exp $setting run $n" >&2
        exit 1
    fi
    start=$(date +%s.%N)
    local pid=
    if [ -n "$event" ]; then
        (sleep "$at" && eval "$event" >/dev/null 2>&1) &
        pid=$!
    fi
    state=
    local deadline=$((SECONDS + 900))
    while [ "$state" != SUCCEEDED ] && [ "$state" != FAILED ]; do
        if [ "$SECONDS" -ge "$deadline" ]; then
            state=TIMEOUT
            echo "x TIMEOUT" > "$tmp/status"
            break
        fi
        sleep 0.25
        "$mr" status "$job" > "$tmp/status" 2>/dev/null || continue
        state=$(awk 'NR == 1 { print $2 }' "$tmp/status")
    done
    end=$(date +%s.%N)
    # An event still pending missed the job: cancel it, and do not count the run.
    local missed=0
    if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
        kill "$pid" 2>/dev/null || true
        missed=1
        log "$exp $setting run $n finished before its event at ${at}s; marked invalid"
    fi
    [ -z "$pid" ] || wait "$pid" 2>/dev/null || true
    echo "$exp,$setting,$program,$workers,$n,$size,$m,$r,$at,$state,$(counter seconds)," \
        "$(awk -v s="$start" -v e="$end" 'BEGIN { printf "%.3f", e - s }'),$(counter backups),$(counter backups_won)," \
        "$(counter map.output.bytes),$(if [ "$state" = SUCCEEDED ] && [ "$missed" = 0 ]; then valid "$in" "$out"; else echo 0; fi),$job" |
        tr -d ' ' >> "$dir/runs.csv"
    in_volume rm -rf "$out"
}

begin() { # EXPERIMENT
    local dir=bench/results/$1
    mkdir -p "$dir"
    echo "experiment,setting,program,workers,run,input_bytes,m,r,event_at,state,seconds,wall_seconds,backups,backups_won,map_output_bytes,valid,job" > "$dir/runs.csv"
    python3 - "$(git rev-parse HEAD)" "$(git status --porcelain | wc -l)" "$(docker version --format '{{.Server.Version}}')" > "$dir/machine.json" <<'EOF'
import json, os, platform, sys
cpu = next((l.split(":", 1)[1].strip() for l in open("/proc/cpuinfo") if l.startswith("model name")), "")
mem = next(int(l.split()[1]) * 1024 for l in open("/proc/meminfo") if l.startswith("MemTotal:"))
json.dump({"commit": sys.argv[1], "dirty": int(sys.argv[2]) > 0, "cpus": os.cpu_count(), "cpu": cpu,
           "memory_bytes": mem, "kernel": platform.release(), "docker": sys.argv[3]}, sys.stdout, indent=2)
print()
EOF
}

# The median wall time of an experiment's normal runs.
median() { awk -F, -v s="$2" '$2 == s && $16 == 1 { print $12 }' "bench/results/$1/runs.csv" | sort -n | awk '{ a[NR] = $1 } END { print a[int((NR + 1) / 2)] }'; }

e0() {
    begin E0
    for program in grep distsort wordcount; do
        for workers in 4 2 1; do
            configure "$workers"
            for n in $(seq "$repeat"); do run E0 "w$workers" "$program" "$n" "$workers" ""; done
        done
    done
}

# E1 pairs the settings in each run and swaps which goes first on even runs,
# so neither always gets the page cache the other warmed. Both give the same
# output, so whichever runs first sets the reference.
e1() {
    begin E1
    configure 4
    local n combiner order
    for zipf in 0.5 1.0 1.5; do
        for n in $(seq "$repeat"); do
            order="on off"
            [ $((n % 2)) = 1 ] || order="off on"
            for combiner in $order; do
                submit_flags=()
                [ "$combiner" = on ] || submit_flags=(-combiner=false)
                run E1 "s$zipf-$combiner" wordcount "$n" 4 ""
            done
        done
    done
    zipf=1.0 submit_flags=()
}

e2() {
    begin E2
    for setting in normal straggler-backups straggler-no-backups; do
        if [ "$setting" = straggler-no-backups ]; then configure 4 backups-off.yaml; else configure 4; fi
        for n in $(seq "$repeat"); do
            slow=
            [ "$setting" = normal ] || slow=$("${compose[@]}" ps -q worker | head -1)
            [ -z "$slow" ] || docker update --cpus 0.2 "$slow" >/dev/null
            start_sampler tasks "bench/results/E2/tasks-$setting-$n.csv"
            run E2 "$setting" distsort "$n" 4 ""
            stop_sampler
            [ -z "$slow" ] || docker update --cpus 1 "$slow" >/dev/null
            slow=
        done
    done
    configure 4
}

e3() {
    begin E3
    configure 4
    for n in $(seq "$repeat"); do
        start_sampler bytes "bench/results/E3/samples-normal-$n.csv"
        run E3 normal distsort "$n" 4 ""
        stop_sampler
    done
    local t at
    t=$(median E3 normal)
    for pct in 25 50 75; do
        at=$(awk -v t="$t" -v p="$pct" 'BEGIN { printf "%.1f", t * p / 100 }')
        for n in $(seq "$repeat"); do
            local victim
            victim=$("${compose[@]}" ps -q worker | tail -1)
            start_sampler bytes "bench/results/E3/samples-kill-$pct-$n.csv"
            run E3 "kill-$pct" distsort "$n" 4 "$at" "docker kill $victim"
            stop_sampler
            "${compose[@]}" up -d --scale worker=4 worker >/dev/null 2>&1
            wait_workers 4
        done
    done
}

e4() {
    begin E4
    in_volume rm -rf /out/wal
    configure 4 wal.yaml
    for n in $(seq "$repeat"); do
        start_sampler tasks "bench/results/E4/tasks-normal-$n.csv"
        run E4 normal distsort "$n" 4 ""
        stop_sampler
    done
    local t at coordinator
    t=$(median E4 normal)
    at=$(awk -v t="$t" 'BEGIN { printf "%.1f", t / 2 }')
    coordinator=$("${compose[@]}" ps -q coordinator)
    for n in $(seq "$repeat"); do
        start_sampler tasks "bench/results/E4/tasks-restart-$n.csv"
        # SIGKILL, so the coordinator crashes instead of telling its workers to exit.
        run E4 restart distsort "$n" 4 "$at" "docker kill $coordinator && docker start $coordinator"
        stop_sampler
        wait_workers 4
    done
    configure 4
    in_volume rm -rf /out/wal
}

case ${1:-} in
E0) e0 ;;
E1) e1 ;;
E2) e2 ;;
E3) e3 ;;
E4) e4 ;;
all) e0 && e1 && e2 && e3 && e4 ;;
*)
    echo "usage: bench/experiments.sh E0|E1|E2|E3|E4|all" >&2
    exit 2
    ;;
esac
log "done"
