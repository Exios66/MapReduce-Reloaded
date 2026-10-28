"""Keep the hosted CI configuration consistent with the rest of the repository.

CircleCI itself is not run here (docs/ci.md): these tests catch the drift that
would otherwise show up only on a PR, such as two different linter pins, and
exercise scripts/ci-install-go.sh against a fake download server.
"""

import hashlib
import io
import json
import os
import platform
import re
import subprocess
import sys
import tarfile
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]
CIRCLE = (ROOT / ".circleci/config.yml").read_text()
ACTIONS = (ROOT / ".github/workflows/ci.yml").read_text()
INSTALL_GO = ROOT / "scripts/ci-install-go.sh"


def go_mod_version() -> str:
    """The Go release scripts/ci-install-go.sh wants: a toolchain line wins over go."""
    mod = (ROOT / "go/go.mod").read_text()
    toolchain = re.search(r"^toolchain go(\S+)$", mod, re.M)
    return (toolchain or re.search(r"^go (\S+)$", mod, re.M)).group(1)


def test_circleci_runs_the_four_required_checks():
    workflow = CIRCLE.split("\nworkflows:\n", 1)[1]
    names = re.findall(r"^\s+- (proto|go)$|^\s+name: (python-3\.\d+)$", workflow, re.M)
    assert sorted(a or b for a, b in names) == ["go", "proto", "python-3.11", "python-3.13"]


def test_circleci_calls_the_make_targets():
    for command in (
        "make proto-check PYTHON=python PROTO_FETCH=false",
        "make go-lint go-test PYTHON=python",
        "make py-lint py-test PYTHON=python",
        "make tools",
    ):
        assert command in CIRCLE
    # The compatibility check needs a fresh origin/main and must not skip.
    assert "git fetch --quiet origin +refs/heads/main:refs/remotes/origin/main" in CIRCLE


def test_circleci_stays_on_the_free_plan():
    assert "setup_remote_docker" not in CIRCLE
    assert "docker_layer_caching" not in CIRCLE
    assert set(re.findall(r"resource_class: (\S+)", CIRCLE)) <= {"small", "medium"}
    assert "machine:" not in CIRCLE


def test_golangci_lint_has_one_pin():
    vm = re.search(
        r"^GOLANGCI_VERSION=(\S+)$", (ROOT / "scripts/vm-setup.sh").read_text(), re.M
    ).group(1)
    assert re.findall(r"golangci-lint@(v[\d.]+)", ACTIONS) == [vm]
    # CircleCI reads the pin from the script rather than repeating it.
    assert "scripts/vm-setup.sh" in CIRCLE and vm not in CIRCLE
    assert vm in (ROOT / "docs/ci.md").read_text()


def test_actions_runs_only_by_hand():
    # CircleCI is the CI; the Actions workflow is kept for a manual run only.
    on = ACTIONS.split("\non:\n", 1)[1].split("\n\n", 1)[0]
    assert on.split() == ["workflow_dispatch:"]


def test_actions_go_job_can_run_the_python_golden_tests():
    go_job = ACTIONS.split("\n  go:\n", 1)[1].split("\n  python:\n", 1)[0]
    assert "python-version: " in go_job and 'pip install -e "python[dev]"' in go_job
    assert "make go-lint go-test PYTHON=python" in go_job


def test_install_go_reads_the_version_from_go_mod():
    out = subprocess.run([INSTALL_GO, "--version"], capture_output=True, text=True, check=True)
    assert out.stdout.strip() == go_mod_version()


# --- scripts/ci-install-go.sh against a fake download server ---------------------

pytestmark_linux = pytest.mark.skipif(
    sys.platform != "linux", reason="the script installs Linux archives"
)


