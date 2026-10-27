#!/usr/bin/env bash
# Runs the Go benchmarks COUNT times (default 6) and compares them with
# bench/baseline.txt (make perf, #160): it fails if a benchmark's median
# time per op is more than THRESHOLD percent (default 20) slower. The
# baseline is from one machine, so on another (a different cpu line) the
# comparison is printed but cannot fail. BASELINE names another baseline
# file. PERF_UPDATE=1 replaces the
# baseline with this run instead, to be committed on purpose.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
base=${BASELINE:-$root/bench/baseline.txt}
case $base in /*) ;; *) base=$PWD/$base ;; esac # before the cd below
if [ "${PERF_UPDATE:-}" != 1 ] && [ ! -f "$base" ]; then
    echo "perf: no baseline at $base; PERF_UPDATE=1 writes one" >&2
    exit 1
fi
new=$(mktemp)
trap 'rm -f "$new"' EXIT
cd "$root/go"
go test -run '^$' -bench . -benchtime "${BENCHTIME:-1s}" -count "${COUNT:-6}" \
    ./internal/combine ./internal/extsort ./internal/formats/kv ./internal/local \
    ./internal/jobfile ./internal/partition ./internal/shuffle | tee "$new"
if [ "${PERF_UPDATE:-}" = 1 ]; then
    cp "$new" "$base"
    echo "perf: wrote $base; commit it"
    exit 0
fi
if command -v benchstat >/dev/null; then
    benchstat "$base" "$new"
fi
"${PYTHON:-python3}" "$root/scripts/perf-compare.py" "$base" "$new" "${THRESHOLD:-20}"
