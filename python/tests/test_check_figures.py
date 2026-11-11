"""Tests for scripts/check-figures.py's archive checks and layout; drawing needs matplotlib."""

import hashlib
import importlib.util
import io
import tarfile
from pathlib import Path

import pytest

SCRIPT = Path(__file__).resolve().parents[2] / "scripts/check-figures.py"
_spec = importlib.util.spec_from_file_location("check_figures", SCRIPT)
figures = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(figures)


def archive(path, member):
    """Writes a tar.gz at path holding one runs.csv at member and returns its SHA-256."""
    data = b"setting,valid\n"
    with tarfile.open(path, "w:gz") as t:
        info = tarfile.TarInfo(member)
        info.size = len(data)
        t.addfile(info, io.BytesIO(data))
    return hashlib.sha256(path.read_bytes()).hexdigest()


def test_unpacks_the_isolated_e0_in_place_of_the_superseded_one(tmp_path):
    a = tmp_path / "archives"
    a.mkdir()
    sums = [
        f"{archive(a / f'{name}.tar.gz', member)}  ./{name}.tar.gz"
        for name, member in [
            ("E0", "E0/runs.csv"),
            ("E0-isolated", "E0/runs.csv"),
            ("E1", "E1/runs.csv"),
        ]
    ]
    (a / "SHA256SUMS").write_text("\n".join(sums) + "\n")
    assert figures.verify(a) == []
    figures.unpack(a, tmp_path / "out")
    assert sorted(
        p.relative_to(tmp_path / "out").as_posix() for p in (tmp_path / "out").rglob("*.csv")
    ) == [
        "E0-isolated/runs.csv",
        "E1/runs.csv",
    ]


def test_reports_a_changed_or_missing_archive(tmp_path):
    archive(tmp_path / "E1.tar.gz", "E1/runs.csv")
    (tmp_path / "SHA256SUMS").write_text(f"{'0' * 64}  ./E1.tar.gz\n{'0' * 64}  ./E2.tar.gz\n")
    assert figures.verify(tmp_path) == [
        "E1.tar.gz: SHA-256 differs from SHA256SUMS",
        "E2.tar.gz: listed in SHA256SUMS but missing",
    ]


def test_a_figure_that_changes_only_in_color_differs(tmp_path):
    Image = pytest.importorskip("PIL.Image")
    Image.new("RGB", (4, 4), "white").save(tmp_path / "a.png")
    Image.new("RGB", (4, 4), "white").save(tmp_path / "b.png")
    Image.new("RGB", (4, 4), "black").save(tmp_path / "c.png")
    assert figures.differs(tmp_path / "a.png", tmp_path / "b.png") is None
    assert figures.differs(tmp_path / "a.png", tmp_path / "c.png") is not None
