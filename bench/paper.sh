#!/usr/bin/env bash
# Fixed-VM campaign frontend: reuse the host CLI, generators, MRKV and samplers.
# Usage: bench/paper.sh prepare|run grep|sort native|zstdkv [normal|no-backups|restart]
# Requires explicit VM handover; flock also excludes other paper invocations.
set -euo pipefail
cd "$(dirname "$0")/.."
exec 9> "$HOME/.mapreduce-paper.lock"
flock -n 9 || { echo "paper: another campaign command owns the VM" >&2; exit 1; }

action=${1:-} scenario=${2:-} variant=${3:-} setting=${4:-normal}
size=${SIZE:-10000000000} m=${M:-160} interval=${INTERVAL:-0.5}
deadline=${JOB_TIMEOUT_SECONDS:-3600}
case "$action:$scenario:$variant:$setting" in
    prepare:grep:native:normal|prepare:sort:native:normal|prepare:grep:zstdkv:normal|prepare:sort:zstdkv:normal) ;;
    run:grep:native:normal|run:grep:zstdkv:normal|run:sort:native:normal|run:sort:zstdkv:normal|run:sort:native:no-backups|run:sort:zstdkv:no-backups|run:sort:native:restart|run:sort:zstdkv:restart) ;;
    *) echo "usage: bench/paper.sh prepare|run grep|sort native|zstdkv [normal|no-backups|restart]" >&2; exit 2 ;;
esac
for n in "$size" "$m" "$deadline"; do
    [[ "$n" =~ ^[1-9][0-9]*$ ]] || { echo "size, M and deadline must be positive integers" >&2; exit 2; }
done
((size % 100 == 0 && size / 100 >= m)) || { echo "SIZE must contain at least M whole 100-byte records" >&2; exit 2; }
[[ "$interval" =~ ^[0-9]+([.][0-9]+)?$ ]] && awk -v i="$interval" 'BEGIN { exit !(i > 0) }' || exit 2
if [ "$size" = 10000000000 ] && [ "$m" != 160 ]; then echo "10 GB campaign requires M=160" >&2; exit 2; fi
if [ "$setting" = restart ]; then
    [[ "${EVENT_AFTER:-}" =~ ^[0-9]+([.][0-9]+)?$ ]] || { echo "restart requires EVENT_AFTER in seconds" >&2; exit 2; }
fi
data="data/paper-input-$scenario-$variant-$size-$m"
format=text codec=none
[ "$variant" = native ] || { format=kv; codec=zstd; }
tool=bin/paper-data
mr=bin/mr
[[ -x "$tool" && -x "$mr" ]] || { echo "run make paper-tools first" >&2; exit 2; }
free_bytes() { df -B1 . | awk 'NR == 2 { print $4 }'; }
require_space() {
    local free
    free=$(free_bytes)
    if [[ ! "$free" =~ ^[0-9]+$ ]] || [ "$free" -lt "$1" ]; then
        echo "paper: need $1 free bytes, have ${free:-unknown}; no job started" >&2
        exit 1
    fi
}

if [ "$action" = prepare ]; then
    [ ! -e "$data" ] || { echo "paper: input already exists: $data; not overwritten" >&2; exit 1; }
    # Preparation alone must leave operational reserve; execution has a separate gate.
    require_space "$((size + (4 << 30)))"
    mkdir -p data
    chmod a+x data
    staging=$data.partial-$$
    trap 'echo "paper: incomplete preparation retained at $staging if present" >&2' ERR
    python3 "bench/gen-$scenario.py" "$size" 42 | "$tool" prepare -records "$((size / 100))" -shards "$m" -format "$format" "$staging"
    mv -T "$staging" "$data"
    echo "$data/manifest.json"
    exit 0
fi

[ -f "$data/manifest.json" ] || { echo "paper: prepare $scenario $variant first" >&2; exit 2; }
physical=$(python3 - "$data/manifest.json" "$size" "$m" "$format" <<'PY'
import json, sys
x = json.load(open(sys.argv[1]))
assert x['logical_bytes'] == int(sys.argv[2]) and x['shards'] == int(sys.argv[3])
assert x['format'] == sys.argv[4]
print(x['physical_bytes'])
PY
)
budget=${SPACE_BUDGET_BYTES:-$((4 << 30))}
if [ "$scenario" = sort ] && [ -z "${SPACE_BUDGET_BYTES:-}" ]; then
    budget=$((physical * 3 + (4 << 30)))
