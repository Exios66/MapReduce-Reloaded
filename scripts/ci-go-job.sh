#!/bin/sh
# Runs this CircleCI node's share of the go job (docs/ci.md, ADR 0023).
#
# The go job runs with `parallelism: 3`: CircleCI starts three containers of
# the one job, and the go check passes only if all three do. Each container
# runs a different part of the same make targets the job always ran, so
# together they run all of it:
#
#   full mode    node 0: make go-test-race
#                node 1: make go-test-cover   (go-test = race, then cover)
#                node 2: make sh-lint go-lint py-e2e
#   narrow mode  node 0: make go-python-test
#                node 1: nothing
#                node 2: make py-e2e
#
# With one node (CIRCLE_NODE_TOTAL unset or 1) it runs every part in turn,
# in the order the job used to. Any other node count is an error, so a
# change to the job's parallelism cannot silently drop a part.
#
# Usage: scripts/ci-go-job.sh full|narrow  (PYTHON and MAKE may be set;
# MR_CI_GO_JOB_NO_INSTALL=1 skips the shellcheck install, for the tests)
set -eu
mode=${1:-}
node=${CIRCLE_NODE_INDEX:-0}
total=${CIRCLE_NODE_TOTAL:-1}
make=${MAKE:-make}
python=${PYTHON:-python}

run() {
    echo "ci-go-job: $mode mode, node $node of $total: make $*"
    # shellcheck disable=SC2086 # MAKE may be a command with arguments.
    $make "$@" PYTHON="$python"
}

# sh-lint needs shellcheck: install the release pinned in vm-setup.sh (it
# checks the SHA-256). make check skips shellcheck where it is missing; CI
# must not, so MR_REQUIRE_SHELLCHECK makes a missing one fail.
lint() {
    if [ -z "${MR_CI_GO_JOB_NO_INSTALL:-}" ]; then
        scripts/vm-setup.sh --shellcheck
        PATH="$HOME/.local/bin:$PATH"
    fi
    MR_REQUIRE_SHELLCHECK=1
    export MR_REQUIRE_SHELLCHECK
    run sh-lint
    run go-lint
}

case "$mode:$total" in
full:1)
    lint
    run go-test-race
    run go-test-cover
    run py-e2e
    ;;
full:3)
    case "$node" in
    0) run go-test-race ;;
    1) run go-test-cover ;;
    2)
        lint
        run py-e2e
        ;;
    *) echo "ci-go-job: node $node of 3?" >&2; exit 2 ;;
    esac
    ;;
narrow:1)
    run go-python-test
    run py-e2e
    ;;
narrow:3)
    case "$node" in
    0) run go-python-test ;;
    1) echo "ci-go-job: narrow mode, node 1 of 3: nothing to run (nodes 0 and 2 run it all)" ;;
    2) run py-e2e ;;
    *) echo "ci-go-job: node $node of 3?" >&2; exit 2 ;;
    esac
    ;;
*)
    echo "usage: ci-go-job.sh full|narrow, on 1 or 3 CircleCI nodes (got mode '$mode', $total nodes)" >&2
    exit 2
    ;;
esac
