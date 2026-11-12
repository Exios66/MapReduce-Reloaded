"""scripts/demo-check.py: the live cluster against what docs/demo.md needs."""

import http.client
import importlib.util
import json
import subprocess
import urllib.error
from pathlib import Path

import pytest
import yaml

ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "scripts" / "demo-check.py"
WORKERS = [f"mapreduce-worker-{i}" for i in range(1, 5)]
COORD = "mapreduce-coordinator-1"
GB = 1_000_000_000
DOCKER_ROOT = "/var/lib/docker"


@pytest.fixture
def dc():
    spec = importlib.util.spec_from_file_location("demo_check", SCRIPT)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def ps_lines(states=None):
    states = states or ["running"] * 4
    rows = [{"Name": COORD, "Service": "coordinator", "State": "running"}]
    pairs = zip(WORKERS, states, strict=True)
    rows += [{"Name": n, "Service": "worker", "State": s} for n, s in pairs]
    return "\n".join(json.dumps(r) for r in rows) + "\n"


def inspect_row(name, cpus, memory, cmd=None):
    return {
        "Name": "/" + name,
        "HostConfig": {"NanoCpus": cpus, "Memory": memory},
        "Config": {"Cmd": cmd or []},
    }


def fake_docker(ps=None, workers_memory=805_306_368, cmd=None):
    def docker(args):
        if args[0] == "version":
            return "29.8.1\n"
        if args[0] == "info":
            return DOCKER_ROOT + "\n"
        if args[0] == "compose":
            return ps if ps is not None else ps_lines()
        if args[0] == "inspect":
            rows = []
            for name in args[1:]:
                if name == COORD:
                    rows.append(
                        inspect_row(
                            name, 0, 268_435_456, cmd or ["coordinator", "-data-dir", "/out/wal"]
                        )
                    )
                else:
                    rows.append(inspect_row(name, 1_000_000_000, workers_memory))
            return json.dumps(rows)
        raise AssertionError(args)

    return docker


def fake_fetch(at=100.0, last_seen=None, workers=4):
    def fetch(url):
        assert url.endswith("/status.json"), url
        seen = [last_seen if last_seen is not None else at - 1] + [at - 1] * (workers - 1)
        ws = [{"id": f"w{i}", "host": f"h{i}", "last_seen": s} for i, s in enumerate(seen)]
        return json.dumps({"at": at, "jobs": [], "workers": ws, "lost": []}).encode()

    return fetch


def ready_root(tmp_path, sort=True):
    (tmp_path / "bin").mkdir()
    mr = tmp_path / "bin/mr"
    mr.write_text("#!/bin/sh\n")
    mr.chmod(0o755)
    corpus = tmp_path / "data/input/corpus"
    corpus.mkdir(parents=True)
    (corpus / "a.txt").write_text("a")
    (tmp_path / "data/input/weblog").mkdir(parents=True)
    (tmp_path / "data/input/weblog/access.log").write_text("x")
    if sort:
        with open(tmp_path / "data/input/sort-1g.txt", "wb") as f:
            f.truncate(1_073_741_824)
    return tmp_path


def checks(dc, root, docker=None, fetch=None, free=10 * GB):
    return dc.run_checks(
        docker or fake_docker(),
        fetch or fake_fetch(),
        root,
        free if callable(free) else (lambda p: free),
        "http://127.0.0.1:18780/",
    )


def by_name(result):
    return {c.name: c for c in result}


def test_a_ready_cluster_passes(dc, tmp_path, monkeypatch, capsys):
    root = ready_root(tmp_path)
    result = checks(dc, root)
    assert [c.name for c in result] == [
        "docker", "workers", "coordinator", "limits", "wal", "status",
        "heartbeat", "disk", "client", "inputs", "sort",
    ]  # fmt: skip
    assert all(c.level == "OK" for c in result), result
    monkeypatch.setattr(dc, "ROOT", root)
    monkeypatch.setattr(dc, "docker", fake_docker())
    monkeypatch.setattr(dc, "fetch", fake_fetch())
    monkeypatch.setattr(dc, "free_bytes", lambda p: 10 * GB)
    assert dc.main([]) == 0
    assert capsys.readouterr().out.count("OK ") == 11


