"""Deployment contracts checked without starting containers (#158)."""

import json
import os
import re
import shutil
import subprocess
from pathlib import Path

import pytest
import yaml

ROOT = Path(__file__).resolve().parents[2]
COMPOSE = ROOT / "deploy/compose/compose.yaml"
MONITORING = COMPOSE.parent / "monitoring"


def load(path):
    return yaml.safe_load(path.read_text())


def documented_limits(text):
    limits = {}
    for line in text.splitlines():
        cells = [cell.strip().replace("`", "") for cell in line.split("|")[1:-1]]
        if len(cells) < 2:
            continue
        memory = re.fullmatch(r"(\d+) MB(?: each)?", cells[1])
        if not memory:
            continue
        names = cells[0].split(" (")[0].split(", ")
        for name in names:
            limits[name] = int(memory[1])
    return limits


def test_compose_memory_limits_match_vm_documentation():
    services = load(COMPOSE)["services"]
    documented = documented_limits((ROOT / "docs/cluster-vm.md").read_text())
    assert set(documented) == set(services)
    for name, service in services.items():
        assert service["mem_limit"] == f"{documented[name]}m", name


def test_documented_memory_totals_include_replicas_and_profiles():
    services = load(COMPOSE)["services"]
    core = sum(
        int(service["mem_limit"].removesuffix("m")) * service.get("deploy", {}).get("replicas", 1)
        for service in services.values()
        if not service.get("profiles")
    )
    with_tools_and_s3 = core + sum(
        int(services[name]["mem_limit"].removesuffix("m")) for name in ("mr", "s3")
    )
    everything = with_tools_and_s3 + sum(
        int(services[name]["mem_limit"].removesuffix("m")) for name in ("prometheus", "grafana")
    )
    text = (ROOT / "docs/cluster-vm.md").read_text()
    totals = [int(value.replace(",", "")) for value in re.findall(r"\*\*([\d,]+) MB", text)]
    assert totals == [core, with_tools_and_s3, everything]


def test_prometheus_preserves_mapreduce_job_labels():
    scrapes = load(MONITORING / "prometheus.yml")["scrape_configs"]
    coordinator = [scrape for scrape in scrapes if scrape["job_name"] == "coordinator"]
    assert len(coordinator) == 1
    scrape = coordinator[0]
    assert scrape.get("honor_labels") is True
    assert "coordinator:8080" in [
        target for config in scrape["static_configs"] for target in config["targets"]
    ]
    for rule in scrape.get("metric_relabel_configs", []):
        assert rule.get("target_label") not in {"job", "exported_job"}
        assert rule.get("action") not in {"labeldrop", "labelkeep", "labelmap"}


def test_worker_scraping_discovers_all_replicas():
    scrapes = load(MONITORING / "prometheus.yml")["scrape_configs"]
    worker = [scrape for scrape in scrapes if scrape["job_name"] == "worker"]
    assert len(worker) == 1
    assert {"names": ["worker"], "type": "A", "port": 8081} in worker[0]["dns_sd_configs"]


def test_dashboard_datasources_are_provisioned():
    provisioning = MONITORING / "grafana/provisioning/datasources"
    sources = {
        source["uid"]: source
        for path in provisioning.glob("*.yml")
        for source in load(path)["datasources"]
    }
    dashboard = load(MONITORING / "grafana/dashboards/mapreduce.json")
    assert dashboard["panels"]
    for panel in dashboard["panels"]:
        datasource = panel["datasource"]
        assert datasource["uid"] in sources
        source = sources[datasource["uid"]]
        assert source["type"] == datasource["type"] == "prometheus"
        assert source["url"] == "http://prometheus:9090"


@pytest.fixture(scope="module")
def docker_compose():
    docker = shutil.which("docker")
    if not docker:
        pytest.skip("Docker CLI not installed; skipping Compose validation")
    version = subprocess.run(
        [docker, "compose", "version"], capture_output=True, text=True, timeout=15
    )
    if version.returncode:
        pytest.skip(f"Docker Compose plugin unavailable: {version.stderr.strip()}")
    return [docker, "compose", "-f", str(COMPOSE)]


@pytest.mark.parametrize("profiles", [(), ("s3",), ("monitoring",), ("tools",), ("*",)])
def test_compose_profiles_validate_without_daemon(docker_compose, profiles):
    command = list(docker_compose)
    for profile in profiles:
        command.extend(["--profile", profile])
    result = subprocess.run(
        [*command, "config", "--format", "json"],
        check=True,
        capture_output=True,
        text=True,
        cwd=ROOT,
        env=os.environ | {"COMPOSE_PROFILES": ""},
        timeout=30,
    )
    services = json.loads(result.stdout)["services"]
    expected = {
        name
        for name, service in load(COMPOSE)["services"].items()
        if not service.get("profiles")
        or "*" in profiles
        or set(service["profiles"]) & set(profiles)
    }
    assert set(services) == expected


def test_backups_override_keeps_recovery_enabled(docker_compose):
    result = subprocess.run(
        [
            *docker_compose,
            "-f",
            str(COMPOSE.with_name("backups-off.yaml")),
            "config",
            "--format",
            "json",
        ],
        check=True,
        capture_output=True,
        text=True,
        env=os.environ | {"COMPOSE_PROFILES": ""},
        timeout=30,
    )
    command = json.loads(result.stdout)["services"]["coordinator"]["command"]
    assert "-backups=false" in command
    assert command[command.index("-data-dir") + 1] == "/out/wal"


def test_speculation_override_keeps_recovery_enabled(docker_compose):
    result = subprocess.run(
        [
            *docker_compose,
            "-f",
            str(COMPOSE.with_name("speculation-late.yaml")),
            "config",
            "--format",
            "json",
        ],
        check=True,
        capture_output=True,
        text=True,
        env=os.environ | {"COMPOSE_PROFILES": ""},
        timeout=30,
    )
    command = json.loads(result.stdout)["services"]["coordinator"]["command"]
    assert "-speculation=late" in command
    assert "-backups=false" not in command
    assert command[command.index("-data-dir") + 1] == "/out/wal"


def test_soak_override_lowers_the_worker_limit(docker_compose):
    result = subprocess.run(
        [
            *docker_compose,
            "-f",
            str(COMPOSE.with_name("soak.yaml")),
            "config",
            "--format",
            "json",
        ],
        check=True,
        capture_output=True,
        text=True,
        env=os.environ | {"COMPOSE_PROFILES": ""},
        timeout=30,
    )
    worker = json.loads(result.stdout)["services"]["worker"]
    # Compose keeps "256m" in one version and normalizes it to bytes in another.
    limit = worker["mem_limit"]
    assert (int(limit[:-1]) << 20 if limit.endswith("m") else int(limit)) == 256 << 20
    assert worker["command"][0] == "worker"


def test_promtool_config():
    promtool = shutil.which("promtool")
    if not promtool:
        pytest.skip("promtool not installed; skipping external Prometheus config validation")
    subprocess.run(
        [promtool, "check", "config", str(MONITORING / "prometheus.yml")],
        check=True,
        capture_output=True,
        text=True,
        timeout=30,
    )
