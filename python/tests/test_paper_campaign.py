"""Campaign preflight must fail before touching Docker or publishing bad input."""

import hashlib
import importlib.util
import json
import os
import shutil
import signal
import subprocess
import sys
import time
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
    (tmp_path / "clock.json").write_text(json.dumps({"monotonic": 100, "epoch": 1000}))
    (tmp_path / "submission-clock.json").write_text(
        json.dumps({"monotonic": 101, "epoch": 1001, "interval": 1})
    )
    (tmp_path / "workers.json").write_text(json.dumps([{"Name": f"/w{i}"} for i in range(4)]))
    samples = "seconds,url,metric,value\n" + "".join(
        f"{second},u{i},{metric},0\n"
        for second in range(12)
        for i in range(4)
        for metric in module.COUNTERS
    )
    tasks = "seconds,job,kind,state,value\n" + "".join(
        f"{second},job-1,map,completed,16\n{second},job-1,reduce,completed,8\n"
        for second in range(1, 12)
    )
    resources = "".join(
        json.dumps({"epoch": 1000 + second, "name": f"w{i}", "memory": "1MiB", "cpu": "0%"})
        + "\n"
        for second in range(12)
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
        (
            "samples.csv",
            "\n".join(r for r in samples.splitlines() if not r.startswith(("10,", "11,")))
            + "\n",
        ),
        ("tasks.csv", tasks.replace("job-1", "job-2")),
        ("resources.jsonl", resources.replace("1010", "1009").replace("1011", "1009")),
        (
            "samples.csv",
            "\n".join(
                r for r in samples.splitlines()
                if not any(r.startswith(f"{t},u0,") for t in range(2, 9))
            ) + "\n",
        ),
        (
            "tasks.csv",
            "\n".join(
                r for r in tasks.splitlines()
                if not any(r.startswith(f"{t},") for t in range(2, 9))
            ) + "\n",
        ),
        (
            "resources.jsonl",
            "".join(
                line + "\n" for line in resources.splitlines()
                if not 1001 < json.loads(line)["epoch"] < 1009
            ),
        ),
    ):
        path = tmp_path / filename
        original = path.read_text()
        path.write_text(bad)
        with pytest.raises(ValueError):
            module.check(tmp_path, "job-1", 16, 8, 110, 1010)
        path.write_text(original)
    # Only the explicitly recorded worker interruption may have a longer gap.
    (tmp_path / "restart.json").write_text(json.dumps({"url": "u0"}))
    (tmp_path / "events.csv").write_text(
        "event,epoch,exit_code\nkill_requested,1002,0\nregistered,1008,0\n"
    )
    gap = "\n".join(
        r for r in samples.splitlines()
        if not any(r.startswith(f"{t},u0,") for t in range(4, 8))
    ) + "\n"
    (tmp_path / "samples.csv").write_text(gap)
    assert module.check(tmp_path, "job-1", 16, 8, 110, 1010)["verified"]
    (tmp_path / "samples.csv").write_text(
        "\n".join(row for row in gap.splitlines() if ",u1," not in row) + "\n"
    )
    result = subprocess.run(
        [
            sys.executable, ROOT / "bench/check-paper-evidence.py", str(tmp_path),
            "job-1", "16", "8", "110", "1010",
        ],
        env=dict(os.environ, PYTHONOPTIMIZE="1"),
        capture_output=True,
        text=True,
    )
    assert result.returncode != 0
    assert '"verified": true' not in result.stdout


@pytest.fixture
def campaign(tmp_path):
    (tmp_path / "bench").mkdir()
    (tmp_path / "bin").mkdir()
    shutil.copyfile(ROOT / "bench/paper.sh", tmp_path / "bench/paper.sh")
    for name in ("mr", "paper-data"):
        path = tmp_path / "bin" / name
        path.write_text("#!/bin/sh\nexit 0\n")
        path.chmod(0o755)
    fake = tmp_path / "fake"
    fake.mkdir()
    df = fake / "df"
    df.write_text(
        "#!/bin/sh\nprintf 'Filesystem 1B-blocks Used Available Use%% Mounted\\n"
        "disk 100000000000 0 100000000000 0%% /\\n'\n"
    )
    df.chmod(0o755)

    def run(*args, **overrides):
        env = dict(
            os.environ, HOME=str(tmp_path), SIZE="200", M="2",
            PATH=f"{fake}:{os.environ['PATH']}",
        )
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
    (root / "bench/gen-grep.py").write_text(
        "from pathlib import Path\n"
        "Path('generator-ran').touch()\n"
        "raise SystemExit(1)\n"
    )
    result = run("prepare", "grep", "native")
    assert result.returncode != 0
    assert (root / "generator-ran").exists()
    assert not (root / "data/paper-input-grep-native-200-2").exists()