@pytest.fixture
def fake_go_download(tmp_path):
    """A PATH whose curl serves a one-file Go archive and its release list."""
    version = go_mod_version()
    arch = {"x86_64": "amd64", "aarch64": "arm64", "arm64": "arm64"}[platform.machine()]
    filename = f"go{version}.linux-{arch}.tar.gz"

    def build_archive(reported: str) -> bytes:
        buf = io.BytesIO()
        with tarfile.open(fileobj=buf, mode="w:gz") as tar:
            body = f'#!/bin/sh\n[ "$1 $2" = "env GOVERSION" ] && echo go{reported}\n'.encode()
            info = tarfile.TarInfo("go/bin/go")
            info.size, info.mode = len(body), 0o755
            tar.addfile(info, io.BytesIO(body))
        return buf.getvalue()

    serve = tmp_path / "serve"
    serve.mkdir()
    calls = tmp_path / "curl-calls"
    calls.touch()
    bin_dir = tmp_path / "bin"
    bin_dir.mkdir()
    curl = bin_dir / "curl"
    curl.write_text(
        "#!/bin/sh\nout=; url=\n"
        "while [ $# -gt 0 ]; do\n"
        '  case "$1" in -o) out=$2; shift ;; https://*) url=$1 ;; esac; shift\n'
        "done\n"
        f'echo "$url" >> {calls}\n'
        'case "$url" in\n'
        f'  *mode=json*) cp {serve}/releases.json "$out" ;;\n'
        f'  *.tar.gz) cp {serve}/archive "$out" ;;\n'
        "  *) exit 22 ;;\nesac\n"
    )
    curl.chmod(0o755)

    def publish(reported=version, sha=None):
        archive = build_archive(reported)
        (serve / "archive").write_bytes(archive)
        sha = sha or hashlib.sha256(archive).hexdigest()
        release = {"version": f"go{version}", "files": [{"filename": filename, "sha256": sha}]}
        (serve / "releases.json").write_text(json.dumps([release]))

    def run(**env):
        full = {
            **os.environ,
            "PATH": f"{bin_dir}{os.pathsep}{os.environ['PATH']}",
            "GO_CACHE_DIR": str(tmp_path / "cache"),
            "HOME": str(tmp_path),
            **env,
        }
        return subprocess.run([INSTALL_GO], capture_output=True, text=True, env=full)

    publish()
    return run, publish, calls, tmp_path / "cache" / f"go{version}-{arch}"


@pytestmark_linux
def test_install_go_downloads_and_verifies(fake_go_download):
    run, _, calls, dest = fake_go_download
    result = run()
    assert result.returncode == 0, result.stderr
    assert result.stdout.strip() == str(dest / "bin")
    assert (dest / "bin/go").exists()
    assert len(calls.read_text().split()) == 2  # the archive and the release list


@pytestmark_linux
def test_install_go_reuses_a_matching_cache_without_downloading(fake_go_download):
    run, _, calls, dest = fake_go_download
    assert run().returncode == 0
    calls.write_text("")
    result = run()
    assert result.returncode == 0 and result.stdout.strip() == str(dest / "bin")
    assert calls.read_text() == ""


@pytestmark_linux
def test_install_go_replaces_a_cache_with_the_wrong_version(fake_go_download):
    run, _, calls, dest = fake_go_download
    assert run().returncode == 0
    (dest / "bin/go").write_text("#!/bin/sh\necho go1.0.0\n")
    calls.write_text("")
    result = run()
    assert result.returncode == 0, result.stderr
    assert len(calls.read_text().split()) == 2
    assert "go1.0.0" not in (dest / "bin/go").read_text()


@pytestmark_linux
def test_install_go_rejects_a_checksum_mismatch(fake_go_download):
    run, publish, _, dest = fake_go_download
    publish(sha="0" * 64)
    result = run()
    assert result.returncode != 0 and "checksum mismatch" in result.stderr
    assert not dest.exists()


@pytestmark_linux
def test_install_go_requires_https(fake_go_download):
    run, *_ = fake_go_download
    result = run(GO_DL_BASE="http://example.invalid/dl")
    assert result.returncode != 0 and "https" in result.stderr
