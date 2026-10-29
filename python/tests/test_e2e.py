"""e2e.sh's checks, with fake docker, curl and mr commands; no cluster."""

import os
import shutil
import subprocess
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]

pytestmark = pytest.mark.skipif(shutil.which("timeout") is None, reason="needs timeout(1)")

DOCKER = """#!/bin/sh
case "$*" in
*"image inspect"*) echo "${FAKE_USER:-65532}" ;;
*"--help"*) echo "Usage of fake:" ;;
*"ps -aq"*) echo c1 ;;
*"inspect -f"*) echo "mapreduce-worker-1 ${FAKE_OOM:-false}" ;;
*"ps -q coordinator"*) echo c1 ;;
*"mr status"*) printf 'job-1\\tSUCCEEDED\\n' ;;
esac
"""

CURL = """#!/bin/sh
case "$*" in
*19090*) echo "{\\"data\\":{\\"result\\":[{\\"metric\\":{\\"job\\":\\"${FAKE_JOB:-job-1}\\"}}]}}" ;;
*) echo 'mr_job_tasks{job="job-1",kind="reduce",state="completed"} 4' ;;
esac
"""

MR = """#!/bin/sh
case "$1" in
upload) ;;
submit)
    for a in "$@"; do case "$a" in /data/out/*) out=$a ;; esac; done
    mkdir -p "data${out#/data}"
    cp -r testdata/corpus/expected/. "data${out#/data}/"
    printf 'job-1\\tSUCCEEDED\\nmap\\t7/7\\nreduce\\t4/4\\n' ;;
cat) cat testdata/corpus/expected/part-r-* ;;
status) printf 'job-1\\tSUCCEEDED\\n' ;;
esac
"""


@pytest.fixture
def repo(tmp_path):
    """A copy of the repository with fake docker, curl and mr on the path."""
    for name in ("scripts", "bin", "fakebin", "data/out", "testdata/corpus"):
        (tmp_path / name).mkdir(parents=True)
    shutil.copy(ROOT / "scripts/e2e.sh", tmp_path / "scripts" / "e2e.sh")
    shutil.copytree(ROOT / "testdata" / "corpus" / "expected", tmp_path / "testdata/corpus/expected")
    for name, body in (("docker", DOCKER), ("curl", CURL)):
        path = tmp_path / "fakebin" / name
        path.write_text(body)
        path.chmod(0o755)
    mr = tmp_path / "bin" / "mr"
    mr.write_text(MR)
    mr.chmod(0o755)
    return tmp_path


def run(repo, **env):
    return subprocess.run(
        ["bash", "scripts/e2e.sh"],
        cwd=repo,
        env={**os.environ, "PATH": f"{repo / 'fakebin'}:{os.environ['PATH']}", **env},
        capture_output=True,
        text=True,
        timeout=60,
    )


def test_local_run_passes(repo):
    result = run(repo, E2E_PROMETHEUS_TRIES="1")
    assert result.returncode == 0, result.stderr
    assert "output matches the golden output" in result.stdout


def test_a_killed_container_fails_the_run(repo):
    result = run(repo, E2E_PROMETHEUS_TRIES="1", FAKE_OOM="true")
    assert result.returncode == 1
    assert "memory limit" in result.stderr


def test_a_root_image_fails_the_run(repo):
    result = run(repo, E2E_PROMETHEUS_TRIES="1", FAKE_USER="0")
    assert result.returncode == 1
    assert "runs as root" in result.stderr


def test_prometheus_must_label_the_series_with_the_job(repo):
    result = run(repo, E2E_PROMETHEUS_TRIES="1", FAKE_JOB="job-other")
    assert result.returncode == 1
    assert "does not label mr_job_tasks" in result.stderr
