#!/usr/bin/env bash
# Runs `make check` on a clean copy of HEAD and records the result as a GitHub
# commit status (context local-ci/make-check) on that commit. This is the
# fallback for when GitHub Actions cannot run (ADR 0014).
#
# Needs GITHUB_TOKEN (or GH_TOKEN): a fine-grained token with "Commit
# statuses: write" on this repository. Run it with `make ci-report`.
# Set PYTHON=... as for `make check`; set CI_REPORT_DRY_RUN=1 to skip posting.
set -euo pipefail

context="local-ci/make-check"
root=$(git rev-parse --show-toplevel)
cd "$root"

token="${GITHUB_TOKEN:-${GH_TOKEN:-}}"
if [ -z "$token" ] && [ -z "${CI_REPORT_DRY_RUN:-}" ]; then
    echo "ci-report: set GITHUB_TOKEN (or GH_TOKEN) to a token with 'Commit statuses: write'." >&2
    exit 1
fi

# Like the pre-push hook: only a committed tree can be reported on.
if [ -n "$(git status --porcelain --untracked-files=all)" ]; then
    echo "ci-report: commit or stash your changes first, so the report is about the commit." >&2
    exit 1
fi

# owner/repo from https://github.com/o/r(.git), git@github.com:o/r(.git) or ssh://git@github.com/o/r.
url=$(git remote get-url origin)
slug=${url%.git}
slug=${slug#*github.com[:/]}
case "$slug" in
    */*/* | "" | */) echo "ci-report: cannot read owner/repo from origin '$url'." >&2; exit 1 ;;
    */*) ;;
    *) echo "ci-report: cannot read owner/repo from origin '$url'." >&2; exit 1 ;;
esac

sha=$(git rev-parse HEAD)
host=$(hostname)

post() { # state description
    if [ -n "${CI_REPORT_DRY_RUN:-}" ]; then
        echo "ci-report: [dry run] $1: $2"
        return 0
    fi
    # The token goes in a header file so it is not on the command line.
    curl -sS --fail-with-body -o /dev/null -X POST \
        -H "Accept: application/vnd.github+json" \
        -H @<(printf 'Authorization: Bearer %s\n' "$token") \
        "https://api.github.com/repos/$slug/statuses/$sha" \
        -d "{\"state\":\"$1\",\"context\":\"$context\",\"description\":\"$2\"}"
    echo "ci-report: posted $1 for ${sha:0:12} on $slug"
}

work=$(mktemp -d)
log=$(mktemp)
trap 'rm -rf "$work" "$log"' EXIT

# A fresh clone of this repository's commits, so untracked or ignored files
# cannot affect the result. It keeps origin pointing at GitHub, because
# `make proto-breaking` fetches origin/main.
git clone --quiet --no-hardlinks "$root" "$work/src"
git -C "$work/src" remote set-url origin "$url"
git -C "$work/src" checkout --quiet --detach "$sha"

post pending "make check running on $host"

start=$(date +%s)
status=0
{
    # Reuse this clone's pinned tools (.tools/bin is git-ignored); else install.
    if [ -d "$root/.tools/bin" ]; then
        mkdir -p "$work/src/.tools" && cp -R "$root/.tools/bin" "$work/src/.tools/bin"
    else
        make -C "$work/src" tools
    fi
    make -C "$work/src" check ${PYTHON:+PYTHON="$PYTHON"}
} >"$log" 2>&1 || status=$?
secs=$(($(date +%s) - start))

if [ "$status" -eq 0 ]; then
    post success "make check passed in ${secs}s on $host"
else
    echo "ci-report: make check failed (exit $status); last lines of the log:" >&2
    tail -n 40 "$log" >&2
    post failure "make check failed in ${secs}s on $host"
fi
exit "$status"