fi
[[ "$budget" =~ ^[1-9][0-9]*$ ]] || { echo "SPACE_BUDGET_BYTES must be positive" >&2; exit 2; }
require_space "$budget"
stamp=$(date -u +%Y%m%dT%H%M%SZ)-$$
dir=bench/results/paper-$scenario-$variant-$setting-$stamp
out=/data/out/paper-$stamp
host_out=data/out/paper-$stamp
mkdir -p "$dir" data/out
chmod a+rwx data/out
compose=(docker compose -p mapreduce -f deploy/compose/compose.yaml -f "$dir/compose.json")
samplers=() event_pid='' owned=0 finished=0
cleanup() {
    local pid
    if [ -n "$event_pid" ]; then
        pkill -TERM -P "$event_pid" 2>/dev/null || true
        kill "$event_pid" 2>/dev/null || true
        wait "$event_pid" 2>/dev/null || true
    fi
    for pid in "${samplers[@]}"; do kill "$pid" 2>/dev/null || true; done
    for pid in "${samplers[@]}"; do wait "$pid" 2>/dev/null || true; done
    if [ "$owned" = 1 ]; then
        "${compose[@]}" logs --no-color coordinator worker > "$dir/cluster.log" 2>&1 || true
        "${compose[@]}" stop worker coordinator > "$dir/stop.log" 2>&1 || true
    fi
    [ "$finished" = 1 ] || echo "paper: stopped; inputs, output and per-run WAL retained; inspect $dir" >&2
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
python3 - "$dir/compose.json" "$stamp" "$setting" <<'PY'
import json, sys
coordinator = ['coordinator', '-addr', ':7070', '-http', ':8080', '-data-dir', '/data/paper-wal-' + sys.argv[2]]
if sys.argv[3] == 'no-backups': coordinator += ['-backups=false']
worker = ['worker', '-coordinator', 'coordinator:7070', '-http', ':8081', '-min-free-mib', '4096']
json.dump({'services': {'coordinator': {'command': coordinator}, 'worker': {'command': worker, 'mem_limit': '768m', 'cpus': 1}}}, open(sys.argv[1], 'w'))
PY
# Every trial has a fresh WAL: never replay a timed-out campaign accidentally.
mkdir "data/paper-wal-$stamp"
chmod a+rwx "data/paper-wal-$stamp"
"${compose[@]}" config --format json > "$dir/effective-compose.json"
owned=1
"${compose[@]}" up -d --force-recreate --scale worker=4 coordinator worker > "$dir/start.log" 2>&1
"${compose[@]}" up -d --scale worker=4 coordinator worker >> "$dir/start.log" 2>&1
registered=0
for _ in $(seq 120); do
    registered=$(curl -fsS --max-time 2 http://127.0.0.1:18780/metrics | awk '$1 == "mr_workers" { print $2 }') || registered=0
    [ "$registered" = 4 ] && break
    sleep 0.5
done
[ "$registered" = 4 ] || { echo "paper: four workers did not register" >&2; exit 1; }
ids=() urls=()
while IFS= read -r id; do
    ids+=("$id")
    ip=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$id")
    urls+=("http://$ip:8081/metrics")
done < <("${compose[@]}" ps -q worker)
docker inspect "${ids[@]}" > "$dir/workers.json"
docker image inspect mapreduce-reloaded > "$dir/image.json"
python3 - "$dir" "$(git rev-parse HEAD)" <<'PY'
import hashlib, json, subprocess, sys
from pathlib import Path
p = Path(sys.argv[1])
stamp = json.load(open('bin/paper-build.json'))
assert stamp['commit'] == sys.argv[2], 'stale build: run make paper-tools'
assert not subprocess.check_output(['git', 'status', '--porcelain', '--untracked-files=no']).strip(), 'tracked source changed after build'
for name, want in stamp['tools'].items():
    assert hashlib.sha256(Path(name).read_bytes()).hexdigest() == want, 'stale tool: ' + name
assert json.load(open(p/'image.json'))[0]['Id'] == stamp['image']
assert all(w['Image'] == stamp['image'] and w['HostConfig']['NanoCpus'] == 1000000000 and w['HostConfig']['Memory'] == 768*1024**2 for w in json.load(open(p/'workers.json')))
(p/'build.json').write_text(json.dumps(stamp, indent=2))
PY
read -r clock_epoch MR_BENCH_START_MONOTONIC < <(python3 -c 'import time; print(time.time(), time.monotonic())')
export MR_BENCH_START_MONOTONIC
printf '{"epoch":%s,"monotonic":%s}\n' "$clock_epoch" "$MR_BENCH_START_MONOTONIC" > "$dir/clock.json"
python3 bench/scrape-metrics.py "$interval" "$deadline" "${urls[@]}" > "$dir/samples.csv" &
samplers+=("$!")
python3 bench/scrape-tasks.py "$interval" "$deadline" http://127.0.0.1:18780/metrics > "$dir/tasks.csv" &
samplers+=("$!")
(
    while true; do
        docker stats --no-stream --format '{"epoch":"'"$(date +%s.%N)"'","name":{{json .Name}},"memory":{{json .MemUsage}},"cpu":{{json .CPUPerc}}}' "${ids[@]}" || true
    done
) > "$dir/resources.jsonl" 2> "$dir/resources-errors.log" &
samplers+=("$!")
# Do not submit until all four workers supplied a baseline.
baseline=0
for _ in $(seq 40); do
    for pid in "${samplers[@]}"; do kill -0 "$pid" || { echo "paper: sampler died" >&2; exit 1; }; done
    if python3 - "$dir/samples.csv" <<'PY'
import csv, sys
rows = list(csv.DictReader(open(sys.argv[1])))
sys.exit(len({r['url'] for r in rows}) != 4)
PY
    then baseline=1; break; fi
    sleep 0.25
done
[ "$baseline" = 1 ] || { echo "paper: missing worker sample baselines" >&2; exit 1; }
params=() r=8 program=papersort
if [ "$scenario" = grep ]; then params=(-param pattern=XYZ); r=1; program='grep'; fi
read -r start start_mono < <(python3 -c 'import time; print(time.time(), time.monotonic())')
job=$(timeout "$deadline" "$mr" submit -m "$m" -r "$r" -input-format "$format" -intermediate-codec "$codec" -output-format "$format" -output-codec "$codec" "${params[@]}" "$program" "$out" "/data/${data#data/}/part-m-*" 2> "$dir/submit.log")
[[ "$job" =~ ^job-[a-z0-9-]+$ ]] || { echo "paper: invalid submitted job ID: $job" >&2; exit 1; }
submitted=$(date +%s.%N)
printf 'event,epoch,exit_code\nsubmitted,%s,0\n' "$submitted" > "$dir/events.csv"
if [ "$setting" = restart ]; then
    (
        sleep "$EVENT_AFTER"
        timeout 5 "$mr" status "$job" > "$dir/event-status.txt"
        [ "$(awk 'NR == 1 { print $2 }' "$dir/event-status.txt")" = RUNNING ] || exit 1
        printf 'kill_requested,%s,0\n' "$(date +%s.%N)" >> "$dir/events.csv"
        timeout 5 docker kill "${ids[0]}" >> "$dir/event.log" 2>&1 || exit 1
        printf 'killed,%s,0\n' "$(date +%s.%N)" >> "$dir/events.csv"
        timeout 5 docker start "${ids[0]}" >> "$dir/event.log" 2>&1 || exit 1
        printf 'restarted,%s,0\n' "$(date +%s.%N)" >> "$dir/events.csv"
        # SIGKILL leaves the old registration until its timeout. The fifth
        # registration proves its replacement registered promptly, not just started.
        for _ in $(seq 16); do
            count=$(curl -fsS --max-time 1 http://127.0.0.1:18780/metrics | awk '$1 == "mr_workers" { print $2 }')
            if [ "$count" = 5 ]; then
                printf 'registered,%s,0\n' "$(date +%s.%N)" >> "$dir/events.csv"
                exit 0
            fi
            sleep 0.25
        done
        exit 1
    ) &
    event_pid=$!
fi
printf 'epoch,free_bytes\n' > "$dir/disk.csv"
state=
while [ "$state" != SUCCEEDED ] && [ "$state" != FAILED ]; do
    free=$(free_bytes)
    printf '%s,%s\n' "$(date +%s.%N)" "$free" >> "$dir/disk.csv"
    if [[ ! "$free" =~ ^[0-9]+$ ]] || [ "$free" -lt $((2 << 30)) ]; then
        echo "paper: emergency disk reserve reached" >&2
        exit 1
    fi
    remaining=$(python3 -c 'import sys,time; print(max(0,float(sys.argv[1])+float(sys.argv[2])-time.monotonic()))' "$start_mono" "$deadline")
    awk -v r="$remaining" 'BEGIN { exit !(r > 0) }' || { echo "paper: TIMEOUT; stopping and retaining data" >&2; exit 1; }
    poll_timeout=$(awk -v r="$remaining" 'BEGIN { print r < 5 ? r : 5 }')
    if timeout "$poll_timeout" "$mr" status "$job" > "$dir/status.pending" 2> "$dir/status-errors.log"; then
        mv "$dir/status.pending" "$dir/status.txt"
        state=$(awk 'NR == 1 { print $2 }' "$dir/status.txt")
    fi
    case "$state" in SUCCEEDED|FAILED) ;; *) sleep "$(awk -v i="$interval" -v r="$remaining" 'BEGIN { print i < r ? i : r }')" ;; esac
done
read -r end end_mono < <(python3 -c 'import time; print(time.time(), time.monotonic())')
[ "$state" = SUCCEEDED ] || { echo "paper: job failed" >&2; exit 1; }
if [ -n "$event_pid" ]; then
    if kill -0 "$event_pid" 2>/dev/null; then echo "paper: job ended before replacement completed; invalid trial" >&2; exit 1; fi
    wait "$event_pid" || { echo "paper: failure injection failed; invalid trial" >&2; exit 1; }
    event_pid=
    grep -q '^registered,' "$dir/events.csv"
    python3 - "$dir/events.csv" "$dir/status.txt" "$start" <<'PY'
import csv, sys
events = {r['event']: float(r['epoch']) for r in csv.DictReader(open(sys.argv[1]))}
status = dict(line.split(None, 1) for line in open(sys.argv[2]) if line.strip())
assert events['killed'] < float(sys.argv[3]) + float(status['seconds']), 'worker killed after possible job completion'
PY
fi
sleep "$(awk -v i="$interval" 'BEGIN { print 2*i }')"
# Finalize samples before plotting; failed collectors invalidate the trial.
for pid in "${samplers[@]}"; do kill -0 "$pid" || { echo "paper: sampler died" >&2; exit 1; }; done
for pid in "${samplers[@]}"; do kill "$pid"; done
for pid in "${samplers[@]}"; do wait "$pid" 2>/dev/null || true; done
samplers=()
python3 - "$dir/samples.csv" <<'PY'
import csv, sys
rows = list(csv.DictReader(open(sys.argv[1])))
by = {}
for r in rows: by.setdefault(r['url'], set()).add(r['seconds'])
assert len(by) == 4 and all(len(times) >= 2 for times in by.values()), 'incomplete rate coverage'
PY
# Stop/join every attempt, including speculative losers, before validation/deletion.
"${compose[@]}" logs --no-color coordinator worker > "$dir/cluster.log" 2>&1
"${compose[@]}" stop worker coordinator > "$dir/stop.log" 2>&1
owned=0
docker inspect "${ids[@]}" > "$dir/workers-after.json"
if grep -q '"OOMKilled": true' "$dir/workers-after.json"; then echo "paper: OOM kill; invalid trial" >&2; exit 1; fi
"$tool" verify -kind "$scenario" -format "$format" -parts "$r" -manifest "$data/manifest.json" "$host_out" > "$dir/validation.json"
cp "$data/manifest.json" "$dir/input-manifest.json"
python3 bench/plot-rates.py "$dir/samples.csv" > "$dir/rates.csv"
if python3 -c 'import matplotlib' 2>/dev/null; then python3 bench/plot-rates.py "$dir/samples.csv" "$dir/rates.png"; fi
python3 - "$dir" "$scenario" "$variant" "$setting" "$size" "$m" "$r" "$start" "$submitted" "$end" "$budget" "$(git rev-parse HEAD)" "$(git status --porcelain | wc -l)" "$start_mono" "$end_mono" <<'PY'
import json, os, platform, sys
from pathlib import Path
p = Path(sys.argv[1])
status = dict(line.split(None, 1) for line in (p/'status.txt').read_text().splitlines() if line.strip())
assert status['map'] == f'{sys.argv[6]}/{sys.argv[6]}'
assert status['reduce'] == f'{sys.argv[7]}/{sys.argv[7]}'
record = dict(scenario=sys.argv[2], variant=sys.argv[3], setting=sys.argv[4], logical_bytes=int(sys.argv[5]), m=int(sys.argv[6]), r=int(sys.argv[7]), start_epoch=float(sys.argv[8]), submitted_epoch=float(sys.argv[9]), end_epoch=float(sys.argv[10]), wall_seconds=float(sys.argv[15])-float(sys.argv[14]), space_budget_bytes=int(sys.argv[11]), commit=sys.argv[12], dirty=int(sys.argv[13]) != 0, status=status, validation=json.load(open(p/'validation.json')), cpus=os.cpu_count(), kernel=platform.release())
json.dump(record, open(p/'result.json', 'w'), indent=2)
PY
# Only this unique, stopped and independently verified run's output is removed.
rm -rf -- "$host_out"
finished=1
echo "$dir"
