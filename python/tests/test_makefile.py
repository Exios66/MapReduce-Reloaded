"""Exercise the PR's Makefile changes without Docker, network access or codegen."""

import os
import shlex
import stat
import subprocess
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]
FETCH = "git fetch --quiet origin main"
TREE = "git ls-tree -r --name-only origin/main -- proto"
BREAKING = "buf breaking --against .git#ref=origin/main"


@pytest.fixture
def make_project(tmp_path, monkeypatch):
    """Run the real Makefile in a disposable working directory with fake tools."""
    log = tmp_path / "calls"
    log.touch()
    monkeypatch.setenv("CALL_LOG", str(log))
    monkeypatch.setenv("PATH", f"{tmp_path / 'bin'}{os.pathsep}{os.environ['PATH']}")
    # Do not inherit a parent make's flags or project-specific overrides.
    for name in (
        "MAKEFLAGS",
        "GNUMAKEFLAGS",
        "MFLAGS",
        "MAKEFILES",
        "PROTO_FETCH",
        "BASE_FILES",
        "FETCH_EXIT",
        "TREE_EXIT",
        "BUF_EXIT",
    ):
        monkeypatch.delenv(name, raising=False)

    def tool(path, body):
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text("#!/bin/sh\nset -eu\n" + body)
        path.chmod(0o755)

    tool(
        tmp_path / "bin" / "git",
        """printf 'git %s\n' "$*" >> "$CALL_LOG"
case "$1" in
    fetch) exit "${FETCH_EXIT:-0}" ;;
    ls-tree)
        printf '%s\n' "${BASE_FILES-proto/mapreduce/v1/types.proto}"
        exit "${TREE_EXIT:-0}"
        ;;
    diff|ls-files) exit 0 ;;
    *) echo "unexpected git command: $*" >&2; exit 99 ;;
esac
""",
    )
    tool(
        tmp_path / ".tools" / "bin" / "buf",
        """printf 'buf %s\n' "$*" >> "$CALL_LOG"
exit "${BUF_EXIT:-0}"
""",
    )
    tool(
        tmp_path / "scripts" / "gen-proto.sh",
        """printf 'proto PYTHON=%s\n' "$PYTHON" >> "$CALL_LOG"
""",
    )
    tool(
        tmp_path / "bin" / "docker",
        """test -d data/input
test -d data/out
test -w data/out
test -x data/out
printf 'docker %s\n' "$*" >> "$CALL_LOG"
""",
    )

    def invoke(*args, check=True):
        return subprocess.run(
            ["make", "--no-print-directory", "-f", str(ROOT / "Makefile"), *args],
            cwd=tmp_path,
            capture_output=True,
            text=True,
            timeout=10,
            check=check,
        )

    return tmp_path, invoke, log


def test_data_creates_directories_and_worker_permissions(make_project):
    project, make, _ = make_project
    make("data")
    assert (project / "data/input").is_dir()
    output = project / "data/out"
    assert output.is_dir()
    # UID 65532 needs write and traversal permission, regardless of host ownership.
    assert stat.S_IMODE(output.stat().st_mode) & 0o777 == 0o777


def test_data_rerun_preserves_files_and_repairs_permissions(make_project):
    project, make, _ = make_project
    make("data")
    source = project / "data/input/input.txt"
    result = project / "data/out/part-r-00000"
    source.write_text("original input\n")
    source.chmod(0o640)
    result.write_text("original output\n")
    result.chmod(0o640)
    (project / "data/out").chmod(0o700)

    make("data")

    assert source.read_text() == "original input\n"
    assert result.read_text() == "original output\n"
    assert stat.S_IMODE(source.stat().st_mode) == 0o640
    assert stat.S_IMODE(result.stat().st_mode) == 0o640
    assert stat.S_IMODE((project / "data/out").stat().st_mode) & 0o777 == 0o777


def test_cluster_up_prepares_data_before_starting_compose(make_project):
    _, make, log = make_project
    make("cluster-up")
    assert log.read_text().splitlines() == [
        "docker compose -p mapreduce -f deploy/compose/compose.yaml up -d --build"
    ]