def test_restart_requires_event_time_before_docker(campaign):
    _, run = campaign
    result = run("run", "sort", "zstdkv", "restart", EVENT_AFTER="bad")
    assert result.returncode != 0
    assert "EVENT_AFTER" in result.stderr


@pytest.fixture
def prepared_campaign(campaign):
    root, run = campaign
    data = root / "data/paper-input-grep-native-200-2"
    data.mkdir(parents=True)
    for i in range(2):
        (data / f"part-m-{i:05d}.txt").write_bytes(b"a" * 99 + b"\n")
    (data / "manifest.json").write_text(json.dumps({
        "records": 2, "logical_bytes": 200, "physical_bytes": 200,
        "shards": 2, "format": "text",
    }))
    commit = "a" * 40
    image = "sha256:" + "b" * 64
    stamp = {"commit": commit, "image": image, "tools": {
        name: hashlib.sha256((root / name).read_bytes()).hexdigest()
        for name in ("bin/mr", "bin/paper-data")
    }}
    (root / "bin/paper-build.json").write_text(json.dumps(stamp))
    git = root / "fake/git"
    git.write_text(
        "#!/bin/sh\ncase \"$1\" in\n"
        f"rev-parse) printf '%s\\n' '{commit}' ;;\n"
        "status) printf '%s' \"${DIRTY_SOURCE:-}\" ;;\n*) exit 1 ;;\nesac\n"
    )
    git.chmod(0o755)
    docker = root / "fake/docker"
    docker.write_text(
        f"#!{sys.executable}\n"
        "import json, os, sys\nfrom pathlib import Path\n"
        f"image = {image!r}\ncommit = {commit!r}\n"
        "args = sys.argv[1:]\n"
        "with Path('docker-calls').open('a') as f: f.write(json.dumps(args) + '\\n')\n"
        "if args[:2] == ['image', 'inspect']:\n"
        "    labels = {'org.opencontainers.image.revision': commit}\n"
        "    print(json.dumps([{'Id': image, 'Config': {'Labels': labels}}]))\n"
        "elif args[0] == 'compose':\n"
        "    if 'config' in args: print('{}')\n"
        "    elif 'ps' in args: print('w1\\nw2\\nw3\\nw4' if args[-1] == 'worker' else 'c1')\n"
        "elif args[0] == 'inspect':\n"
        "    if '-f' in args: print('172.20.0.' + args[-1][-1])\n"
        "    else:\n"
        "        coordinator_is_bad = os.environ.get('BAD_SERVICE') == 'coordinator'\n"
        "        records = [{'Name': '/' + name,\n"
        "                    'Image': 'wrong-image'\n"
        "                    if (name == 'c1') == coordinator_is_bad else image}\n"
        "                   for name in args[1:]]\n"
        "        print(json.dumps(records))\n"
        "else: raise SystemExit(1)\n"
    )
    docker.chmod(0o755)
    curl = root / "fake/curl"
    curl.write_text("#!/bin/sh\nprintf 'mr_workers 4\\n'\n")
    curl.chmod(0o755)
    return root, run, image


def test_untracked_source_blocks_run_before_docker(prepared_campaign):
    root, run, _ = prepared_campaign
    result = run("run", "grep", "native", DIRTY_SOURCE="?? go/extra.go\n")
    assert result.returncode != 0
    assert "including untracked files" in result.stderr
    assert not (root / "docker-calls").exists()


def test_changed_input_blocks_run_before_docker(prepared_campaign):
    root, run, _ = prepared_campaign
    part = root / "data/paper-input-grep-native-200-2/part-m-00000.txt"
    part.write_bytes(b"")
    result = run("run", "grep", "native")
    assert result.returncode != 0
    assert "input sizes changed" in result.stderr
    assert not (root / "docker-calls").exists()


@pytest.mark.parametrize("service", ["worker", "coordinator"])
def test_services_are_pinned_and_wrong_running_image_is_rejected(prepared_campaign, service):
    root, run, image = prepared_campaign
    result = run("run", "grep", "native", BAD_SERVICE=service)
    assert result.returncode != 0
    assert "running services do not match the pinned image" in result.stderr
    calls = [json.loads(line) for line in (root / "docker-calls").read_text().splitlines()]
    assert calls[0] == ["image", "inspect", image]
    starts = [args for args in calls if args[0] == "compose" and "up" in args]
    assert len(starts) == 1
    assert "--no-build" in starts[0]
    assert starts[0][starts[0].index("--pull") + 1] == "never"
    override = next((root / "bench/results").glob("*/compose.json"))
    services = json.loads(override.read_text())["services"]
    assert all(s["image"] == image and s["pull_policy"] == "never" for s in services.values())
    assert ["inspect", "c1"] in calls


