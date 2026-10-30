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
