#!/usr/bin/env python3
"""Fail if any file the PR changes matches no CI mapping pattern.

Reads the mapping block from .circleci/config.yml (the ``mapping: |`` block
under the path-filtering/filter job; each line's first token is a regex),
diffs the branch against origin/main at the merge base, and exits 1 listing
every changed file that no pattern matches. An unmapped file must get a
mapping before the PR can merge: silently skipping it would drop CI
coverage for whatever it affects (docs/ci.md, ADR 0020).

Run from the repository root: ``python3 scripts/check-ci-mapping.py``.
The mapping-coverage CI job runs it on every PR.
"""

import re
import subprocess
import sys
from pathlib import Path

SETUP_CONFIG = Path(".circleci/config.yml")


def load_patterns(config_text):
    """Return the first token of each nonblank line in the first mapping block.

    Return an empty list if no mapping block is found. The first nonblank
    line after the marker establishes the block's indentation.
    """
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
    """Return the files that no mapping pattern matches (re.search).

    Raise re.error if a pattern is invalid.
    """
    compiled = [re.compile(p) for p in patterns]
    return [f for f in files if not any(rx.search(f) for rx in compiled)]


def _run(cmd, check=True):
    """Run a command with captured text output and return its completed process.

    A nonzero exit raises subprocess.CalledProcessError unless check is false;
    errors starting the process propagate.
    """
    return subprocess.run(cmd, check=check, text=True, capture_output=True)


def fetch_main():
    """Fetch origin/main, falling back to an HTTPS GitHub URL for the remote.

    A failed origin fetch triggers the fallback; a failed fallback raises
    subprocess.CalledProcessError. Errors starting a command also propagate.
    """
    try:
        _run(
            [
                "git",
                "fetch",
                "--quiet",
                "origin",
                "+refs/heads/main:refs/remotes/origin/main",
            ]
        )
        return
    except subprocess.CalledProcessError:
        pass
    url = _run(["git", "config", "--get", "remote.origin.url"], check=False).stdout.strip()
    if url.startswith("git@github.com:"):
        slug = url[len("git@github.com:") :]
    elif "github.com/" in url:
        slug = url.split("github.com/", 1)[1]
    else:
        slug = url
    if slug.endswith(".git"):
        slug = slug[:-4]
    _run(
        [
            "git",
            "fetch",
            "--quiet",
            f"https://github.com/{slug}.git",
            "+refs/heads/main:refs/remotes/origin/main",
        ]
    )


def changed_files():
    """Return file names changed between HEAD and its merge base with origin/main.

    Git command failures raise subprocess.CalledProcessError; errors starting
    Git also propagate.
    """
    base = _run(["git", "merge-base", "origin/main", "HEAD"]).stdout.strip()
    out = _run(["git", "diff", "--name-only", base, "HEAD"]).stdout
    return [line for line in out.splitlines() if line]


def main():
    """Check committed changes against CI mappings and return an exit status.

    Return 1 and report to stderr if there are no patterns or any files are
    unmatched; otherwise report the file count to stdout and return 0.
    Config read errors and invalid regexes propagate. A failed origin fetch is
    retried via HTTPS; a failed retry or Git comparison propagates its error.
    """
    patterns = load_patterns(SETUP_CONFIG.read_text())
    if not patterns:
        print(
            "check-ci-mapping: no mapping block found in .circleci/config.yml",
            file=sys.stderr,
        )
        return 1
    fetch_main()
    files = changed_files()
    bad = unmatched_files(files, patterns)
    if bad:
        print(
            "check-ci-mapping: these changed files match no CI mapping pattern:",
            file=sys.stderr,
        )
        for f in bad:
            print(f"  {f}", file=sys.stderr)
        print(
            "Add a mapping for each file in .circleci/config.yml (docs/ci.md).",
            file=sys.stderr,
        )
        return 1
    print(f"check-ci-mapping: {len(files)} changed file(s), all mapped.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
