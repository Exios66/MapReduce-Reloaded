#!/usr/bin/env bash
# drive.sh COMMAND [ARGS]: start, use, disturb and stop the Compose cluster
# (a coordinator and four workers) from the repository root.
#
#   up                     build and start the cluster, build bin/mr, upload the example data
#   wordcount              word count over the 4-book corpus; prints the top 5 words
#   big [MiB]              writes a Zipf input of MiB (default 512) to /data/input/zipf
#   status JOB             the job's status and counters
#   page                   the status page as text
#   kill-worker            word count on the big input; kills a worker mid-job
#   restart-coordinator    word count on the big input; restarts the coordinator mid-job
#   down                   stops the cluster and deletes its volumes; keeps data/
#
# Every clone on a host drives the same Compose project and volumes, so all
# but status and page refuse while a bench script runs or while another
# clone started the cluster. MR_DRIVE_FORCE=1 skips the check.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
compose=(docker compose -p mapreduce -f deploy/compose/compose.yaml)
page_url=http://127.0.0.1:18780/

# guard: refuses while a bench script runs or another clone owns the cluster.
guard() {
    [ "${MR_DRIVE_FORCE:-0}" = 1 ] && return
    local busy owners
    # Processes running a bench script: as the program or as an interpreter's first argument.
    busy=$(pgrep -a . | awk -v s='(^|/)bench/[a-z0-9-]+[.](sh|py)$' \
        '$2 ~ s || ($2 ~ /(^|\/)(ba|da)?sh$|python3?$/ && $3 ~ s)' || true)
    if [ -n "$busy" ]; then
        printf 'refusing: a bench script is running and shares this cluster:\n%s\n' "$busy" >&2
        exit 3
    fi
    owners=$(docker ps -a --filter label=com.docker.compose.project=mapreduce \
        --format '{{.Label "com.docker.compose.project.working_dir"}}' | sort -u | grep -vxF "$PWD/deploy/compose" || true)
    if [ -n "$owners" ]; then
        printf 'refusing: another clone started the cluster, and its volumes hold that clone'"'"'s data:\n%s\n' "$owners" >&2
        echo "stop it from that clone, or set MR_DRIVE_FORCE=1 if nothing there is needed" >&2
        exit 3
    fi
}

state() { bin/mr status "$1" 2>/dev/null | awk 'NR == 1 { print $2 }'; }

# wait_done JOB: polls until the job ends; prints its status; fails unless SUCCEEDED.
wait_done() {
    local s
    for _ in $(seq 600); do
        s=$(state "$1" || true)
        case $s in SUCCEEDED | FAILED) break ;; esac
        sleep 0.5
    done
    bin/mr status "$1"
    [ "$s" = SUCCEEDED ]
}

# disturb WHAT: runs word count on the big input and, once maps are under way,
# kills one worker or restarts the coordinator. The output must match an
# undisturbed run's.
disturb() {
    [ -f data/input/zipf/zipf.txt ] || bash "$0" big
    local ref=/data/out/drive-ref out=/data/out/drive-$1 job
    rm -rf "data/out/drive-ref" "data/out/drive-$1"
    job=$(bin/mr submit -m 16 -r 4 wordcount "$ref" /data/input/zipf/zipf.txt | tail -1)
    echo "reference run $job"
    wait_done "$job" >/dev/null
    job=$(bin/mr submit -m 16 -r 4 wordcount "$out" /data/input/zipf/zipf.txt | tail -1)
    echo "disturbed run $job"
    until bin/mr status "$job" 2>/dev/null | awk '$1 == "map" { split($2, a, "/"); exit !(a[1] > 0) }'; do sleep 0.5; done
    case $1 in
    kill-worker)
        local victim
        victim=$("${compose[@]}" ps -q worker | head -1)
        echo "killing worker $victim at $(bin/mr status "$job" | awk '$1 == "map" { print "map " $2 }')"
        docker kill "$victim" >/dev/null
        ;;
    restart-coordinator)
        echo "restarting the coordinator at $(bin/mr status "$job" | awk '$1 == "map" { print "map " $2 }')"
        "${compose[@]}" restart coordinator >/dev/null
        ;;
    esac
    wait_done "$job"
    "${compose[@]}" up -d --scale worker=4 coordinator worker >/dev/null 2>&1
    if [ "$(bin/mr cat "$ref" | sort | sha256sum)" = "$(bin/mr cat "$out" | sort | sha256sum)" ]; then
        echo "output matches the undisturbed run"
    else
        echo "OUTPUT DIFFERS from the undisturbed run" >&2
        return 1
    fi
}

case ${1:-help} in
up | wordcount | big | kill-worker | restart-coordinator | down) guard ;;
esac

case ${1:-help} in
up) make cluster-up mr upload-example ;;
wordcount)
    rm -rf data/out/wc
    bin/mr submit -m 8 -r 4 -wait examples/wordcount.go /data/out/wc '/data/input/corpus/*.txt'
    bin/mr cat /data/out/wc | sort -t$'\t' -k2 -nr | sed -n 1,5p
    ;;
big)
    mkdir -p data/input/zipf
    python3 bench/gen-zipf.py $((${2:-512} * 1048576)) 42 1.0 > data/input/zipf/zipf.txt
    ls -l data/input/zipf/zipf.txt
    ;;
status) bin/mr status "${2:?usage: drive.sh status JOB}" ;;
page)
    html=$(curl -fsS "$page_url")
    printf '%s\n' "$html" | sed -e '/<style/,/<\/style>/d' -e '/<script/,/<\/script>/d' \
        -e 's/<[^>]*>/ /g' -e 's/&middot;/·/g' -e 's/  */ /g' | grep -v '^ *$' || true
    ;;
kill-worker | restart-coordinator) disturb "$1" ;;
down) make cluster-down ;;
*) sed -n '2,16p' "$0" | sed 's/^# \{0,1\}//' ;;
esac
