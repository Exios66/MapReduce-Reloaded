"""The demo notebook's code cells are valid Python and keep the pipeline."""

import ast
import json
from pathlib import Path

NOTEBOOK = Path(__file__).resolve().parents[2] / "examples" / "demo.ipynb"


def cells():
    """The notebook's cells, after checking its nbformat version."""
    document = json.loads(NOTEBOOK.read_text())
    assert document["nbformat"] == 4
    return document["cells"]


def test_code_cells_compile():
    """Every code cell is valid Python."""
    code = [cell for cell in cells() if cell["cell_type"] == "code"]
    assert code, "the notebook has no code cells"
    for cell in code:
        ast.parse(cell["source"])


def test_the_notebook_runs_the_demo_pipeline():
    """Checks that the demo's steps appear in the notebook's code cells."""
    source = "\n".join(cell["source"] for cell in cells() if cell["cell_type"] == "code")
    for step in ('"upload"', '"submit"', '"cat"', "-local", "metrics_lines"):
        assert step in source, step


def test_the_notebook_shows_the_combiner_and_both_recoveries():
    """The combiner comparison, the worker kill and the coordinator restart
    are each run and checked, not only described."""
    source = "\n".join(cell["source"] for cell in cells() if cell["cell_type"] == "code")
    for step in (
        '"-combiner=false"',
        '"docker", "kill", "mapreduce-worker-4"',
        '"docker", "restart", "mapreduce-coordinator-1"',
        "digest(RESTARTED) == digest(SORT)",
    ):
        assert step in source, step


def test_sections_are_numbered_in_order_and_listed_in_the_introduction():
    """The headings run 1, 2, 3, ... and the introduction's table names them."""
    headings = [
        cell["source"].split("\n", 1)[0]
        for cell in cells()
        if cell["cell_type"] == "markdown" and cell["source"].startswith("## ")
    ]
    numbers = [int(h.split()[1].rstrip(".")) for h in headings]
    assert numbers == list(range(1, len(numbers) + 1))
    listed = set()
    for line in cells()[0]["source"].splitlines():
        first = line.split("|")[1].strip() if line.startswith("| ") else ""
        lo, _, hi = first.partition("–")
        if lo.isdigit():
            listed.update(range(int(lo), int(hi or lo) + 1))
    assert listed == set(numbers[1:-1]), "setup and clean up need no row; every other section does"


def test_the_notebook_is_committed_without_outputs():
    """Outputs hold a run's job ids and times; the committed copy has none."""
    for cell in cells():
        if cell["cell_type"] == "code":
            assert cell["outputs"] == [] and cell["execution_count"] is None


def test_the_evaluation_figures_it_shows_exist():
    """Section 14 displays committed figures, in both themes."""
    source = next(cell["source"] for cell in cells() if "display(Image(" in cell["source"])
    names = source.split("for name in (", 1)[1].split(")", 1)[0]
    figures = [name.strip(' "') for name in names.split(",") if name.strip()]
    assert figures
    for name in figures:
        for theme in ("", "-dark"):
            assert (NOTEBOOK.parents[1] / "bench/evaluation" / f"{name}{theme}.png").is_file()
