#!/usr/bin/env bash
# status.sh HOST [REPO] [RUNS]: one read-only snapshot of a benchmark
# campaign on HOST (an ssh alias, or - for this machine).
#
# REPO is the clone the experiments run in (default ~/mr-exp); RUNS holds a
# gated chain's logs/ and per-commit archives (default ~/runs). Prints the
# chain's progress if RUNS/logs/chain.log exists, the running chunk's last
# runs, the row count of every bench/results/*/runs.csv, the cluster's
# containers, load and free disk. Starts, stops and deletes nothing.
set -uo pipefail
host=${1:?usage: status.sh HOST [REPO] [RUNS]}
repo=${2:-'~/mr-exp'} runs=${3:-'~/runs'}

probe=$(cat <<EOF
repo=$repo runs=$runs
echo "== \$(hostname) at \$(date -u +%FT%TZ)"
echo "load: \$(cut -d' ' -f1-3 /proc/loadavg 2>/dev/null)   free on /: \$(df -h / | awk 'NR==2{print \$4}')"
if [ -f \$runs/logs/chain.log ]; then
    echo "== chain (\$runs/logs/chain.log)"
    grep -E 'START|GATE|DONE|FAIL|ABORT|WARNING|p5 |CHAIN' \$runs/logs/chain.log | tail -n 12
    c=\$(grep START \$runs/logs/chain.log | tail -1 | sed -E 's/.*START ([^ ]+) .*/\1/')
    if [ -n "\$c" ] && [ -f \$runs/logs/\$c.log ]; then
        echo "== running chunk \$c: \$(grep -cE ' run [0-9]+\$' \$runs/logs/\$c.log) runs started; last lines:"
        tail -n 3 \$runs/logs/\$c.log
    fi
    echo "archives: \$(ls \$runs/*/ 2>/dev/null | grep -c 'tar.gz') in \$runs/<commit>/"
fi
command -v tmux >/dev/null && echo "tmux: \$(tmux ls 2>/dev/null | cut -d: -f1 | tr '\n' ' ')"
if [ -d \$repo/bench/results ]; then
    echo "== \$repo/bench/results (rows excluding header)"
    for f in \$repo/bench/results/*/runs.csv; do
        [ -f "\$f" ] && echo "  \$(basename \$(dirname \$f)): \$((\$(wc -l < \$f) - 1)) runs, last modified \$(date -u -r \$f +%H:%M:%SZ)"
    done
fi
echo "== cluster containers"
docker ps --filter label=com.docker.compose.project=mapreduce --format '  {{.Names}}  {{.Status}}' 2>/dev/null
EOF
)
if [ "$host" = - ]; then bash -c "$probe"; else ssh -o ConnectTimeout=15 -o BatchMode=yes "$host" bash -s <<< "$probe"; fi
