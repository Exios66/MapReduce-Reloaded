#!/usr/bin/env bash
# End-to-end test (#21) on the running Compose cluster: word count over the
# test corpus must match the golden output, which local mode also produces,
# byte for byte, no container may have been killed for exceeding its memory
# limit, and the coordinator's /metrics must show the job done (#32).
#
# With the argument s3, the job reads and writes the s3 profile's store
# (#41) instead of the shared volume.
set -euo pipefail
cd "$(dirname "$0")/.."

compose=(docker compose -p mapreduce -f deploy/compose/compose.yaml)
name=e2e-$(date +%s)
case "${1:-local}" in
local) job=/out/$name input='/corpus/*.txt' from=coordinator:/out/$name ;;
s3) job=s3://out/$name input='s3://corpus/*.txt' from=s3:/data/out/$name ;;
*) echo "usage: $0 [local|s3]" >&2; exit 2 ;;
esac
got=$(mktemp -d)
trap 'rm -rf "$got"' EXIT

# The job is bounded, since it never finishes if every worker is killed;
# the memory check below then says why.
submitted=0
timeout 600 "${compose[@]}" run --rm mr submit -coordinator coordinator:7070 -m 7 -r 4 -wait \
    wordcount "$job" "$input" || submitted=$?

containers=$("${compose[@]}" ps -aq)
if [ -z "$containers" ]; then
    echo "e2e: no cluster containers to inspect" >&2
    exit 1
fi
mapfile -t container_ids <<< "$containers"
inspection=$(docker inspect -f '{{.Name}} {{.State.OOMKilled}}' "${container_ids[@]}")
if [ -z "$inspection" ]; then
    echo "e2e: container inspection returned no output" >&2
    exit 1
fi
oom=$(printf '%s\n' "$inspection" | grep ' true$' || true)
if [ -n "$oom" ]; then
    echo "e2e: killed for exceeding the memory limit: $oom" >&2
    exit 1
fi
if [ "$submitted" -ne 0 ]; then
    echo "e2e: the job failed or timed out (exit $submitted)" >&2
    exit 1
fi
"${compose[@]}" cp "$from" "$got/out"
diff -r testdata/corpus/expected "$got/out"
# Jobs are listed in the order submitted, so the last one is this job.
done_reduces=$(curl -fsS http://127.0.0.1:18780/metrics | grep 'kind="reduce",state="completed"}' | tail -1)
if [ "${done_reduces##* }" != 4 ]; then
    echo "e2e: /metrics does not show the job's 4 reduce tasks completed" >&2
    exit 1
fi
echo "e2e: output matches the golden output"
