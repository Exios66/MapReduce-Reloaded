#!/usr/bin/env python3
"""Fail if any file the PR changes matches no CI mapping pattern.

Reads the mapping block from .circleci/config.yml (the ``mapping: |`` block
under the path-filtering/filter job; each line's first token is a regex),
diffs the branch against origin/main at the merge base, and exits 1 listing
every changed file that no pattern matches. An unmapped file must get a
mapping before the PR can merge: silently skipping it would drop CI
coverage for whatever it affects (docs/ci.md, ADR 0019).

Run from the repository root: ``python3 scripts/check-ci-mapping.py``.
The mapping-coverage CI job runs it on every PR.
"""

import re
import subprocess
import sys
from pathlib import Path

SETUP_CONFIG = Path(".circleci/config.yml")


def load_patterns(config_text):
    """Return the mapping regexes from the setup config's mapping block."""
    lines = config_text.splitlines()
    patterns = []
    in_block = False
    block_indent = None
    for line in lines:
        if not in_block:
            if line.strip() == "mapping: |":
                in_block = True
            continue
        if not line.strip():
            continue
        indent = len(line) - len(line.lstrip())
        if block_indent is None:
            block_indent = indent
        if indent < block_indent:
            break
        patterns.append(line.split()[0])
    return patterns


def unmatched_files(files, patterns):
    """Return the files that no mapping pattern matches (re.search)."""
    compiled = [re.compile(p) for p in patterns]
    return [f for f in files if not any(rx.search(f) for rx in compiled)]


def _run(cmd, check=True):
    return subprocess.run(cmd, check=check, text=True, capture_output=True)


def fetch_main():
    """Fetch origin/main, falling back to the public URL (proto job pattern)."""
    try:
        _run(["git", "fetch", "--quiet", "origin",
              "+refs/heads/main:refs/remotes/origin/main"])
        return
    except subprocess.CalledProcessError:
        pass
    url = _run(["git", "config", "--get", "remote.origin.url"],
               check=False).stdout.strip()
    if url.startswith("git@github.com:"):
        slug = url[len("git@github.com:"):]
    elif "github.com/" in url:
        slug = url.split("github.com/", 1)[1]
    else:
        slug = url
    if slug.endswith(".git"):
        slug = slug[:-4]
    _run(["git", "fetch", "--quiet", f"https://github.com/{slug}.git",
          "+refs/heads/main:refs/remotes/origin/main"])


def changed_files():
    """Return the files this branch changes against origin/main."""
    base = _run(["git", "merge-base", "origin/main", "HEAD"]).stdout.strip()
    out = _run(["git", "diff", "--name-only", base, "HEAD"]).stdout
    return [line for line in out.splitlines() if line]


def main():
    patterns = load_patterns(SETUP_CONFIG.read_text())
    if not patterns:
        print("check-ci-mapping: no mapping block found in .circleci/config.yml",
              file=sys.stderr)
        return 1
    fetch_main()
    files = changed_files()
    bad = unmatched_files(files, patterns)
    if bad:
        print("check-ci-mapping: these changed files match no CI mapping "
              "pattern:", file=sys.stderr)
        for f in bad:
            print(f"  {f}", file=sys.stderr)
        print("Add a mapping for each file in .circleci/config.yml "
              "(docs/ci.md).", file=sys.stderr)
        return 1
    print(f"check-ci-mapping: {len(files)} changed file(s), all mapped.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