def test_cluster_up_does_not_start_when_data_preparation_fails(make_project):
    project, make, log = make_project
    (project / "data").write_text("a file blocks directory creation")
    result = make("cluster-up", check=False)
    assert result.returncode != 0
    assert log.read_text() == ""


@pytest.mark.parametrize("override", [None, "true", "false"])
def test_proto_breaking_fetch_policy(make_project, override):
    _, make, log = make_project
    args = [] if override is None else [f"PROTO_FETCH={override}"]
    make("proto-breaking", *args)
    expected = [] if override == "false" else [FETCH]
    assert log.read_text().splitlines() == [*expected, TREE, BREAKING]


def test_ci_proto_check_uses_prefetched_main(make_project):
    _, make, log = make_project
    # Execute the CircleCI job's actual make invocation, so changing the CI
    # override cannot silently reintroduce an authenticated fetch.
    commands = [
        shlex.split(line.strip().removeprefix("command: "))
        for line in (ROOT / ".circleci/continue-config.yml").read_text().splitlines()
        if line.strip().startswith("command: make proto-check")
    ]
    assert len(commands) == 1
    make(*commands[0][1:])
    assert log.read_text().splitlines() == [
        "buf lint",
        TREE,
        BREAKING,
        "proto PYTHON=python",
        "git diff --exit-code --stat -- go/gen python/mapreduce_reloaded/gen",
        "git ls-files --others --exclude-standard -- go/gen python/mapreduce_reloaded/gen",
    ]


@pytest.mark.parametrize("files", ["", "proto/README.md", "proto/types.proto.bak"])
def test_proto_breaking_skips_only_when_baseline_has_no_protos(make_project, monkeypatch, files):
    _, make, log = make_project
    monkeypatch.setenv("BASE_FILES", files)
    result = make("proto-breaking", "PROTO_FETCH=false")
    assert log.read_text().splitlines() == [TREE]
    assert "has no .proto files yet, skipping" in result.stdout


def test_proto_breaking_finds_proto_among_other_files(make_project, monkeypatch):
    _, make, log = make_project
    monkeypatch.setenv("BASE_FILES", "proto/README.md\nproto/mapreduce/v1/types.proto\n")
    make("proto-breaking", "PROTO_FETCH=false")
    assert log.read_text().splitlines() == [TREE, BREAKING]


@pytest.mark.parametrize(
    ("failure", "override", "expected"),
    [
        ("FETCH_EXIT", "true", [FETCH]),
        ("TREE_EXIT", "true", [FETCH, TREE]),
        ("TREE_EXIT", "false", [TREE]),
        ("BUF_EXIT", "true", [FETCH, TREE, BREAKING]),
        ("BUF_EXIT", "false", [TREE, BREAKING]),
    ],
)
def test_proto_breaking_propagates_failures(make_project, monkeypatch, failure, override, expected):
    _, make, log = make_project
    monkeypatch.setenv(failure, "1")
    result = make("proto-breaking", f"PROTO_FETCH={override}", check=False)
    assert result.returncode != 0
    assert log.read_text().splitlines() == expected
    assert "skipping" not in result.stdout


@pytest.fixture
def cleanup_project(make_project, monkeypatch):
    project, make, _ = make_project
    volume = project / "volume"
    volume.mkdir()
    monkeypatch.setenv("OUTPUT_VOLUME", str(volume))
    # Execute the container's actual shell/find command against a temporary
    # volume. Only Docker transport and disk-usage reporting are replaced.
    docker = project / "bin/docker"
    docker.write_text(
        f"#!{os.sys.executable}\n"
        "import os, subprocess, sys\n"
        "if sys.argv[1:3] == ['system', 'df']:\n"
        "    sys.exit(0)\n"
        "args = sys.argv[6:]\n"
        "assert args[:3] == ['sh', '-eu', '-c']\n"
        "args[3] = args[3].replace('/out', os.environ['OUTPUT_VOLUME'])\n"
        "sys.exit(subprocess.run(args).returncode)\n"
    )
    for name in ("done-1", "done-2", "active-job", "wal", "exp-input-cached"):
        directory = volume / name
        directory.mkdir()
        (directory / "part-r-00000").write_text(name)
    (volume / "sort-1g.txt").write_text("input")
    return volume, make


