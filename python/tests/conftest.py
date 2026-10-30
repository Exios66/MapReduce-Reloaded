"""Fail the test run if a test reads a file CI would not rerun it for.

CI skips the Python jobs on a PR unless a changed file maps to them in
.circleci/mapping.txt (docs/ci.md). A test that reads a tracked file outside
python/ makes that file an input of the Python jobs, so its mapping must say
so; otherwise a PR changing the file would skip the test that checks it.
An audit hook records every such file a test opens, and the session fails,
naming the file, if the mapping does not route it to run-python.
"""

import importlib.util
import os
import subprocess
import sys
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]
_OWN = str(ROOT / "python") + os.sep
_read = set()
_recording = [True]


def _audit(event, args):
    if event != "open" or not _recording[0] or not isinstance(args[0], (str, os.PathLike)):
        return
    mode, flags = args[1], args[2]
    if isinstance(mode, str) and any(c in mode for c in "wax+"):
        return
    if mode is None and flags & (os.O_WRONLY | os.O_RDWR):
        return
    path = os.path.abspath(os.fspath(args[0]))
    if path.startswith(str(ROOT) + os.sep) and not path.startswith(_OWN):
        _read.add(os.path.relpath(path, ROOT))


sys.addaudithook(_audit)


def pytest_configure(config):
    config.addinivalue_line(
        "markers",
        "lint_inputs: the test sweeps the files the lint job checks; changing "
        "them runs lint, so its reads need no run-python mapping",
    )


@pytest.hookimpl(tryfirst=True)
def pytest_runtest_call(item):
    _recording[0] = item.get_closest_marker("lint_inputs") is None


def pytest_runtest_teardown(item):
    _recording[0] = True


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
        if f and not {p for rx, p in mapping if rx.search(f)} & {"run-python", "run-all"}
    )


def pytest_sessionfinish(session, exitstatus):
    bad = _unrouted(sorted(_read)) if _read else []
    if bad:
        reporter = session.config.pluginmanager.get_plugin("terminalreporter")
        lines = [
            "These files are read by the Python tests, but .circleci/mapping.txt "
            "does not route them to run-python, so a PR changing one would skip "
            "the tests that read it. Add a line for each (docs/ci.md):",
            *(f"  {f}" for f in bad),
        ]
        if reporter:
            reporter.write_sep("=", "CI mapping", red=True)
            for line in lines:
                reporter.write_line(line)
        session.exitstatus = 1
