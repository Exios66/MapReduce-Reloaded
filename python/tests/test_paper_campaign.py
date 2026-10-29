"""Campaign preflight must fail before touching Docker or publishing bad input."""

import importlib.util
import json
import os
import shutil
import subprocess
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]


@pytest.mark.parametrize(
    "state,expected",
    [("MAPPING", 0), ("REDUCING", 0), ("SUCCEEDED", 1), ("FAILED", 1), ("RUNNING", 1)],
)
def test_failure_injection_accepts_only_coordinator_active_states(state, expected):
    script = (ROOT / "bench/paper.sh").read_text()
    body = script.split("\nactive_state() {\n", 1)[1].split("\n}\n", 1)[0]
    result = subprocess.run(
        ["bash", "-c", "active_state() {\n" + body + '\n}\nactive_state "$1"', "test", state]
    )
    assert result.returncode == expected


def test_terminal_evidence_rejects_stale_or_missing_collectors(tmp_path):
    spec = importlib.util.spec_from_file_location(
        "evidence", ROOT / "bench/check-paper-evidence.py"
    )
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    (tmp_path / "clock.json").write_text(json.dumps({"monotonic": 100}))
    (tmp_path / "workers.json").write_text(json.dumps([{"Name": f"/w{i}"} for i in range(4)]))
    samples = "seconds,url,metric,value\n" + "".join(
        f"{second},u{i},{metric},0\n"
        for second in (0, 11)
        for i in range(4)
        for metric in module.COUNTERS
    )
    tasks = "seconds,job,kind,state,value\n11,job-1,map,completed,16\n11,job-1,reduce,completed,8\n"
    resources = "".join(
        json.dumps({"epoch": 1011, "name": f"w{i}", "memory": "1MiB", "cpu": "0%"}) + "\n"
        for i in range(4)
    )
    for filename, text in (
        ("samples.csv", samples),
        ("tasks.csv", tasks),
        ("resources.jsonl", resources),
    ):
        (tmp_path / filename).write_text(text)
    assert module.check(tmp_path, "job-1", 16, 8, 110, 1010)["verified"]
    for filename, bad in (
        ("samples.csv", samples.replace("11,", "9,")),
        ("tasks.csv", tasks.replace("job-1", "job-2")),
        ("resources.jsonl", resources.replace("1011", "1009")),
    ):
        path = tmp_path / filename
        original = path.read_text()
        path.write_text(bad)
        with pytest.raises(AssertionError):
            module.check(tmp_path, "job-1", 16, 8, 110, 1010)
        path.write_text(original)


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
    df.write_text(
        "#!/bin/sh\nprintf 'Filesystem 1B-blocks Used Available Use%% Mounted\\n"
        "disk 100 100 0 100%% /\\n'\n"
    )
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
