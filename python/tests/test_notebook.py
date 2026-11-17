"""The demo notebook's code cells are valid Python and keep the pipeline."""

import ast
import json
import re
import shutil
from pathlib import Path
from tempfile import TemporaryDirectory

NOTEBOOK = Path(__file__).resolve().parents[2] / "examples" / "demo.ipynb"


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


RULE = "---\n\n"  # the horizontal rule each section opens with


def heading(cell):
    """A markdown cell's text after its opening rule, if it has one."""
    return cell["source"].removeprefix(RULE)


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


def test_section_two_opens_the_status_page():
    """Section 2 renders a link that opens the status page in the browser:
    the workers' live view, refreshed every few seconds. The bare URL is
    not enough of a check: the setup defines METRICS and STATUS on the same
    address, so the assertion matches the rendered link."""
    source = "\n".join(cell["source"] for cell in cells() if cell["cell_type"] == "code")
    assert "[Open the status page](http://127.0.0.1:18780/)" in source


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
        # The scorecard says the combiner saved bytes, so §8 asserts it did.
        "assert saved > 1",
    ):
        assert step in source, step
    # A clean stop tells idle workers to exit, so the coordinator is crashed instead.
    assert '"docker", "restart"' not in source


def test_sections_are_numbered_in_order_and_listed_in_the_introduction():
    """The headings run 1, 2, 3, ... and the introduction's table names them."""
    headings = [
        heading(cell).split("\n", 1)[0]
        for cell in cells()
        if cell["cell_type"] == "markdown" and heading(cell).startswith("## ")
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
    """Section 14 displays committed figures, in both themes, each captioned
    with its finding."""
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


def test_setup_stops_on_a_bare_copy_and_warns_on_an_unconfirmed_one():
    """§1 stops on a bare copy, which cannot run the demo, and only warns
    when it cannot confirm a clone of main current with origin/main: a zip
    download, another branch, no network or a clone behind still runs."""
    source = next(c["source"] for c in cells() if "def find_root()" in c["source"])
    for pattern in (
        "--is-inside-work-tree",
        "a bare copy cannot run the demo",
        "is not a git clone",
        "the demo runs from main",
        "could not reach origin",
        "HEAD..origin/main",
        "restart the kernel and run again",
    ):
        assert pattern in source, pattern
    tree = ast.parse(source)
    raised = [ast.unparse(n) for n in ast.walk(tree) if isinstance(n, (ast.Raise, ast.Assert))]
    for warned in (
        "is not a git clone",
        "the demo runs from main",
        "could not reach origin",
        "behind origin/main",
    ):
        assert not any(warned in text for text in raised), f"{warned!r} stops the run"


def test_setup_defines_run_before_the_checkout_checks_use_it():
    """The checkout checks call run(); the def must come first in execution
    order, or a fresh kernel raises NameError."""
    source = next(c["source"] for c in cells() if "def find_root()" in c["source"])
    tree = ast.parse(source)
    defined = min(
        n.lineno for n in ast.walk(tree) if isinstance(n, ast.FunctionDef) and n.name == "run"
    )
    used = min(
        n.lineno
        for n in ast.walk(tree)
        if isinstance(n, ast.Call)
        and any(
            isinstance(a, ast.Constant) and a.value == "--is-inside-work-tree" for a in ast.walk(n)
        )
    )
    assert defined < used, "run() is used before it is defined in the setup cell"


def test_cleanup_removes_only_what_earlier_sections_defined():
    """The last cell runs even when a section before it was skipped, and
    removes the path of the one section that did run."""
    code = next(c["source"] for c in cells() if "removed this run" in c["source"])
    with TemporaryDirectory() as tmp:
        root = Path(tmp)
        exec(code, {"shutil": shutil, "ROOT": root})  # no section ran: no NameError
        (root / "data/out/notebook-x").mkdir(parents=True)
        exec(code, {"shutil": shutil, "ROOT": root, "WC": "/data/out/notebook-x"})
        assert not (root / "data/out/notebook-x").exists()


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
    markdown = next(heading(c) for c in cells() if heading(c).startswith("## 15. If a cell fails"))
    makefile = (NOTEBOOK.parents[1] / "Makefile").read_text()
    for target in set(re.findall(r"`make ([\w-]+)", markdown)):
        assert re.search(rf"^{re.escape(target)}:", makefile, re.MULTILINE), target
    code = "\n".join(c["source"] for c in cells() if c["cell_type"] == "code")
    rows = [line.split("|")[1] for line in markdown.splitlines() if line.startswith("| `")]
    for _, quoted in re.findall(r"(`+)(.+?)\1", "".join(rows)):
        assert quoted in code, quoted


def test_setup_fetch_cannot_hang_the_demo():
    """§1 refreshes origin/main over the network; a hung connection must
    give up within a minute with a warning, not hang the first cell."""
    source = next(c["source"] for c in cells() if "def find_root()" in c["source"])
    assert '"fetch", "origin", "main", timeout=60)' in source
    assert "except (RuntimeError, subprocess.TimeoutExpired):" in source


def test_every_demo_section_tells_its_story():
    """Each section from setup to the evaluation has story text just before
    its code cell, saying what the cell does and what to point at; an opening
    and a closing frame them. The text reads as prose, not as labelled
    presenter's notes."""
    all_cells = cells()
    ids = [cell["id"] for cell in all_cells]
    story = [f"story-{number}" for number in range(1, 15)]
    for name in ["story-arc", *story, "story-close"]:
        index = ids.index(name)
        assert all_cells[index]["cell_type"] == "markdown", name
        assert "narration" not in all_cells[index]["source"].lower(), name
        assert not all_cells[index]["source"].startswith(">"), name
        if name in story:
            assert all_cells[index + 1]["cell_type"] == "code", name


def test_every_section_opens_with_a_rule():
    """Each numbered section starts after a horizontal rule, so it begins on
    a clear break when presented."""
    sections = [c for c in cells() if heading(c).startswith("## ")]
    assert sections
    for cell in sections:
        assert cell["source"].startswith(RULE + "## "), cell["source"][:40]


def test_every_figure_is_numbered_and_captioned():
    """§6 and §8 caption the figures they draw as 1 and 2, and §14 numbers
    the evaluation's figures from 3, each captioned with its finding."""
    code = {c["id"]: c["source"] for c in cells() if c["cell_type"] == "code"}
    assert "def caption(number, text):" in code["cell-2"]
    assert code["cell-12"].index("plt.show()") < code["cell-12"].index("caption(1, ")
    assert code["cell-16"].index("plt.show()") < code["cell-16"].index("caption(2, ")
    assert "enumerate(FIGURES.items(), start=3)" in code["cell-28"]
    assert "caption(number, finding)" in code["cell-28"]
    for source in code.values():
        if "plt.show()" in source:
            assert "caption(" in source.split("plt.show()")[-1]


def test_the_scorecard_lists_what_ran_and_what_was_skipped():
    """After the closing words, a scorecard lists every claim the run
    checked; a fault section switched off is recorded as skipped, so the
    scorecard never claims a check that did not run."""
    all_cells = cells()
    ids = [cell["id"] for cell in all_cells]
    assert ids.index("scorecard") == ids.index("story-close") + 1
    assert "CHECKS.items()" in all_cells[ids.index("scorecard")]["source"]
    code = {c["id"]: c["source"] for c in all_cells if c["cell_type"] == "code"}
    for cell_id, section in (
        ("cell-8", 4),
        ("cell-14", 7),
        ("cell-16", 8),
        ("cell-18", 9),
        ("cell-20", 10),
        ("cell-22", 11),
        ("cell-26", 13),
    ):
        assert re.search(rf"passed\(\s*{section},", code[cell_id]), cell_id
    assert "skipped(10, " in code["cell-20"] and '"KILL_WORKER is False")' in code["cell-20"]
    assert (
        "skipped(11, " in code["cell-22"] and '"RESTART_COORDINATOR is False")' in code["cell-22"]
    )


def test_running_a_section_again_replaces_its_scorecard_row():
    """A claim's text can hold measured values, such as the lost worker's id;
    running its section again, or with its fault switched back on, replaces
    the row, so the scorecard counts each claim once."""
    setup = next(c["source"] for c in cells() if c["id"] == "cell-2")
    helpers = [
        node
        for node in ast.parse(setup).body
        if isinstance(node, ast.FunctionDef) and node.name in ("check_key", "passed", "skipped")
    ]
    assert len(helpers) == 3
    namespace = {"CHECKS": {}}
    exec(compile(ast.Module(body=helpers, type_ignores=[]), "cell-2", "exec"), namespace)
    claim = "A worker killed mid-job: the coordinator lost r1-w4 and ran {} of its tasks again"
    off = "A worker killed mid-job: the job still succeeds"
    namespace["skipped"](10, off, "KILL_WORKER is False")
    namespace["passed"](10, claim.format(9))
    namespace["passed"](10, claim.format(11))
    namespace["passed"](13, "Local mode and the cluster wrote the same output, byte for byte")
    combiner = "The combiner cut the bytes shuffled, and the output did not change: {}× fewer"
    namespace["passed"](8, combiner.format("3.1"))
    namespace["passed"](8, combiner.format("3.2"))
    assert len(namespace["CHECKS"]) == 3
    assert namespace["CHECKS"][(8, combiner.split(":")[0])] == (combiner.format("3.2"), "✓ held")
    assert namespace["CHECKS"][(10, "A worker killed mid-job")] == (claim.format(11), "✓ held")
