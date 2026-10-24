#!/usr/bin/env bash
# Codec benchmark (#42) on the running Compose cluster: runs word count and
# distsort REPEAT times (default 3) per intermediate codec, and writes the
# bytes shuffled and the job time of each run to bench/results/codecs.csv.
#
# The input is SIZE bytes (default 64 MiB) of lines of random corpus words,
# from bench/gen-words.py, copied into the shared /out volume. Every map
# output file is fetched once by its reduce task, so map.output.bytes is
# the bytes shuffled. Seconds is the job's duration as the coordinator
# measured it; wall_seconds is the time of mr submit -wait, which also
# includes about a second to start its container. Each run's output, and the
# input at the end, are removed from /out with the Dockerfile's build image,
# since the cluster images have no shell.
set -euo pipefail
cd "$(dirname "$0")/.."

compose=(docker compose -p mapreduce -f deploy/compose/compose.yaml)
size=${SIZE:-67108864}
repeat=${REPEAT:-3}
results=bench/results/codecs.csv
tmp=$(mktemp -d)
# Removes paths from the output volume; the build image is there after
# make cluster-up.
rm_out() { docker run --rm -v mapreduce_output:/out golang:1.26-alpine rm -rf "$@"; }
trap 'rm -rf "$tmp"; rm_out /out/bench-input.txt' EXIT

python3 bench/gen-words.py "$size" > "$tmp/input.txt"
input_bytes=$(wc -c < "$tmp/input.txt")
"${compose[@]}" cp "$tmp/input.txt" coordinator:/out/bench-input.txt

mkdir -p bench/results
echo "program,codec,run,input_bytes,map_output_records,map_output_bytes,seconds,wall_seconds" > "$results"
stamp=$(date +%s)
counter() { awk -v name="$1" '$1 == name { print $2 }' "$tmp/status"; }
for program in wordcount distsort; do
    for codec in none zstd snappy; do
        for run in $(seq "$repeat"); do
            start=$(date +%s.%N)
            "${compose[@]}" run --rm mr submit -coordinator coordinator:7070 -m 16 -r 4 \
                -intermediate-codec "$codec" -wait \
                "$program" "/out/bench-$stamp-$program-$codec-$run" /out/bench-input.txt > "$tmp/status"
            end=$(date +%s.%N)
            wall=$(awk -v s="$start" -v e="$end" 'BEGIN { printf "%.3f", e - s }')
            echo "$program,$codec,$run,$input_bytes,$(counter map.output.records),$(counter map.output.bytes),$(counter seconds),$wall" |
                tee -a "$results"
            rm_out "/out/bench-$stamp-$program-$codec-$run"
        done
    done
done
