#!/usr/bin/env bash
# preflight.sh [--final]: checks the current branch against AGENTS.md's CI
# and commit rules before a push, and says whether the push would start a
# CircleCI pipeline. Reads git and, when available, gh; runs no tests.
#
# Without --final it checks an in-progress push: the head commit should
# carry [skip ci] once a PR is open. With --final it checks the one push
# that runs the full suite: up to date with origin/main, no [skip ci], and a
# CHANGELOG entry that cites the PR number. Exits 1 if any check fails.
set -uo pipefail
final=false
[ "${1:-}" = --final ] && final=true
cd "$(git rev-parse --show-toplevel)" || exit 1
fail=0
ok() { echo "  ok    $*"; }
bad() { echo "  FAIL  $*"; fail=1; }
note() { echo "  note  $*"; }

branch=$(git rev-parse --abbrev-ref HEAD)
git fetch -q origin main 2>/dev/null || note "could not fetch origin/main; using the local copy"
base=$(git merge-base HEAD origin/main)
echo "preflight: $branch ($(git rev-parse --short HEAD)), $([ "$final" = true ] && echo final || echo in-progress) push"

if [ "$branch" != main ]; then ok "not on main"; else bad "on main: branch first; main changes only through a PR"; fi

# Identity and footers (AGENTS.md "Commits and PRs").
while IFS='|' read -r sha author committer; do
    case "$author|$committer" in
    "Jack J Burleson|Jack J Burleson" | "coderabbitai[bot]|"*) ;;
    *) bad "$sha is authored by '$author', committed by '$committer'; every commit is the owner's" ;;
    esac
done < <(git log --format='%h|%an|%cn' "$base..HEAD")
if git log --format=%B "$base..HEAD" | grep -qiE '^(Co-Authored-By:|.*Generated with)'; then
    bad "a commit carries a Co-Authored-By or 'Generated with' line"
else
    ok "commits: owner identity, no attribution footers"
fi

# Which jobs the change implicates (docs/ci.md, ADR 0020).
if params=$(python3 scripts/ci-params.py 2>/dev/null); then
    note "jobs this diff implicates: $params"
fi
python3 scripts/ci-params.py --check >/dev/null 2>&1 || note "ci-params --check reports an unmapped file; the full suite will run"

# CHANGELOG (AGENTS.md "Changelog").
pr=$(gh pr view --json number --jq .number 2>/dev/null || true)
added=$(git diff "$base" HEAD -- CHANGELOG.md | grep -E '^\+- ' || true)
if [ -z "$added" ]; then
    note "no CHANGELOG.md bullet added; fine only for a change no user would notice"
else
    ok "CHANGELOG.md has $(grep -c . <<< "$added") new bullet(s)"
    if [ -n "$pr" ] && ! git diff "$base" HEAD -- CHANGELOG.md | grep -E '^\+' | grep -q "#$pr"; then
        if [ "$final" = true ]; then bad "no new CHANGELOG line cites (#$pr)"; else note "cite (#$pr) in the CHANGELOG by the final push"; fi
    fi
fi

# Pipeline cost (AGENTS.md "CI: one full run per PR, at the end").
behind=$(git rev-list --count HEAD..origin/main)
skip=false
git log -1 --format=%B | grep -q '\[skip ci\]' && skip=true
if [ -z "$pr" ]; then
    note "no open PR: opening one always runs one pipeline, so open it only when the work is ready for review"
    # Issues, PRs and discussions share one sequence, so the newest of any
    # names the next number, unless another is opened first; check it after.
    # Either lookup failing would leave a number already taken, so a failed
    # lookup gives a note and no prediction.
    # shellcheck disable=SC2016 # $owner and $repo are GraphQL variables
    if ! last=$(gh api "repos/{owner}/{repo}/issues?state=all&per_page=1&sort=created&direction=desc" --jq '.[0].number // empty' 2>/dev/null); then
        note "could not look up the newest issue or PR; no PR-number prediction"
    elif ! disc=$(gh api graphql -F owner='{owner}' -F repo='{repo}' -f query='query($owner:String!,$repo:String!){repository(owner:$owner,name:$repo){discussions(first:1,orderBy:{field:CREATED_AT,direction:DESC}){nodes{number}}}}' --jq '.data.repository.discussions.nodes[0].number // empty' 2>/dev/null); then
        note "could not look up the newest discussion; no PR-number prediction"
    else
        if [ -n "$disc" ] && { [ -z "$last" ] || [ "$disc" -gt "$last" ]; }; then last=$disc; fi
        if [ -n "$last" ]; then
            note "the PR will likely be #$((last + 1)): cite it in the CHANGELOG before opening, so the opening pipeline is the final one"
        fi
    fi
fi
if [ "$final" = true ]; then
    if [ "$behind" = 0 ]; then ok "up to date with origin/main"; else bad "$behind commit(s) behind origin/main: merge main, then make check"; fi
    if [ "$skip" = false ]; then ok "head has no [skip ci]: this push runs the full suite"; else bad "head has [skip ci]: the final push must run CI"; fi
else
    [ "$behind" = 0 ] || note "$behind commit(s) behind origin/main: leave it until the final push"
    if [ "$skip" = true ]; then ok "head has [skip ci]: this push runs no pipeline"
    elif [ -n "$pr" ]; then bad "PR #$pr is open and the head lacks [skip ci]: this push would spend a pipeline"
    fi
fi
note "run make check before pushing, on the experiment VM rather than a busy laptop"
exit $fail
