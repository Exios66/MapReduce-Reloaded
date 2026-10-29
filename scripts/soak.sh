#!/usr/bin/env bash
# The soak run of #160, the acceptance criterion of #47: word count over 1 GiB
# with the combiner off on two workers limited to 256 MB each. The map output
# is more than the limit, so the workers must spill; no container may be
# killed for exceeding its memory limit.
#
# It uses deploy/compose/soak.yaml, which lowers the worker limit to 256 MB.
# The input is cached in data/input/ between runs; make cluster-down removes
# it. Size and workers can be overridden: SIZE=... WORKERS=... make soak.
set -euo pipefail
cd "$(dirname "$0")/.."

compose=(docker compose -p mapreduce -f deploy/compose/compose.yaml -f deploy/compose/soak.yaml)
size=${SIZE:-1073741824}
workers=${WORKERS:-2}
job=/data/out/soak-$(date +%s)

"${compose[@]}" up -d --build --scale worker="$workers" coordinator worker >/dev/null

# The coordinator's /metrics counts registered workers; wait for all of them.
workers_seen=
for _ in $(seq 120); do
    workers_seen=$(curl -fsS http://127.0.0.1:18780/metrics 2>/dev/null | awk '/^mr_workers /{print $2}') || workers_seen=
    [ "${workers_seen:-0}" -ge "$workers" ] && break
    sleep 1
done
if [ "${workers_seen:-0}" -lt "$workers" ]; then
    echo "soak: the coordinator sees ${workers_seen:-no} workers, want $workers" >&2
    exit 1
fi

input="data/input/soak-input-$size.txt"
if [ ! -f "$input" ]; then
    mkdir -p data/input
    bench/gen-zipf.py "$size" 42 1.0 > "$input"
fi

result=$("${compose[@]}" run --rm -q mr submit -coordinator coordinator:7070 \
    -m 32 -r 4 -combiner=false -wait wordcount "$job" "/data/input/${input##*/}")
printf '%s\n' "$result"
case "$result" in
*SUCCEEDED*) ;;
*) echo "soak: the job did not succeed" >&2; exit 1 ;;
esac

containers=$("${compose[@]}" ps -aq)
[ -n "$containers" ] || { echo "soak: no cluster containers to inspect" >&2; exit 1; }
# shellcheck disable=SC2086 # one ID per word
oom=$(docker inspect -f '{{.Name}} {{.State.OOMKilled}}' $containers | grep ' true$' || true)
if [ -n "$oom" ]; then
    echo "soak: killed for exceeding the memory limit: $oom" >&2
    exit 1
fi

rm -rf "data${job#/data}"
printf '%s\n' "$result" | awk -F'\t' '$1 == "seconds" { printf "soak: job time %.1f s\n", $2 }
$1 == "map.output.bytes" { printf "soak: map output %.2f GB\n", $2 / 1e9 }'
echo "soak: $size bytes with the combiner off finished on $workers workers at 256 MB, no OOM kill"
