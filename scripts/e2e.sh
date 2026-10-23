#!/usr/bin/env bash
# End-to-end test (#21) on the running Compose cluster: word count over the
# test corpus must match the golden output, which local mode also produces,
# byte for byte, and no container may have been killed for exceeding its
# memory limit.
set -euo pipefail
cd "$(dirname "$0")/.."

compose=(docker compose -f deploy/compose/compose.yaml)
job=/out/e2e-$(date +%s)
got=$(mktemp -d)
trap 'rm -rf "$got"' EXIT

"${compose[@]}" run --rm mr submit -coordinator coordinator:7070 -m 7 -r 4 -wait \
    wordcount "$job" '/corpus/*.txt'
"${compose[@]}" cp "coordinator:$job" "$got/out"
diff -r testdata/corpus/expected "$got/out"

oom=$(docker inspect -f '{{.Name}} {{.State.OOMKilled}}' $("${compose[@]}" ps -aq) | grep ' true$' || true)
if [ -n "$oom" ]; then
    echo "e2e: killed for exceeding the memory limit: $oom" >&2
    exit 1
fi
echo "e2e: output matches the golden output"
