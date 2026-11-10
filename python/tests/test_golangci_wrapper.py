"""scripts/golangci-lint.sh picks a golangci-lint that can lint this module.

The wrapper runs in a copy of the repository layout with a fake `go` and
fake linters on PATH, so these tests need neither Go nor the network.
"""

import os
import shutil
import stat
import subprocess
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]
PIN = "v2.14.0"


def executable(path, text):
    path.write_text("#!/bin/sh\n" + text)
    path.chmod(path.stat().st_mode | stat.S_IXUSR)


def linter(path, version, built):
    """A fake golangci-lint that reports version and built, and echoes its run."""
    executable(
        path,
        'if [ "$1" = version ]; then\n'
        f'  echo "golangci-lint has version {version} built with {built} from x on y"\n'
        "else\n"
        f'  echo "ran {path} $*"\n'
        "fi\n",
    )


@pytest.fixture
def tree(tmp_path):
    """A repository copy: the wrapper, the pin, go/, and a PATH directory."""
    (tmp_path / "scripts").mkdir()
    shutil.copy(ROOT / "scripts/golangci-lint.sh", tmp_path / "scripts")
    (tmp_path / "scripts/vm-setup.sh").write_text(f"GOLANGCI_VERSION={PIN}\n")
    (tmp_path / "go").mkdir()
    path = tmp_path / "path"
    path.mkdir()
    # `go env GOVERSION` reports go1.26.0; `go install` logs and builds a good linter.
    executable(
        path / "go",
        'case "$1" in\n'
        "  env) echo go1.26.0 ;;\n"
        f'  install) echo "$GOTOOLCHAIN $*" >> "{tmp_path}/installs"\n'
        '    mkdir -p "$GOBIN"\n'
        f"    cat > \"$GOBIN/golangci-lint\" <<'EOF'\n"
        "#!/bin/sh\n"
        'if [ "$1" = version ]; then\n'
        '  echo "golangci-lint has version 2.14.0 built with go1.26.0 from x"\n'
        'else echo "ran local $*"; fi\n'
        "EOF\n"
        '    chmod +x "$GOBIN/golangci-lint" ;;\n'
        "esac\n",
    )
    return tmp_path


def wrapper(tree):
    env = {"PATH": f"{tree / 'path'}:/usr/bin:/bin", "HOME": str(tree)}
    result = subprocess.run(
        [str(tree / "scripts/golangci-lint.sh"), "run", "./..."],
        cwd=tree / "go",
        env=env,
        capture_output=True,
        text=True,
    )
    installs = tree / "installs"
    return result, installs.read_text() if installs.exists() else ""


@pytest.mark.skipif(os.name != "posix", reason="a POSIX shell script")
@pytest.mark.parametrize("built", ["go1.26.0", "go1.26.11", "go1.27.1"])
def test_uses_the_pinned_linter_on_path_built_by_this_go_or_newer(tree, built):
    linter(tree / "path/golangci-lint", "2.14.0", built)
    result, installs = wrapper(tree)
    assert result.returncode == 0, result.stderr
    assert result.stdout.strip() == f"ran {tree / 'path/golangci-lint'} run ./..."
    assert installs == ""


@pytest.mark.skipif(os.name != "posix", reason="a POSIX shell script")
@pytest.mark.parametrize(
    "version, built",
    [("2.14.0", "go1.25.1"), ("2.5.0", "go1.26.0"), (None, None)],
    ids=["built-by-older-go", "other-version", "none-on-path"],
)
def test_builds_the_pin_into_tools_when_path_has_no_usable_linter(tree, version, built):
    if version:
        linter(tree / "path/golangci-lint", version, built)
    result, installs = wrapper(tree)
    assert result.returncode == 0, result.stderr
    assert result.stdout.strip() == "ran local run ./..."
    assert installs.split() == [
        "go1.26.0+auto",
        "install",
        f"github.com/golangci/golangci-lint/v2/cmd/golangci-lint@{PIN}",
    ]
    # A second run reuses the build.
    (tree / "installs").unlink()
    result, installs = wrapper(tree)
    assert result.stdout.strip() == "ran local run ./..." and installs == ""


@pytest.mark.skipif(os.name != "posix", reason="a POSIX shell script")
def test_fails_when_the_build_does_not_report_the_pin(tree):
    go = tree / "path/go"
    go.write_text(go.read_text().replace("version 2.14.0", "version 2.13.0"))
    result, _ = wrapper(tree)
    assert result.returncode != 0
    assert "does not report v2.14.0" in result.stderr
