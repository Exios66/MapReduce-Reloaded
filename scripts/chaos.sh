#!/usr/bin/env bash
# Chaos on the running Compose cluster (#29): the in-process TestChaos's
# events, on real containers. Each run submits word count over a Zipf input
# and, at random times while it runs, kills a worker (docker kill, then
# starts it again), pauses one (docker pause, 2 to 15 s: a straggler, or a
# lost worker past the 10 s timeout), or crashes and restarts the
# coordinator, which replays its write-ahead log. Every run must succeed with
# the same output as an undisturbed run of the same job.
#
# Usage: scripts/chaos.sh [RUNS [SEED]] (make chaos; defaults 5 and 1). Run
# i uses seed SEED+i-1, so a failing run is replayed with
# scripts/chaos.sh 1 SEED. SIZE (default 256 MiB) sets the input's size.
set -euo pipefail
cd "$(dirname "$0")/.."

runs=${1:-5}
seed=${2:-1}
size=${SIZE:-268435456}
compose=(docker compose -p mapreduce -f deploy/compose/compose.yaml)
mr=bin/mr
input=/data/input/chaos-$size
log() { echo "[$(date +%T)] $*" >&2; }

# The cluster goes back to four running workers and a running coordinator
# however the script ends.
restore() {
    local id
    for id in $("${compose[@]}" ps -aq worker coordinator); do
        docker unpause "$id" >/dev/null 2>&1 || true
    done
    "${compose[@]}" up -d coordinator worker >/dev/null 2>&1 || true
}
trap restore EXIT

if [ ! -d "data/input/chaos-$size" ]; then
    log "generating $size bytes of input"
    tmp=$(mktemp -d)
    python3 bench/gen-zipf.py "$size" 42 1.0 > "$tmp/zipf.txt"
    "$mr" upload "$tmp/zipf.txt" "$input" >/dev/null
    rm -r "$tmp"
fi

# job NAME: submits word count to /data/out/NAME and prints its ID.
job() { "$mr" submit -m 16 -r 4 wordcount "/data/out/$1" "$input/*.txt"; }

# wait_job ID: waits up to 600 s for the job to end, through coordinator
# restarts, and prints its state.
wait_job() {
    local state=
    for _ in $(seq 2400); do
        state=$("$mr" status "$1" 2>/dev/null) && state=${state%%$'\n'*} && state=${state##*$'\t'}
        case $state in SUCCEEDED | FAILED) break ;; esac
        sleep 0.25
    done
    echo "${state:-UNKNOWN}"
}

checksum() { "$mr" cat "/data/out/$1" | sha256sum | cut -d' ' -f1; }

# event KIND: one chaos event, logged so a failing seed can be read back.
# It runs in the script's own shell, not in $(...): bash reseeds RANDOM in a
# subshell, and the seed must decide every choice.
event() {
    local id ids
    mapfile -t ids < <("${compose[@]}" ps -q worker | sort)
    case $1 in
    kill)
        id=${ids[RANDOM % ${#ids[@]}]}
        log "  kill worker ${id:0:12}"
        docker kill "$id" >/dev/null
        sleep 1
        docker start "$id" >/dev/null
        ;;
    pause)
        local secs=$((2 + RANDOM % 14))
        id=${ids[RANDOM % ${#ids[@]}]}
        log "  pause worker ${id:0:12} for ${secs}s"
        docker pause "$id" >/dev/null
        sleep "$secs"
        docker unpause "$id" >/dev/null
        ;;
    restart)
        id=$("${compose[@]}" ps -q coordinator)
        log "  crash and restart the coordinator"
        docker kill "$id" >/dev/null # a crash: a clean stop tells the workers to exit
        docker start "$id" >/dev/null
        ;;
    esac
}

log "reference run"
start=$SECONDS
ref=chaos-ref-$(date +%s)
id=$(job "$ref")
state=$(wait_job "$id")
[ "$state" = SUCCEEDED ] || { echo "chaos: the reference run ended $state" >&2; exit 1; }
span=$((SECONDS - start))
want=$(checksum "$ref")
rm -r "data/out/$ref"
log "reference: ${span}s, output $want"

failed=()
for i in $(seq "$runs"); do
    s=$((seed + i - 1))
    RANDOM=$s
    name=chaos-$s-$(date +%s)
    log "run $i, seed $s"
    id=$(job "$name")
    start=$SECONDS
    # Three to five events, at whole seconds within the first 80% of the
    # reference time, one after another.
    n=$((3 + RANDOM % 3))
    times=()
    for _ in $(seq "$n"); do times+=($((1 + RANDOM % (span * 4 / 5 + 1)))); done
    mapfile -t times < <(printf '%s\n' "${times[@]}" | sort -n)
    for at in "${times[@]}"; do
        sleep $((at > SECONDS - start ? at - (SECONDS - start) : 0))
        case $((RANDOM % 3)) in
        0) event kill ;;
        1) event pause ;;
        2) event restart ;;
        esac
    done
    state=$(wait_job "$id")
    got=
    [ "$state" != SUCCEEDED ] || got=$(checksum "$name")
    if [ "$got" = "$want" ]; then
        log "run $i, seed $s: SUCCEEDED in $((SECONDS - start))s with the reference output"
    else
        log "run $i, seed $s: $state, output ${got:-none}; replay with scripts/chaos.sh 1 $s"
        failed+=("$s")
    fi
    rm -rf "data/out/$name"
    restore
done

if [ "${#failed[@]}" -gt 0 ]; then
    echo "chaos: ${#failed[@]} of $runs runs failed, seeds ${failed[*]}" >&2
    exit 1
fi
echo "chaos: $runs runs from seed $seed, each with the reference output"
