"""The demo notebook's code cells are valid Python and keep the pipeline."""

import ast
import json
import re
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
        '"docker", "kill", "mapreduce-coordinator-1"',
        '"docker", "start", "mapreduce-coordinator-1"',
        "digest(RESTARTED) == digest(SORT)",
        # Each recovery must happen mid-job, and the worker's loss must be logged.
        "assert job.poll() is None",
        '"the job ended before the restart"',
        '"tasks_reset="',
    ):
        assert step in source, step
    # A clean stop tells idle workers to exit, so the coordinator is crashed instead.
    assert '"docker", "restart"' not in source


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
    """Section 14 displays committed figures, in both themes, each under
    its finding."""
    source = next(cell["source"] for cell in cells() if "display(Image(" in cell["source"])
    figures = re.findall(r'^    "([\w-]+)": "', source, re.MULTILINE)
    # E5 (#346) and E7 (#343) are results too, not only the README's.
    assert {"faults", "E0", "E1", "E5", "E7"} <= set(figures)
    for name in figures:
        for theme in ("", "-dark"):
            assert (NOTEBOOK.parents[1] / "bench/evaluation" / f"{name}{theme}.png").is_file()


def test_setup_clears_what_an_interrupted_run_left():
    """A run stopped by a failing cell skips the last section's cleanup, so
    the setup cell removes earlier runs' outputs and corpus before this run."""
    code = [cell["source"] for cell in cells() if cell["cell_type"] == "code"]
    setup = next(source for source in code if "RUN = " in source)
    for pattern in ('glob("notebook-*")', 'glob("notebook-corpus-*")', "shutil.rmtree(old"):
        assert pattern in setup, pattern
    # Every output this run writes is named notebook-*, so the next run's setup finds it.
    for name in ("WC", "JOBFILE", "NOCOMB", "URLS", "SORT", "RESTARTED", "LOCAL"):
        assignment = next(
            line for s in code for line in s.splitlines() if line.startswith(f"{name} = ")
        )
        assert "data/out/notebook-" in assignment, assignment


def test_every_cell_has_a_unique_id():
    """nbformat 4.5 requires an id on every cell; without one, nbformat warns
    now and will refuse the notebook later."""
    document = json.loads(NOTEBOOK.read_text())
    assert (document["nbformat"], document["nbformat_minor"]) >= (4, 5)
    ids = [cell.get("id") for cell in document["cells"]]
    assert all(ids) and len(set(ids)) == len(ids), ids
    assert all(re.fullmatch(r"[A-Za-z0-9_-]{1,64}", cell_id) for cell_id in ids)


def test_the_troubleshooting_section_names_real_targets_and_messages():
    """Each make target section 15 tells the presenter to run exists, and
    each message it quotes is one a code cell raises."""
    markdown = next(
        c["source"] for c in cells() if c["source"].startswith("## 15. If a cell fails")
    )
    makefile = (NOTEBOOK.parents[1] / "Makefile").read_text()
    for target in set(re.findall(r"`make ([\w-]+)", markdown)):
        assert re.search(rf"^{re.escape(target)}:", makefile, re.MULTILINE), target
    code = "\n".join(c["source"] for c in cells() if c["cell_type"] == "code")
    rows = [line.split("|")[1] for line in markdown.splitlines() if line.startswith("| `")]
    for _, quoted in re.findall(r"(`+)(.+?)\1", "".join(rows)):
        assert quoted in code, quoted
