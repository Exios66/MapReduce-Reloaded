#!/usr/bin/env bash
# End-to-end test (#21) on the running Compose cluster: word count over the
# test corpus must match the golden output, which local mode also produces,
# byte for byte, no container may have been killed for exceeding its memory
# limit, and the coordinator's /metrics must show the job done (#32).
#
# The job is submitted by the host client, bin/mr (make mr), over input it
# uploads to shared storage, and the output is read and then deleted from
# data/ by the host user (ADR 0016). With the argument s3, the job is
# submitted from the mr container and reads and writes the s3 profile's
# store (#41). Afterwards the coordinator is killed and started again, and
# must still report the job SUCCEEDED, read back from its write-ahead log.
set -euo pipefail
cd "$(dirname "$0")/.."

compose=(docker compose -p mapreduce -f deploy/compose/compose.yaml)
name=e2e-$(date +%s)
case "${1:-local}" in
local) job=/data/out/$name input='/data/input/e2e-corpus/*.txt'
    submit=(bin/mr submit) ;;
s3) job=s3://out/$name input='s3://corpus/*.txt' from=s3:/data/out/$name
    submit=("${compose[@]}" run --rm mr submit -coordinator coordinator:7070) ;;
*) echo "usage: $0 [local|s3]" >&2; exit 2 ;;
esac
got=$(mktemp -d)
trap 'rm -rf "$got"' EXIT

# The image runs its binaries as a non-root user, and each answers --help
# (#158). cluster-up builds it.
user=$(docker image inspect --format '{{.Config.User}}' mapreduce-reloaded)
case "$user" in
"" | 0 | 0:* | root | root:*) echo "e2e: the image runs as root (User=$user)" >&2; exit 1 ;;
esac
docker run --rm --entrypoint /usr/local/bin/coordinator mapreduce-reloaded --help >/dev/null
docker run --rm --entrypoint /usr/local/bin/worker mapreduce-reloaded --help >/dev/null
docker run --rm --entrypoint /usr/local/bin/mr mapreduce-reloaded status --help >/dev/null

# The job is bounded, since it never finishes if every worker is killed;
# the memory check below then says why.
submitted=0
[ "${1:-local}" = s3 ] || bin/mr upload 'testdata/corpus/*.txt' /data/input/e2e-corpus >/dev/null
result=$(timeout 600 "${submit[@]}" -m 7 -r 4 -wait wordcount "$job" "$input") || submitted=$?
printf '%s\n' "$result"
id=${result%%$'\t'*}

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
if [ "${1:-local}" = s3 ]; then
    "${compose[@]}" cp "$from" "$got/out"
    diff -r testdata/corpus/expected "$got/out"
else
    diff -r testdata/corpus/expected "data/out/$name"
    diff <(cat testdata/corpus/expected/part-r-*) <(bin/mr cat "$job")
    rm -r "data/out/$name" # the host user can delete what the containers wrote
fi
# Jobs are listed in the order submitted, so the last one is this job.
done_reduces=$(curl -fsS http://127.0.0.1:18780/metrics | grep 'kind="reduce",state="completed"}' | tail -1)
if [ "${done_reduces##* }" != 4 ]; then
    echo "e2e: /metrics does not show the job's 4 reduce tasks completed" >&2
    exit 1
fi

# Prometheus keeps the coordinator's job label: without honor_labels it is
# renamed to exported_job, and every MapReduce job merges into one series
# (#144, #158). The scrape interval is 5 s, so give it a few.
"${compose[@]}" --profile monitoring up -d --wait prometheus >/dev/null
series=
for _ in $(seq 30); do
    series=$(curl -fsS --get http://127.0.0.1:19090/api/v1/query \
        --data-urlencode "query=mr_job_tasks{job=\"$id\"}" || true)
    case "$series" in *"\"job\":\"$id\""*) break ;; esac
    sleep 1
done
case "$series" in
*"\"job\":\"$id\""*) ;;
*) echo "e2e: Prometheus does not label mr_job_tasks with job $id" >&2; exit 1 ;;
esac

# A crash, not a clean stop: a stopping coordinator tells its workers to exit.
coordinator=$("${compose[@]}" ps -q coordinator)
docker kill "$coordinator" >/dev/null
docker start "$coordinator" >/dev/null
state=
for _ in $(seq 60); do
    # The whole reply, then its first line: head could close the pipe early.
    state=$("${compose[@]}" run --rm -q mr status -coordinator coordinator:7070 "$id" 2>/dev/null) && state=${state%%$'\n'*} && break
    sleep 1
done
if [ "${state##*$'\t'}" != SUCCEEDED ]; then
    echo "e2e: after a restart the coordinator reports job $id as '${state:-nothing}', not SUCCEEDED" >&2
    exit 1
fi
echo "e2e: output matches the golden output, and the job survived a coordinator restart"
