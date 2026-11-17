"""The features notebook, examples/features.ipynb: its layout, that it stays
off the cluster and out of the demo's paths, that its committed outputs are
a clean run, and that its checks still hold when its cells run."""

import ast
import json
import re
import sys
import types
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]
NOTEBOOK = ROOT / "examples" / "features.ipynb"


def cells():
    """The notebook's cells, after checking its nbformat version. A source
    saved as a list of lines is joined, so a Jupyter or VS Code save keeps
    the tests working."""
    document = json.loads(NOTEBOOK.read_text())
    assert document["nbformat"] == 4
    for cell in document["cells"]:
        if isinstance(cell["source"], list):
            cell["source"] = "".join(cell["source"])
    return document["cells"]


def code():
    return [cell for cell in cells() if cell["cell_type"] == "code"]


def text(output):
    """An output's printed or rendered text, joined if saved as lines."""
    if output["output_type"] == "stream":
        value = output["text"]
    else:
        value = output.get("data", {}).get(
            "text/markdown", output.get("data", {}).get("text/plain", "")
        )
    return "".join(value) if isinstance(value, list) else value


def test_code_cells_compile():
    for cell in code():
        ast.parse(cell["source"])


def test_every_cell_has_a_unique_id():
    ids = [cell["id"] for cell in cells()]
    assert len(ids) == len(set(ids))


def test_every_section_has_a_rule_a_story_and_code():
    """Each section opens with a rule and its heading, then a story cell, then
    its code; the headings run 1, 2, 3, ... and the introduction lists them."""
    all_cells = cells()
    numbers = []
    for i, cell in enumerate(all_cells):
        if cell["id"].startswith("section-"):
            n = int(cell["id"].removeprefix("section-"))
            numbers.append(n)
            assert cell["source"].startswith(f"---\n\n## {n}. "), cell["id"]
            assert all_cells[i + 1]["id"] == f"story-{n}", f"section {n} has no story"
            assert all_cells[i + 2]["cell_type"] == "code", (
                f"section {n}'s story is not followed by code"
            )
    assert numbers == list(range(1, len(numbers) + 1))
    intro = all_cells[0]["source"]
    listed = [int(m) for m in re.findall(r"^\| (\d+) \|", intro, re.M)]
    assert listed == numbers


def test_it_never_touches_the_cluster_or_the_demos_paths():
    """It runs beside the demo: no Docker, no coordinator, nothing written
    under data/, and none of the notebook-* paths the demo's setup removes."""
    source = "\n".join(cell["source"] for cell in code())
    for word in ("docker", "submit", "18780", "notebook-"):
        assert word not in source, word
    assert not re.search(r"(?<![a-z])data/|/data\b", source), "a path under data/"
    assert "tempfile.mkdtemp" in source
    assert code()[-1]["source"].strip().startswith("shutil.rmtree(WORK")


def test_the_committed_outputs_are_a_clean_run():
    """The showcase is committed with a run's outputs, so it reads complete
    on GitHub: every code cell ran, in order, without an error, every claim
    held, and no output shows a path on the machine that ran it."""
    counts = [cell["execution_count"] for cell in code()]
    assert counts == list(range(1, len(counts) + 1)), "run it top to bottom with Restart & Run All"
    outputs = [output for cell in code() for output in cell["outputs"]]
    assert not [o for o in outputs if o["output_type"] == "error"]
    printed = "\n".join(text(o) for o in outputs)
    held = re.search(r"(\d+) of (\d+) claims held", printed)
    assert held and held[1] == held[2], "a claim did not hold, or the scorecard did not run"
    assert "skipped" not in printed, "commit a run in which every section ran"
    for prefix in ("/tmp", "/home/", "/root", "/Users/", "/private/", "C:\\"):
        assert prefix not in printed, prefix


def test_every_figure_is_numbered_and_captioned():
    numbers = []
    for cell in code():
        captions = [int(n) for n in re.findall(r"\bcaption\((\d+),", cell["source"])]
        assert len(captions) == cell["source"].count("plt.show()"), cell["id"]
        numbers += captions
    assert numbers == list(range(1, len(numbers) + 1))


@pytest.fixture
def no_jupyter(monkeypatch):
    """Runs the cells without Jupyter or matplotlib, as CI's Python jobs have
    neither: IPython.display is stubbed and collects what would be shown."""
    shown = []
    display = types.ModuleType("IPython.display")
    display.Markdown = lambda text: text
    display.display = shown.append
    monkeypatch.setitem(sys.modules, "IPython", types.ModuleType("IPython"))
    monkeypatch.setitem(sys.modules, "IPython.display", display)
    monkeypatch.setitem(sys.modules, "matplotlib", None)  # the import fails: no figures
    monkeypatch.setitem(sys.modules, "matplotlib.pyplot", None)
    monkeypatch.setenv("MR_ROOT", str(ROOT))
    return shown


def test_its_checks_hold_when_it_runs(no_jupyter, capsys):
    """Runs every cell but the Go one, which builds or needs bin/mr; go/cmd/mr's
    tests check the Go and Python examples against each other already."""
    namespace = {}
    for cell in code():
        if cell["id"] != "go":
            exec(compile(cell["source"], f"features.ipynb:{cell['id']}", "exec"), namespace)
    assert namespace["HAVE_PLOT"] is False
    assert not namespace["WORK"].exists(), "the last cell did not remove the temporary directory"
    held = [result for _, result in namespace["CHECKS"].values()]
    assert len(held) == 9 and all(result == "✓ held" for result in held), held
    assert "9 of 9 claims held" in capsys.readouterr().out
