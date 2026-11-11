"""scripts/install-tool.sh: keep a tool built from the pin, go install anything else.

`make tools` runs it for each code generator. A fake `go` stands in for the
real one: `go version -m BIN` prints BIN's recorded build info, and
`go install PIN` logs the pin, so the tests see what would be rebuilt.
"""

import os
import re
import subprocess
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "scripts/install-tool.sh"
PIN = "github.com/bufbuild/buf/cmd/buf@v1.74.0"


@pytest.fixture
def fake_go(tmp_path):
    gobin = tmp_path / "gobin"
    gobin.mkdir()
    log = tmp_path / "installs"
    log.touch()
    bin_dir = tmp_path / "bin"
    bin_dir.mkdir()
    go = bin_dir / "go"
    go.write_text(
        "#!/bin/sh\n"
        'if [ "$1 $2" = "version -m" ]; then cat "$3.info" 2>/dev/null || exit 1; exit 0; fi\n'
        f'if [ "$1" = install ]; then echo "$2 $GOBIN" >> {log}; exit 0; fi\n'
        "exit 2\n"
    )
    go.chmod(0o755)

    def binary(path_line, mod_line):
        """A cached binary whose `go version -m` shows these lines."""
        tool = gobin / "buf"
        tool.write_text("#!/bin/sh\n")
        tool.chmod(0o755)
        (gobin / "buf.info").write_text(
            f"{tool}: go1.26.11\n\tpath\t{path_line}\n\tmod\t{mod_line}\th1:abc=\n"
        )

    def run(pin=PIN, **env):
        full = {
            **os.environ,
            "PATH": f"{bin_dir}{os.pathsep}{os.environ['PATH']}",
            "GOBIN": str(gobin),
            **env,
        }
        return subprocess.run([SCRIPT, pin], capture_output=True, text=True, env=full)

    return run, binary, log, gobin


def test_installs_a_missing_tool(fake_go):
    run, _, log, gobin = fake_go
    result = run()
    assert result.returncode == 0, result.stderr
    assert log.read_text().split() == [PIN, str(gobin)]


def test_keeps_a_tool_built_from_the_pin(fake_go):
    run, binary, log, _ = fake_go
    binary("github.com/bufbuild/buf/cmd/buf", "github.com/bufbuild/buf\tv1.74.0")
    result = run()
    assert result.returncode == 0, result.stderr
    assert "already" in result.stdout
    assert log.read_text() == ""


@pytest.mark.parametrize(
    "path_line, mod_line",
    [
        # Another version of the module.
        ("github.com/bufbuild/buf/cmd/buf", "github.com/bufbuild/buf\tv1.73.0"),
        # Another package that happens to have the same name.
        ("example.com/other/buf", "example.com/other\tv1.74.0"),
        # A local build has no module version.
        ("github.com/bufbuild/buf/cmd/buf", "github.com/bufbuild/buf\t(devel)"),
    ],
)
def test_rebuilds_any_other_tool(fake_go, path_line, mod_line):
    run, binary, log, _ = fake_go
    binary(path_line, mod_line)
    result = run()
    assert result.returncode == 0, result.stderr
    assert log.read_text().split()[0] == PIN


def test_rebuilds_a_binary_go_cannot_read(fake_go):
    run, _, log, gobin = fake_go
    (gobin / "buf").write_text("not a Go binary")
    (gobin / "buf").chmod(0o755)
    assert run().returncode == 0
    assert log.read_text().split()[0] == PIN


@pytest.mark.parametrize("pin", ["github.com/bufbuild/buf/cmd/buf", "buf@", "@v1"])
def test_rejects_a_pin_without_a_version(fake_go, pin):
    run, _, log, _ = fake_go
    result = run(pin)
    assert result.returncode == 2 and "package@version" in result.stderr
    assert log.read_text() == ""


def test_make_tools_installs_every_pin_through_the_script():
    makefile = (ROOT / "Makefile").read_text()
    recipe = makefile.split("\ntools:\n", 1)[1].split("\n\n", 1)[0].splitlines()
    assert recipe and all(
        re.fullmatch(r"\tGOBIN=\$\(TOOLS\) scripts/install-tool\.sh \S+@v\S+", line)
        for line in recipe
    )
    assert "go install" not in "\n".join(recipe)
