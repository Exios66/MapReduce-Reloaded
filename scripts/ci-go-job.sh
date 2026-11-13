#!/bin/sh
# Runs this CircleCI node's share of the go job (docs/ci.md, ADR 0024, ADR 0025).
#
# The go job runs with `parallelism: 3`: CircleCI starts three containers of
# the one job, and the go check passes only if all three do. Each container
# runs a different part of the same make targets the job always ran, so
# together they run all of it:
#
#   full mode    node 0: make go-test-race
#                node 1: make go-test-cover   (go-test = race, then cover)
#                node 2: make sh-lint go-lint py-e2e
#
# Narrow mode runs only the parts a PR's files implicate, when none of them
# needs the full mode. MR_CI_GO_NARROW names the parts, as CircleCI renders
# the pipeline parameters: "python=true docs=false sh=false".
#
#   python  only python/ changed:            make go-python-test py-e2e
#   docs    only README.md or                make go-python-test
#           docs/paper-mapping.md changed
#   sh      only shell scripts or the git    make sh-lint
#           hook changed
#
#   narrow mode  node 0: make go-python-test  (python or docs)
#                node 1: nothing
#                node 2: make sh-lint         (sh)
#                        make py-e2e          (python)
#
# With one node (CIRCLE_NODE_TOTAL unset or 1) it runs every part in turn,
# in the order the job used to. Any other node count is an error, and so is
# a narrow mode with no part, so a change to the job's parallelism or its
# parameters cannot silently drop a part.
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
sh_lint() {
    if [ -z "${MR_CI_GO_JOB_NO_INSTALL:-}" ]; then
        scripts/vm-setup.sh --shellcheck
        PATH="$HOME/.local/bin:$PATH"
    fi
    MR_REQUIRE_SHELLCHECK=1
    export MR_REQUIRE_SHELLCHECK
    run sh-lint
}

# part NAME: whether narrow mode was asked for the part NAME.
part() {
    case " ${MR_CI_GO_NARROW:-} " in
    *" $1=true "*) return 0 ;;
    *) return 1 ;;
    esac
}

idle() {
    echo "ci-go-job: narrow mode, node $node of $total: nothing to run for '${MR_CI_GO_NARROW:-}'"
}

# The narrow parts of one node: go (go-python-test) or rest (sh-lint, py-e2e).
narrow_go() {
    if part python || part docs; then run go-python-test; else idle; fi
}

narrow_rest() {
    if ! part sh && ! part python; then
        idle
        return
    fi
    if part sh; then sh_lint; fi
    if part python; then run py-e2e; fi
}

if [ "$mode" = narrow ] && ! part python && ! part docs && ! part sh; then
    echo "ci-go-job: narrow mode needs a part: MR_CI_GO_NARROW is '${MR_CI_GO_NARROW:-}', want python=true, docs=true or sh=true in it" >&2
    exit 2
fi

case "$mode:$total" in
full:1)
    sh_lint
    run go-lint
    run go-test-race
    run go-test-cover
    run py-e2e
    ;;
full:3)
    case "$node" in
    0) run go-test-race ;;
    1) run go-test-cover ;;
    2)
        sh_lint
        run go-lint
        run py-e2e
        ;;
    *) echo "ci-go-job: node $node of 3?" >&2; exit 2 ;;
    esac
    ;;
narrow:1)
    if part sh; then sh_lint; fi
    if part python || part docs; then run go-python-test; fi
    if part python; then run py-e2e; fi
    ;;
narrow:3)
    case "$node" in
    0) narrow_go ;;
    1) idle ;;
    2) narrow_rest ;;
    *) echo "ci-go-job: node $node of 3?" >&2; exit 2 ;;
    esac
    ;;
*)
    echo "usage: ci-go-job.sh full|narrow, on 1 or 3 CircleCI nodes (got mode '$mode', $total nodes)" >&2
    exit 2
    ;;
esac
