"""Documentation contracts: compare the guides with local sources, without a cluster.

These checks cover the status corrections and new guides in PR #283. Historical
roadmap entries and external facts (GitHub settings, PR counts, VM measurements)
are deliberately not inferred from the checkout.
"""

import re
from pathlib import Path

import pytest
import yaml

ROOT = Path(__file__).resolve().parents[2]


def prose(path):
    """Ignore Markdown code delimiters and wrapping when checking concrete claims."""
    return " ".join((ROOT / path).read_text(encoding="utf-8").replace("`", "").split())


@pytest.fixture(scope="module")
def services():
    return yaml.safe_load((ROOT / "deploy/compose/compose.yaml").read_text())["services"]


def test_examples_guide_lists_the_registered_programs():
    guide = (ROOT / "examples/README.md").read_text()
    compiled = guide.split("The compiled example programs", 1)[1].split("—", 2)[1]
    documented = re.findall(r"`([^`]+)`", compiled)
    registered = [
        name
        for source in (ROOT / "go/examples").glob("*/*.go")
        if not source.name.endswith("_test.go")
        for name in re.findall(r'api\.Register\("([^"]+)"', source.read_text())
    ]
    assert documented
    assert len(documented) == len(set(documented)), "duplicate example in the guide"
    assert set(documented) == set(registered)


def test_examples_guide_describes_a_source_job_and_the_runbook_notebook():
    guide = prose("examples/README.md")
    assert re.search(r"source is sent.*interpreted by the workers", guide)
    assert "no compiled binary is uploaded" in guide
    assert re.search(r"demo.ipynb.*runbook \(§5\)", guide)


@pytest.mark.parametrize(
    "pattern, service, field, scale",
    [
        (r"coordinator runs .*? with (\d+) MB", "coordinator", "mem_limit", 1),
        (r"each worker has (\d+) MB", "worker", "mem_limit", 1),
        (r"cpus: (\d+)", "worker", "cpus", 1),
        (r"a (\d+) GiB minimum-free-disk gate", "worker", "-min-free-mib", 1024),
    ],
)
def test_compose_guide_resource_limits_match_configuration(
    services, pattern, service, field, scale
):
    match = re.search(pattern, prose("deploy/compose/README.md"))
    assert match, f"missing resource limit: {pattern}"
    config = services[service]
    if field.startswith("-"):
        actual = config["command"][config["command"].index(field) + 1]
    else:
        actual = str(config[field]).removesuffix("m")
    assert int(match[1]) * scale == int(actual)


def test_compose_guide_wal_path_matches_the_coordinator(services):
    match = re.search(r"-data-dir (\S+)", prose("deploy/compose/README.md"))
    assert match, "the guide must identify the persistent WAL path"
    command = services["coordinator"]["command"]
    assert match[1] == command[command.index("-data-dir") + 1]
    assert any(
        match[1].startswith(mount.split(":")[1] + "/")
        for mount in services["coordinator"]["volumes"]
    )


def test_compose_guide_profiles_match_configuration(services):
    guide = (ROOT / "deploy/compose/README.md").read_text()
    profiles = guide.split("Profiles, all off by default:", 1)[1].split("- [`soak.yaml`", 1)[0]
    documented = set(re.findall(r"`([^`]+)`\s+\(", profiles))
    configured = {
        profile for service in services.values() for profile in service.get("profiles", [])
    }
    assert documented == configured == {"tools", "s3", "monitoring"}
    # The tools container must not accidentally become a default cluster service.
    assert services["mr"]["profiles"] == ["tools"]


@pytest.mark.parametrize("name", ["coordinator", "worker", "mr"])
def test_compose_guide_shared_mounts_apply_to_cluster_and_tools(services, name):
    guide = prose("deploy/compose/README.md")
    assert "the workers, and the mr tools container share" in guide
    assert "test corpus read-only at /corpus" in guide
    assert "named output volume at /out" in guide
    assert set(services[name]["volumes"]) == {
        "../../data:/data",
        "../../testdata/corpus:/corpus:ro",
        "output:/out",
    }


def test_compose_guide_distinguishes_s3_and_monitoring_mounts(services):
    guide = prose("deploy/compose/README.md")
    assert "different paths (/data/corpus, /data/out)" in guide
    assert "monitoring services mount only their own configuration" in guide
    assert set(services["s3"]["volumes"]) == {
        "../../testdata/corpus:/data/corpus:ro",
        "s3-output:/data/out",
    }
    for name in ("prometheus", "grafana"):
        assert all(mount.startswith("./monitoring/") for mount in services[name]["volumes"])


def test_compose_guide_loopback_claim_covers_every_published_port(services):
    assert "All published ports are bound to 127.0.0.1 only" in prose("deploy/compose/README.md")
    ports = [port for service in services.values() for port in service.get("ports", [])]
    assert ports
    assert all(port.split(":")[0] == "127.0.0.1" for port in ports)


