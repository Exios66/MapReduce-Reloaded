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
    # ps, not pgrep -a: the BSD pgrep on macOS prints bare PIDs, so the guard
    # would pass silently there.
    busy=$(ps -eo pid=,args= | awk -v s='(^|/)bench/[a-z0-9-]+[.](sh|py)$' \
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

# maps_done JOB: the map count on the job's status line, e.g. 3/16, or nothing
# if the coordinator cannot be reached.
maps_done() { bin/mr status "$1" 2>/dev/null | awk '$1 == "map" { print $2 }'; }

# disturbed_ready JOB: waits until a map has finished and the job is still
# running, so the kill or restart lands while it still has work to recover.
# Returns 1 if the job ended first: nothing was then disturbed, and a run that
# passed without it would not show that recovery works.
disturbed_ready() {
    local maps
    for _ in $(seq 600); do
        case $(state "$1") in
        SUCCEEDED | FAILED) return 1 ;;
        esac
        maps=$(maps_done "$1")
        case $maps in
        '' | 0/*) ;;
        *) return 0 ;;
        esac
        sleep 0.5
    done
    return 1
}

# restore_workers: brings the cluster back to four workers whatever the
# disturbance did, so a failed kill or a failed job leaves no worker stopped.
restore_workers() {
    "${compose[@]}" up -d --scale worker=4 coordinator worker >/dev/null 2>&1 || true
}

# compare_outputs REF OUT: 0 when their sorted contents are byte-identical, 1
# when they differ, 2 when either cannot be read. cmp, not sha256sum, which
# stock macOS lacks; an unreadable output must not count as a match.
compare_outputs() {
    local a b
    a=$(mktemp) || return 2
    b=$(mktemp) || { rm -f "$a"; return 2; }
    if ! bin/mr cat "$1" | sort > "$a" || ! bin/mr cat "$2" | sort > "$b"; then
        rm -f "$a" "$b"
        return 2
    fi
    if cmp -s "$a" "$b"; then
        rm -f "$a" "$b"
        return 0
    fi
    rm -f "$a" "$b"
    return 1
}

# disturb WHAT: runs word count on the big input and, once maps are under way,
# kills one worker or restarts the coordinator. The output must match an
# undisturbed run's.
disturb() {
    [ -f data/input/zipf/zipf.txt ] || bash "$0" big
    local ref=/data/out/drive-ref out=/data/out/drive-$1 job failed=0 victim cmp_status=0
    rm -rf "data/out/drive-ref" "data/out/drive-$1"
    job=$(bin/mr submit -m 16 -r 4 wordcount "$ref" /data/input/zipf/zipf.txt | tail -1)
    echo "reference run $job"
    wait_done "$job" >/dev/null
    job=$(bin/mr submit -m 16 -r 4 wordcount "$out" /data/input/zipf/zipf.txt | tail -1)
    echo "disturbed run $job"
    if ! disturbed_ready "$job"; then
        echo "the job ended before a map was seen running, so nothing was disturbed; use a bigger input" >&2
        return 1
    fi
    case $1 in
    kill-worker)
        victim=$("${compose[@]}" ps -q worker | head -1 || true)
        echo "killing worker $victim at map $(maps_done "$job")"
        docker kill "$victim" >/dev/null || failed=1
        ;;
    restart-coordinator)
        echo "restarting the coordinator at map $(maps_done "$job")"
        "${compose[@]}" restart coordinator >/dev/null || failed=1
        ;;
    esac
    if [ "$failed" = 0 ]; then
        wait_done "$job" || failed=1
    fi
    restore_workers
    [ "$failed" = 0 ] || return 1
    compare_outputs "$ref" "$out" || cmp_status=$?
    case $cmp_status in
    0) echo "output matches the undisturbed run" ;;
    1)
        echo "OUTPUT DIFFERS from the undisturbed run" >&2
        return 1
        ;;
    *)
        echo "could not read the outputs to compare them" >&2
        return 1
        ;;
    esac
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
down) make cluster-clean ;;
*) sed -n '2,16p' "$0" | sed 's/^# \{0,1\}//' ;;
esac
