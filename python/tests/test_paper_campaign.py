"""Campaign preflight must fail before touching Docker or publishing bad input."""

import os
import shutil
import subprocess
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]


@pytest.fixture
def campaign(tmp_path):
    (tmp_path / "bench").mkdir()
    (tmp_path / "bin").mkdir()
    shutil.copyfile(ROOT / "bench/paper.sh", tmp_path / "bench/paper.sh")
    for name in ("mr", "paper-data"):
        path = tmp_path / "bin" / name
        path.write_text("#!/bin/sh\nexit 0\n")
        path.chmod(0o755)

    def run(*args, **overrides):
        env = dict(os.environ, HOME=str(tmp_path), SIZE="200", M="2")
        env.update(overrides)
        return subprocess.run(
            ["bash", "bench/paper.sh", *args],
            cwd=tmp_path,
            env=env,
            capture_output=True,
            text=True,
            timeout=10,
        )

    return tmp_path, run


@pytest.mark.parametrize("size,m", [("-1", "2"), ("201", "2"), ("100", "2"), ("10000000000", "32")])
def test_paper_rejects_invalid_workload_before_preparation(campaign, size, m):
    root, run = campaign
    result = run("prepare", "sort", "native", SIZE=size, M=m)
    assert result.returncode != 0
    assert not (root / "data").exists()


def test_paper_preparation_gates_disk_before_generating(campaign):
    root, run = campaign
    fake = root / "fake"
    fake.mkdir()
    df = fake / "df"
    df.write_text("#!/bin/sh\nprintf 'Filesystem 1B-blocks Used Available Use%% Mounted\\ndisk 100 100 0 100%% /\\n'\n")
    df.chmod(0o755)
    result = run("prepare", "grep", "native", PATH=f"{fake}:{os.environ['PATH']}")
    assert result.returncode != 0
    assert "no job started" in result.stderr
    assert not (root / "data").exists()


def test_failed_generator_does_not_publish_prepared_input(campaign):
    root, run = campaign
    (root / "bench/gen-grep.py").write_text("raise SystemExit(1)\n")
    result = run("prepare", "grep", "native")
    assert result.returncode != 0
    assert not (root / "data/paper-input-grep-native-200-2").exists()


def test_restart_requires_event_time_before_docker(campaign):
    _, run = campaign
    result = run("run", "sort", "zstdkv", "restart", EVENT_AFTER="bad")
    assert result.returncode != 0
    assert "EVENT_AFTER" in result.stderr
