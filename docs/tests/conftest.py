"""Fail the test run if a test reads a file CI would not rerun it for.

These tests run in CI's lint job (make docs-test, docs/ci.md), which a PR
skips unless a changed file maps to run-lint in .circleci/mapping.txt. A
test that reads a tracked file makes that file an input of the lint job, so
its mapping must say so. An audit hook records every tracked file a test
opens, and the session fails, naming the file, if the mapping does not
route it to run-lint. python/tests/conftest.py does the same for the Python
jobs.
"""

import importlib.util
import os
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
_read = set()


def _audit(event, args):
    if event != "open" or not isinstance(args[0], (str, os.PathLike)):
        return
    mode, flags = args[1], args[2]
    if isinstance(mode, str) and any(c in mode for c in "wax+"):
        return
    if mode is None and flags & (os.O_WRONLY | os.O_RDWR):
        return
    path = os.path.abspath(os.fspath(args[0]))
    if path.startswith(str(ROOT) + os.sep):
        _read.add(os.path.relpath(path, ROOT))


sys.addaudithook(_audit)


def _unrouted(files):
    spec = importlib.util.spec_from_file_location("ci_params", ROOT / "scripts/ci-params.py")
    ci_params = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(ci_params)
    mapping = ci_params.load_mapping((ROOT / ".circleci/mapping.txt").read_text())
    tracked = subprocess.run(
        ["git", "ls-files", "-z", "--", *files], cwd=ROOT, capture_output=True, text=True
    )
    if tracked.returncode != 0:
        return []  # not a git checkout: nothing to check against
    return sorted(
        f
        for f in tracked.stdout.split("\0")
        if f and not {p for rx, p in mapping if rx.search(f)} & {"run-lint", "run-all"}
    )


def pytest_sessionfinish(session, exitstatus):
    bad = _unrouted(sorted(_read)) if _read else []
    if bad:
        reporter = session.config.pluginmanager.get_plugin("terminalreporter")
        lines = [
            "These files are read by the documentation tests, but .circleci/mapping.txt "
            "does not route them to run-lint, so a PR changing one would skip "
            "the tests that read it. Add a line for each (docs/ci.md):",
            *(f"  {f}" for f in bad),
        ]
        if reporter:
            reporter.write_sep("=", "CI mapping", red=True)
            for line in lines:
                reporter.write_line(line)
        session.exitstatus = 1
