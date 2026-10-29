#!/usr/bin/env python3
"""Select the CI jobs a branch implicates, and check that selection.

Reads .circleci/mapping.txt (each line `<regex> <parameter>`), diffs the
branch against origin/main at the merge base, and turns the changed files
into the pipeline parameters of .circleci/continue-config.yml
(docs/ci.md, ADR 0020).

The rules, all conservative:

- a changed file sets every parameter whose regex matches it;
- a deleted file sets run-all, because other directories may name it (the
  paper-mapping table, Markdown links), and the diff lists a rename as a
  deletion and an addition, so a moved file is seen at both paths;
- a file no regex matches sets run-all, and --check fails on it;
- on main, everything runs.

With no arguments it prints the parameters as JSON, for the setup job's
continuation. With --check (the mapping-coverage job) it exits 1 if any
changed file is unmapped, or if the parameters this pipeline received, in
the MR_CI_PARAMS environment variable as JSON, lack one the selection sets:
a mismatch means the selection did not reach the jobs.

Run from the repository root: ``python3 scripts/ci-params.py [--check]``.
"""

import json
import os
import re
import subprocess
import sys
from pathlib import Path

MAPPING = Path(".circleci/mapping.txt")
PARAMETERS = ("run-all", "run-go", "run-python", "run-proto", "run-lint")


def load_mapping(text):
    """Return [(compiled regex, parameter)] from the mapping file's text."""
    mapping = []
    for n, line in enumerate(text.splitlines(), 1):
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        fields = line.split()
        if len(fields) != 2 or fields[1] not in PARAMETERS:
            raise ValueError(f"{MAPPING}:{n}: want `<regex> <parameter>`, got {line!r}")
        mapping.append((re.compile(fields[0]), fields[1]))
    return mapping


def select(changes, mapping):
    """Return (parameters, unmapped files) for [(status, path)] changes."""
    params, unmapped = set(), []
    for status, path in changes:
        matched = {param for rx, param in mapping if rx.search(path)}
        if not matched:
            unmapped.append(path)
            matched = {"run-all"}
        if status == "D":
            matched.add("run-all")
        params |= matched
    return params, unmapped


def _git(*args):
    return subprocess.run(["git", *args], check=True, text=True, capture_output=True).stdout


def fetch_main():
    """Fetch origin/main, falling back to the public URL (a fork has no deploy key)."""
    ref = "+refs/heads/main:refs/remotes/origin/main"
    try:
        _git("fetch", "--quiet", "origin", ref)
        return
    except subprocess.CalledProcessError:
        pass
    url = subprocess.run(
        ["git", "config", "--get", "remote.origin.url"], text=True, capture_output=True
    ).stdout.strip()
    slug = re.sub(r"^.*github\.com[:/]|\.git$", "", url)
    _git("fetch", "--quiet", f"https://github.com/{slug}.git", ref)


def changed_files():
    """Return [(status, path)] for what this branch changes against origin/main.

    --no-renames lists a rename as a deletion and an addition, so the old
    path is seen too; it also keeps the diff to trees, which a blobless
    checkout has.
    """
    base = _git("merge-base", "origin/main", "HEAD").strip()
    out = _git(
        "-c", "core.quotepath=false", "diff", "--no-renames", "--name-status", "-z", base, "HEAD"
    )
    fields = out.split("\0")
    return [(fields[i][0], fields[i + 1]) for i in range(0, len(fields) - 1, 2)]


def main(argv):
    check = argv == ["--check"]
    if argv and not check:
        print("usage: ci-params.py [--check]", file=sys.stderr)
        return 2
    mapping = load_mapping(MAPPING.read_text())
    if os.environ.get("CIRCLE_BRANCH") == "main":
        params, changes, unmapped = {"run-all"}, [], []
    else:
        fetch_main()
        changes = changed_files()
        params, unmapped = select(changes, mapping)
    if not check:
        print(json.dumps({p: p in params for p in PARAMETERS}))
        return 0
    failed = False
    if unmapped:
        failed = True
        print(
            "ci-params: these changed files match no line of .circleci/mapping.txt:",
            file=sys.stderr,
        )
        for f in unmapped:
            print(f"  {f}", file=sys.stderr)
        print("Add a line for each (docs/ci.md).", file=sys.stderr)
    received = os.environ.get("MR_CI_PARAMS")
    if received is not None:
        got = {p for p, on in json.loads(received).items() if on}
        missing = set() if "run-all" in got else params - got
        if missing:
            failed = True
            print(
                f"ci-params: the pipeline ran with {sorted(got)}, but the changes need "
                f"{sorted(missing)}: the selection did not reach the jobs.",
                file=sys.stderr,
            )
    if failed:
        return 1
    deleted = sum(1 for status, _ in changes if status == "D")
    print(
        f"ci-params: {len(changes)} changed file(s), {deleted} deleted, all mapped; "
        f"selected {sorted(params) or 'nothing'}."
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