def test_clean_output_requires_explicit_selection(cleanup_project):
    volume, make = cleanup_project
    before = sorted(p.relative_to(volume) for p in volume.rglob("*"))
    result = make("clean-output", "CLEAN_OUTPUTS=", check=False)
    assert result.returncode != 0
    assert "select completed output directories" in result.stderr
    assert sorted(p.relative_to(volume) for p in volume.rglob("*")) == before


def test_clean_output_removes_only_selected_directories(cleanup_project):
    volume, make = cleanup_project
    make("clean-output", "CLEAN_OUTPUTS=done-1 done-2")
    assert sorted(p.name for p in volume.iterdir()) == [
        "active-job",
        "exp-input-cached",
        "sort-1g.txt",
        "wal",
    ]
    assert (volume / "active-job/part-r-00000").read_text() == "active-job"
    assert (volume / "sort-1g.txt").read_text() == "input"


@pytest.mark.parametrize(
    "name", ["wal", "sort-1g.txt", "exp-input-cached", "../volume", "/out", "*", ".", ".."]
)
def test_clean_output_rejects_protected_or_unsafe_names_before_deleting(cleanup_project, name):
    volume, make = cleanup_project
    result = make("clean-output", f"CLEAN_OUTPUTS=done-1 {name}", check=False)
    assert result.returncode != 0
    assert (volume / "done-1/part-r-00000").read_text() == "done-1"


@pytest.mark.parametrize("absolute", [False, True])
def test_junit_reports_use_one_directory_and_separate_go_passes(make_project, absolute):
    project, make, log = make_project
    (project / "go").mkdir()
    (project / "python").mkdir()
    for name, script in {
        "gotestsum": """#!/bin/sh
set -eu
printf 'gotestsum %s\\n' "$*" >> "$CALL_LOG"
test "$1" = --junitfile
printf 'report\\n' > "$2"
""",
        "python": """#!/bin/sh
set -eu
printf 'python %s\\n' "$*" >> "$CALL_LOG"
for arg in "$@"; do
    case "$arg" in --junitxml=*) printf 'report\\n' > "${arg#--junitxml=}" ;; esac
done
""",
    }.items():
        tool = project / "bin" / name
        tool.write_text(script)
        tool.chmod(0o755)

    destination = project / "results"
    directory = str(destination) if absolute else "results"
    make("go-test", "py-test", "py-e2e", f"JUNIT_DIR={directory}", "PYTHON=python")

    assert {p.relative_to(destination).as_posix() for p in destination.rglob("*.xml")} == {
        "go/go-test-race.xml",
        "go/go-test-coverage.xml",
        "go/go-test-uncached.xml",
        "python/py-test.xml",
        "python/py-e2e.xml",
    }
    calls = log.read_text().splitlines()
    assert any("-race -shuffle=on ./..." in call for call in calls)
    assert any("-coverpkg=./... -coverprofile=cover.out ./..." in call for call in calls)
    assert any("-count=1 ./internal/papermap ./internal/statusweb" in call for call in calls)


def test_go_tests_without_junit_use_the_original_go_commands(make_project, monkeypatch):
    project, make, log = make_project
    (project / "go").mkdir()
    for name in ("go", "python"):
        tool = project / "bin" / name
        tool.write_text(f'#!/bin/sh\nprintf "{name} %s\\n" "$*" >> "$CALL_LOG"\n')
        tool.chmod(0o755)
    monkeypatch.delenv("JUNIT_DIR", raising=False)

    make("go-test", "PYTHON=python")

    calls = log.read_text().splitlines()
    assert [call for call in calls if call.startswith("go ")] == [
        "go test -race -shuffle=on ./...",
        "go test -shuffle=on -coverpkg=./... -coverprofile=cover.out ./...",
        "go test -count=1 ./internal/papermap ./internal/statusweb",
    ]