@pytest.mark.parametrize("records,physical", [(1, 200), (2, 100)])
def test_result_rejects_incomplete_executed_input(tmp_path, records, physical):
    # Exercise the production result gate, not a copy of its conditions.
    script = (ROOT / "bench/paper.sh").read_text()
    body = script.rsplit("<<'PY'\n", 1)[1].split("\nPY\n", 1)[0]
    (tmp_path / "status.txt").write_text(
        "job-1 SUCCEEDED\nmap 2/2\nreduce 1/1\n"
        f"map.input.records {records}\nmap.input.bytes {physical}\n"
    )
    (tmp_path / "input-manifest.json").write_text(json.dumps({"records": 2, "physical_bytes": 200}))
    args = [
        str(tmp_path), "grep", "native", "normal", "200", "2", "1",
        "0", "0", "1", "100", "head", "0", "0", "1",
    ]
    result = subprocess.run([sys.executable, "-c", body, *args], capture_output=True, text=True)
    assert result.returncode != 0
    assert "executed input does not match" in result.stderr
    assert not (tmp_path / "result.json").exists()


@pytest.mark.parametrize("untracked", [False, True])
def test_paper_build_checks_untracked_source_and_uses_build_iid(tmp_path, untracked):
    repo = tmp_path / "repo"
    repo.mkdir()
    shutil.copyfile(ROOT / "Makefile", repo / "Makefile")
    shutil.copyfile(ROOT / ".gitignore", repo / ".gitignore")
    (repo / "go").mkdir()
    (repo / "go/keep").touch()
    for args in (
        ("init",),
        ("add", "."),
        (
            "-c", "user.name=Test", "-c", "user.email=test@example.org",
            "-c", "core.hooksPath=/dev/null", "commit", "-m", "fixture",
        ),
    ):
        subprocess.run(["git", *args], cwd=repo, check=True, capture_output=True)
    fake = tmp_path / "fake"
    fake.mkdir()
    (repo / ".tools").symlink_to(fake, target_is_directory=True)
    go = fake / "go"
    go.write_text("#!/bin/sh\nprintf 'tool' > \"$3\"\n")
    go.chmod(0o755)
    image = "sha256:" + "c" * 64
    docker = fake / "docker"
    docker.write_text(
        f"#!{sys.executable}\n"
        "import sys\nfrom pathlib import Path\n"
        "args = sys.argv[1:]\n"
        "assert args[0] == 'build'\n"
        f"Path(args[args.index('--iidfile') + 1]).write_text({image!r})\n"
    )
    docker.chmod(0o755)
    if untracked:
        (repo / "go/extra.go").write_text("package extra\n")
    result = subprocess.run(
        ["make", "paper-tools", f"PYTHON={sys.executable}"], cwd=repo,
        env=dict(os.environ, PATH=f"{fake}:{os.environ['PATH']}"),
        capture_output=True, text=True,
    )
    if untracked:
        assert result.returncode != 0
        assert not (repo / "bin").exists()
    else:
        assert result.returncode == 0, result.stderr
        stamp = json.loads((repo / "bin/paper-build.json").read_text())
        assert stamp["image"] == image


def test_resource_sampler_joins_its_active_child(tmp_path):
    script = (ROOT / "bench/paper.sh").read_text()
    body = script.split("\n(\n    child=''\n", 1)[1]
    body = "child=''\n" + body.split('\n) > "$dir/resources.jsonl"', 1)[0]
    docker = tmp_path / "docker"
    docker.write_text(
        f"#!{sys.executable}\n"
        "import signal\nfrom pathlib import Path\n"
        "def stop(*_):\n"
        "    Path('child-stopped').touch()\n"
        "    raise SystemExit(0)\n"
        "signal.signal(signal.SIGTERM, stop)\n"
        "Path('child-started').touch()\n"
        "while True: signal.pause()\n"
    )
    docker.chmod(0o755)
    process = subprocess.Popen(
        ["bash", "-c", "ids=(worker)\ninterval=0.05\n" + body],
        cwd=tmp_path, env=dict(os.environ, PATH=f"{tmp_path}:{os.environ['PATH']}"),
        stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=True,
    )
    try:
        limit = time.monotonic() + 3
        while not (tmp_path / "child-started").exists() and time.monotonic() < limit:
            time.sleep(0.01)
        assert (tmp_path / "child-started").exists()
        process.terminate()
        process.communicate(timeout=5)
        assert (tmp_path / "child-stopped").exists()
    finally:
        if process.poll() is None:
            os.killpg(process.pid, signal.SIGKILL)
            process.communicate()
