"""The README's local-mode quick start: Go and Python give the same files."""

import shutil
import subprocess
import sys
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]
CORPUS = ROOT / "testdata" / "corpus"

pytestmark = pytest.mark.skipif(shutil.which("go") is None, reason="needs the Go toolchain")


def test_local_mode_go_and_python_agree(tmp_path):
    """The README's local-mode quick start: Go and Python write the same files."""
    inputs = str(CORPUS / "*.txt")
    go, py = tmp_path / "go", tmp_path / "py"
    subprocess.run(
        [
            shutil.which("go"),
            "run",
            "./cmd/mr",
            "run",
            "-local",
            "-m",
            "3",
            "-r",
            "4",
            "../examples/wordcount.go",
            str(go),
            inputs,
        ],
        cwd=ROOT / "go",
        check=True,
        capture_output=True,
    )
    subprocess.run(
        [
            sys.executable,
            "-m",
            "mapreduce_reloaded",
            "run",
            "--local",
            "-m",
            "3",
            "-r",
            "4",
            "wordcount",
            str(py),
            inputs,
        ],
        cwd=ROOT / "python",
        check=True,
        capture_output=True,
    )
    names = sorted(p.name for p in go.iterdir())
    assert names == sorted(p.name for p in py.iterdir())
    for name in names:
        assert (py / name).read_bytes() == (go / name).read_bytes(), name