def test_docker_down_fails_once_without_a_traceback(dc, tmp_path):
    calls = []

    def docker(args):
        calls.append(args)
        raise RuntimeError("Cannot connect to the Docker daemon")

    result = checks(dc, ready_root(tmp_path), docker=docker)
    assert [c.level for c in result[:5]] == ["FAIL"] * 5
    assert "docker" in result[0].detail.lower()
    assert len(calls) == 1


def test_unreachable_status_page_names_cluster_up(dc, tmp_path):
    def fetch(url):
        raise urllib.error.URLError("Connection refused")

    c = by_name(checks(dc, ready_root(tmp_path), fetch=fetch))
    assert c["status"].level == "FAIL"
    assert "make cluster-up" in c["status"].detail
    assert c["heartbeat"].level == "FAIL"
    assert c["limits"].level == "OK"  # the Docker checks still ran


def test_an_exited_worker_is_not_counted(dc, tmp_path):
    ps = ps_lines(["running", "running", "running", "exited"])
    c = by_name(checks(dc, ready_root(tmp_path), docker=fake_docker(ps=ps)))
    assert c["workers"] == dc.Check("FAIL", "workers", "3 of 4 running")


def test_compose_ps_as_one_json_array_is_read_too(dc, tmp_path):
    rows = [json.loads(line) for line in ps_lines().splitlines()]
    c = by_name(checks(dc, ready_root(tmp_path), docker=fake_docker(ps=json.dumps(rows))))
    assert c["workers"].level == "OK"


def test_a_stale_heartbeat_names_the_worker(dc, tmp_path):
    c = by_name(checks(dc, ready_root(tmp_path), fetch=fake_fetch(at=100.0, last_seen=80.0)))
    assert c["heartbeat"].level == "FAIL"
    assert "w0" in c["heartbeat"].detail


def test_too_few_workers_on_the_status_page_fail(dc, tmp_path):
    c = by_name(checks(dc, ready_root(tmp_path), fetch=fake_fetch(workers=3)))
    assert c["status"].level == "FAIL"


def test_wrong_limits_fail(dc, tmp_path):
    c = by_name(checks(dc, ready_root(tmp_path), docker=fake_docker(workers_memory=0)))
    assert c["limits"].level == "FAIL"
    assert "mapreduce-worker-1" in c["limits"].detail


def test_a_coordinator_without_its_log_fails(dc, tmp_path):
    c = by_name(checks(dc, ready_root(tmp_path), docker=fake_docker(cmd=["coordinator"])))
    assert c["wal"].level == "FAIL"


def test_low_disk_and_missing_inputs_fail(dc, tmp_path):
    root = ready_root(tmp_path)
    (root / "data/input/weblog/access.log").unlink()
    c = by_name(checks(dc, root, free=5 * GB))
    assert c["disk"].level == "FAIL"
    assert c["inputs"].level == "FAIL"
    assert "make upload-example" in c["inputs"].detail


def test_missing_sort_input_is_a_warning_not_a_failure(dc, tmp_path, monkeypatch):
    root = ready_root(tmp_path, sort=False)
    c = by_name(checks(dc, root))
    assert c["sort"].level == "WARN"
    assert "gen-sort.py" in c["sort"].detail
    monkeypatch.setattr(dc, "ROOT", root)
    monkeypatch.setattr(dc, "docker", fake_docker())
    monkeypatch.setattr(dc, "fetch", fake_fetch())
    monkeypatch.setattr(dc, "free_bytes", lambda p: 10 * GB)
    assert dc.main([]) == 0


def test_paths_resolve_from_the_repository_root(dc, tmp_path, monkeypatch):
    """Run from anywhere: docker runs in the root, so the compose path holds."""
    monkeypatch.chdir(tmp_path)
    assert dc.ROOT == ROOT
    seen = {}

    def run(cmd, **kw):
        seen.update(kw)
        return subprocess.CompletedProcess(cmd, 0, "29.8.1\n", "")

    monkeypatch.setattr(dc.subprocess, "run", run)
    assert dc.docker(["version"]) == "29.8.1\n"
    assert seen["cwd"] == ROOT


@pytest.mark.parametrize("body", [b"null", b"[]", b'{"at": 1, "workers": null}'])
def test_a_status_body_of_the_wrong_shape_fails(dc, tmp_path, body):
    c = by_name(checks(dc, ready_root(tmp_path), fetch=lambda url: body))
    assert c["status"].level == "FAIL"
    assert c["heartbeat"].level == "FAIL"


