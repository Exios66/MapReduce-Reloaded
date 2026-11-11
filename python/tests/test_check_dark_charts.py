"""Tests for scripts/check-dark-charts.py, the dark-variant chart check (#349)."""

import importlib.util
from pathlib import Path

import pytest

SCRIPT = Path(__file__).resolve().parents[2] / "scripts/check-dark-charts.py"
_spec = importlib.util.spec_from_file_location("check_dark_charts", SCRIPT)
charts = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(charts)

PICTURE = (
    "<picture>\n"
    '  <source media="(prefers-color-scheme: dark)" srcset="{dark}">\n'
    '  <img src="{light}" alt="E2">\n'
    "</picture>\n"
)


@pytest.fixture
def write(tmp_path):
    """Writes a file under a scratch root and returns its path."""

    def write(name, text=""):
        p = tmp_path / name
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text(text)
        return p

    return write


def test_a_picture_whose_dark_variant_exists_passes(write):
    write("bench/evaluation/E2-dark.png")
    page = write(
        "README.md",
        PICTURE.format(dark="bench/evaluation/E2-dark.png", light="bench/evaluation/E2.png"),
    )
    assert charts.check(page) == ([], 1)


def test_a_missing_dark_variant_is_reported(write):
    page = write(
        "README.md",
        PICTURE.format(dark="bench/evaluation/E2-dark.png", light="bench/evaluation/E2.png"),
    )
    problems, checked = charts.check(page)
    assert checked == 1
    assert problems == [f"{page}: dark variant bench/evaluation/E2-dark.png does not exist"]


def test_a_bare_chart_outside_a_picture_is_reported(write):
    page = write(
        "docs/report.md",
        '<img src="../bench/evaluation/E1.png" alt="E1">\n\n![E3](evaluation/E3.png)\n',
    )
    problems, _ = charts.check(page)
    assert problems == [
        f"{page}: shows ../bench/evaluation/E1.png without a dark variant",
        f"{page}: shows evaluation/E3.png without a dark variant",
    ]


def test_images_that_are_not_bench_charts_are_left_alone(write):
    page = write("docs/demo.md", "![status](assets/status.png)\n")
    assert charts.check(page) == ([], 0)


def test_main_exits_1_on_a_problem_and_0_when_clean(write, capsys):
    write("bench/evaluation/E2-dark.png")
    good = write("a.md", PICTURE.format(dark="bench/evaluation/E2-dark.png", light="x.png"))
    bad = write("b.md", "![E1](evaluation/E1.png)\n")
    assert charts.main([str(good)]) == 0
    assert charts.main([str(good), str(bad)]) == 1
    assert "evaluation/E1.png" in capsys.readouterr().out


def test_a_scan_of_tracked_pages_that_names_no_dark_variant_fails(write, monkeypatch):
    page = write("a.md", "# nothing here\n")
    monkeypatch.setattr(charts, "tracked", lambda: [page])
    assert charts.main([]) == 1