def test_compose_guide_keeps_soak_and_backups_overrides_separate(services):
    guide = prose("deploy/compose/README.md")
    soak = guide.split("soak.yaml):", 1)[1].split("- [", 1)[0]
    limit = re.search(r"lowers each worker to (\d+) MB", soak)
    assert limit
    override = yaml.safe_load((ROOT / "deploy/compose/soak.yaml").read_text())
    assert override["services"]["worker"]["mem_limit"] == f"{limit[1]}m"
    assert int(limit[1]) < int(services["worker"]["mem_limit"].removesuffix("m"))
    assert "Not used by make check or make e2e" in soak
    backups = guide.split("backups-off.yaml):", 1)[1]
    assert "experiment E2" in backups and "-backups=false" in backups
    override = yaml.safe_load((ROOT / "deploy/compose/backups-off.yaml").read_text())
    assert "-backups=false" in override["services"]["coordinator"]["command"]
    assert "-backups=false" not in services["coordinator"]["command"]


@pytest.mark.parametrize(
    "page",
    ["README.md", "design.md", "docs/design/0001-languages-and-shared-contract.md"],
)
def test_python_coordinator_documentation_points_to_existing_interop_coverage(page):
    text = prose(page)
    assert "coordinator" in text and "make py-e2e" in text
    makefile = (ROOT / "Makefile").read_text()
    target = makefile.split("\npy-e2e:\n", 1)[1].split("\n\n", 1)[0]
    assert "tests/test_coordinator_e2e.py" in target and "MR_REQUIRE_GO=1" in target
    assert (ROOT / "python/mapreduce_reloaded/coordinator/__main__.py").is_file()
    assert "no coordinator or worker" not in text
    assert "local mode only" not in text


@pytest.mark.parametrize("page", ["docs/roadmap.md", "docs/roadmap-explained.md"])
def test_roadmap_status_does_not_leave_the_completed_report_outstanding(page):
    # Only the opening status is current; later sections retain the original plan.
    opening = prose(page).split("### Phase 0", 1)[0].split("## Course deliverables", 1)[0]
    assert "final report (#60)" in opening
    assert not re.search(r"final report \(#60\) remain", opening)


@pytest.mark.parametrize(
    "page, heading",
    [
        ("docs/roadmap.md", "**Phase 5 — Python parity & interop**"),
        ("docs/roadmap-explained.md", "### Phase 5 — Python catches up"),
        ("docs/report.md", "## Future work"),
    ],
)
def test_python_parity_plan_leaves_the_worker_as_future_work(page, heading):
    plan = prose(page).split(heading, 1)[1].split("Phase 6", 1)[0]
    assert re.search(r"Python worker", plan)
    assert not re.search(r"(?:Finish the|Full|a) Python coordinator(?: and worker)?", plan)
    assert "#35–#39" not in plan, "#36 is complete and must not be listed as stretch"


def test_report_feature_total_matches_the_completed_paper_mapping():
    report = prose("docs/report.md")
    match = re.search(r"All (\d+) of the paper's features \(F1–F(\d+)\)", report)
    assert match, "the report must state both the feature count and range"
    mapping = (ROOT / "docs/paper-mapping.md").read_text()
    completed = {int(n) for n in re.findall(r"^\| F(\d+)\s*\|.*\| done \|$", mapping, re.M)}
    assert int(match[1]) == int(match[2]) == len(completed)
    assert completed == set(range(1, int(match[2]) + 1))


def test_current_evaluation_claim_includes_the_last_experiment():
    guide = prose("bench/README.md")
    experiments = re.findall(r"### E(\d+):", guide)
    assert experiments
    last = max(map(int, experiments))
    requirements = prose("design.md").split("## Key decisions", 1)[0]
    assert f"E0–E{last} run" in requirements
    abstract = prose("docs/report.md").split("## Abstract", 1)[1].split("##", 1)[0]
    assert f"E4–E{last}" in abstract


def test_ci_documentation_names_the_configured_circleci_checks():
    # The jobs live in the continuation config now; .circleci/config.yml is
    # only the setup config (ADR 0020).
    config = yaml.safe_load((ROOT / ".circleci/continue-config.yml").read_text())
    jobs = config["workflows"]["ci"]["jobs"]
    configured = {job if isinstance(job, str) else next(iter(job.values()))["name"] for job in jobs}
    ci = (ROOT / "docs/ci.md").read_text()
    table = ci.split("## The jobs", 1)[1].split("## Implicated-only CI", 1)[0]
    assert set(re.findall(r"^\| `([^`]+)` \|", table, re.M)) == configured
    adr = prose("docs/design/0014-ci-on-pull-requests.md")
    provider = adr.split("## CircleCI as the primary provider", 1)[1]
    named = re.search(r"jobs named (.*?)\. Each job", provider)
    assert named
    assert set(re.findall(r"\b(?:proto|go|python-\d+\.\d+|lint|mapping-coverage)\b", named[1])) == configured
    requirements = prose("design.md").split("## Key decisions", 1)[0]
    assert "CircleCI runs tests on PRs" in requirements
    assert "Actions run tests on PRs" not in requirements


def test_roadmap_vm_release_matches_the_vm_specification():
    vm = prose("docs/cluster-vm.md")
    release = re.search(r"Ubuntu \d+\.\d+\.\d+ LTS", vm)
    assert release
    host = prose("docs/roadmap.md").split("Cluster host:", 1)[1].split("##", 1)[0]
    assert release[0] in host


def test_development_codec_results_keep_their_machine_label():
    codecs = prose("bench/README.md").split("## Codecs", 1)[1].split("## Benchmarks", 1)[0]
    assert "Development-machine results — not the course VM" in codecs
    assert "do not compare development-machine numbers with course-VM numbers" in codecs