def test_a_broken_http_response_fails_without_a_traceback(dc, tmp_path):
    def fetch(url):
        raise http.client.BadStatusLine("\x00")

    c = by_name(checks(dc, ready_root(tmp_path), fetch=fetch))
    assert c["status"].level == "FAIL"


def test_unexpected_docker_output_fails_its_checks_not_the_script(dc, tmp_path):
    def docker(args):
        return "[{}]" if args[0] == "inspect" else fake_docker()(args)

    result = checks(dc, ready_root(tmp_path), docker=docker)
    c = by_name(result)
    assert c["limits"].level == "FAIL"
    assert c["disk"].level == "OK"  # the other sections still ran


def test_a_stopped_coordinator_is_not_blamed_on_the_log(dc, tmp_path):
    ps = "\n".join(line for line in ps_lines().splitlines() if "coordinator" not in line)
    c = by_name(checks(dc, ready_root(tmp_path), docker=fake_docker(ps=ps)))
    assert c["coordinator"].level == "FAIL"
    assert c["wal"] == dc.Check("FAIL", "wal", "coordinator not running")


def test_pinned_limits_match_compose_yaml(dc):
    services = yaml.safe_load((ROOT / "deploy/compose/compose.yaml").read_text())["services"]
    assert services["worker"]["cpus"] * 1_000_000_000 == dc.WORKER_NANO_CPUS
    assert services["worker"]["mem_limit"] == "768m"
    assert dc.WORKER_MEMORY == 768 * 1024 * 1024
    assert services["coordinator"]["mem_limit"] == "256m"
    assert dc.COORDINATOR_MEMORY == 256 * 1024 * 1024
    assert "-data-dir" in services["coordinator"]["command"]
    assert services["worker"].get("deploy", {}).get("replicas", dc.WORKERS) == dc.WORKERS


def test_docker_errors_keep_the_cause_not_the_usage_hint(dc, monkeypatch):
    """A VM without the Compose plugin: the cause is the first line."""

    def run(cmd, **kw):
        err = "docker: 'compose' is not a docker command.\nSee 'docker --help'\n"
        return subprocess.CompletedProcess(cmd, 1, "", err)

    monkeypatch.setattr(dc.subprocess, "run", run)
    with pytest.raises(RuntimeError, match="'compose' is not a docker command"):
        dc.docker(["compose", "ps"])


def test_an_http_error_says_the_server_answered(dc, tmp_path):
    def fetch(url):
        raise urllib.error.HTTPError(url, 500, "Internal Server Error", {}, None)

    c = by_name(checks(dc, ready_root(tmp_path), fetch=fetch))
    assert c["status"].level == "FAIL"
    assert "HTTP 500" in c["status"].detail
    assert "unreachable" not in c["status"].detail


@pytest.mark.parametrize("at", [None, "soon"])
def test_a_status_without_its_time_cannot_pass_the_heartbeat(dc, tmp_path, at):
    def fetch(url):
        ws = [{"id": f"w{i}", "last_seen": 99.0} for i in range(4)]
        body = {"workers": ws} if at is None else {"at": at, "workers": ws}
        return json.dumps(body).encode()

    c = by_name(checks(dc, ready_root(tmp_path), fetch=fetch))
    assert c["heartbeat"].level == "FAIL"
    assert "time" in c["heartbeat"].detail


def test_the_disk_check_measures_dockers_disk_too(dc, tmp_path):
    root = ready_root(tmp_path)

    def free(path):
        return 2 * GB if str(path) == DOCKER_ROOT else 10 * GB

    c = by_name(checks(dc, root, free=free))
    assert c["disk"].level == "FAIL"
    assert DOCKER_ROOT in c["disk"].detail


def test_the_disk_check_falls_back_to_the_repository_without_docker(dc, tmp_path):
    def docker(args):
        raise RuntimeError("Cannot connect to the Docker daemon")

    seen = []

    def free(path):
        seen.append(str(path))
        return 10 * GB

    c = by_name(checks(dc, ready_root(tmp_path), docker=docker, free=free))
    assert c["disk"].level == "OK"
    assert seen == [str(tmp_path)]


def test_an_empty_docker_root_is_not_measured_as_the_current_directory(dc, tmp_path):
    def docker(args):
        return "\n" if args[0] == "info" else fake_docker()(args)

    seen = []

    def free(path):
        seen.append(str(path))
        return 10 * GB

    checks(dc, ready_root(tmp_path), docker=docker, free=free)
    assert seen == [str(tmp_path)]
