"""Keep the README's copyable commands and recorded media usable."""

import json
import re
import struct
import subprocess
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]
README = (ROOT / "README.md").read_text()


@pytest.mark.parametrize("block", re.findall(r"```sh\n(.*?)\n```", README, re.S))
def test_readme_shell_blocks_parse(block):
    """The advertised sh blocks must parse as POSIX shell, not only Bash."""
    result = subprocess.run(["sh", "-n"], input=block, text=True, capture_output=True)
    assert result.returncode == 0, result.stderr
    assert "$'" not in block, "ANSI-C quoting is not portable POSIX sh"


def test_readme_make_targets_exist():
    """Every named Make target must exist in the checkout."""
    targets = set(re.findall(r"^([a-z][\w-]*):", (ROOT / "Makefile").read_text(), re.M))
    referenced = set(re.findall(r"\bmake ([a-z][\w-]*)", README))
    assert referenced and referenced <= targets


def test_readme_linter_pin_matches_vm_setup():
    """Development setup must install the same linter as the VM and CI."""
    setup = (ROOT / "scripts/vm-setup.sh").read_text()
    version = re.search(r"^GOLANGCI_VERSION=(\S+)$", setup, re.M)[1]
    assert f"golangci-lint@{version}" in README


@pytest.mark.parametrize("name", ["setup-terminal", "status-vm"])
def test_readme_recordings_exist_in_both_themes(name):
    """Both themes must be real, nonempty GIFs with matching dimensions."""
    sizes = []
    for theme in ("dark", "light"):
        path = ROOT / "docs/assets" / f"{name}-{theme}.gif"
        data = path.read_bytes()
        assert data[:6] in (b"GIF87a", b"GIF89a")
        assert len(data) > 1000
        sizes.append(struct.unpack("<HH", data[6:10]))
        assert path.relative_to(ROOT).as_posix() in README
    assert sizes[0] == sizes[1]
    assert sizes[0][0] >= 900 and sizes[0][1] >= 600
    provenance = json.loads((ROOT / "docs/assets" / f"{name}-provenance.json").read_text())
    if name == "setup-terminal":
        assert provenance["exit_code"] == 0
        assert any(line.strip() == "identical" for _, line in provenance["recorded_output"])
        block = re.search(r"```sh\n(.*?)\n```", README.split("## Local mode\n")[1], re.S)[1]
        assert provenance["commands"] == block.splitlines()
    else:
        assert len(provenance["samples"]) >= 3
        assert all(len(pair) == 2 for pair in provenance["samples"])
        for pair in provenance["samples"]:
            for capture in pair:
                assert capture["finished_at"] >= capture["started_at"]
