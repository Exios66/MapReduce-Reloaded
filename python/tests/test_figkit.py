"""The drawing-figures skill's figkit: the layout check finds colliding,
covering and clipped text, and finish refuses to save a figure with any."""

import importlib.util
import sys
from pathlib import Path

import pytest

SKILL = Path(__file__).resolve().parents[2] / ".claude" / "skills" / "drawing-figures"
spec = importlib.util.spec_from_file_location("figkit", SKILL / "figkit.py")
figkit = importlib.util.module_from_spec(spec)
spec.loader.exec_module(figkit)


def test_overlapping_texts_are_reported():
    texts = [("Alice 221", (10, 10, 60, 20)), ("Gregor 190", (40, 12, 90, 22))]
    assert figkit.problems(texts, [], 100, 100) == ["'Alice 221' overlaps 'Gregor 190'"]


def test_boxes_that_only_touch_are_not():
    # Two tick labels at a shared corner: boxes overlap by under the pad.
    texts = [("0", (0, 10, 10, 20)), ("0.0", (9, 0, 30, 11))]
    assert figkit.problems(texts, [], 100, 100, pad=2) == []
    assert figkit.problems(texts, [], 100, 100, pad=0) == ["'0' overlaps '0.0'"]


def test_text_over_a_marker_and_text_off_the_image_are_reported():
    texts = [("label", (10, 10, 50, 20)), ("title", (80, 90, 130, 99))]
    marks = [("the marker at (15, 15) px", (12, 12, 18, 18))]
    assert figkit.problems(texts, marks, 100, 100) == [
        "'label' covers the marker at (15, 15) px",
        "'title' runs off the image",
    ]


def test_a_label_set_inside_its_mark_is_not_reported():
    marks = [("the marker at (20, 20) px", (10, 10, 30, 30))]
    assert figkit.problems([("=", (15, 15, 25, 25))], marks, 100, 100) == []


def test_theme_suffixes_name_the_files_side_by_side():
    assert figkit.suffix("light") == "" and figkit.suffix("dark") == "-dark"


def test_finish_refuses_colliding_labels_and_saves_clear_ones(tmp_path, monkeypatch):
    pytest.importorskip("matplotlib")
    import matplotlib

    matplotlib.use("Agg")
    import matplotlib.pyplot as plt

    monkeypatch.setattr(sys, "argv", ["pytest"])
    kit = figkit.style()
    t = kit.THEMES["light"]

    def draw(alice, gregor, out):
        with plt.rc_context(kit.rc(t)):
            fig, ax = plt.subplots(figsize=(9, 4))
            ax.loglog(range(1, 1001), [3444 / r for r in range(1, 1001)], color=t["series"][0])
            labels = (("Alice", 41, 221, alice), ("Gregor", 45, 190, gregor))
            for word, rank, n, (offset, ha) in labels:
                kit.dots(ax, t, [rank], [n], t["series"][0])
                ax.annotate(
                    f"{word} {n}", (rank, n), xytext=offset, textcoords="offset points", ha=ha
                )
            kit.heading(fig, t, "A few words do most of the work", "Rank and count (log scales)")
            figkit.finish(kit, fig, out)

    with pytest.raises(AssertionError, match="'Alice 221' overlaps 'Gregor 190'"):
        draw(((10, 6), "left"), ((10, 6), "left"), tmp_path / "collide.png")
    assert not (tmp_path / "collide.png").exists()
    draw(((-10, -10), "right"), ((10, 10), "left"), tmp_path / "apart.png")
    assert (tmp_path / "apart.png").stat().st_size > 0
    assert sys.argv == ["pytest"], "style() restores sys.argv"


def test_scatter_points_and_legend_titles_are_checked(tmp_path, monkeypatch):
    """A label over a scatter point, and a legend title, are seen, not only
    lines' markers and legend entries."""
    pytest.importorskip("matplotlib")
    import matplotlib

    matplotlib.use("Agg")
    import matplotlib.pyplot as plt

    fig, ax = plt.subplots(figsize=(4, 3))
    ax.scatter([5], [5], s=400)
    ax.set_xlim(0, 10)
    ax.set_ylim(0, 10)
    ax.annotate("over the dot", (5, 5), ha="center", va="center")
    ax.legend(*ax.get_legend_handles_labels(), title="A legend title")
    found = figkit.check(fig)
    plt.close(fig)
    assert any(p.startswith("'over the dot' covers the marker") for p in found), found
    fig, ax = plt.subplots(figsize=(4, 3))
    ax.plot([0, 1], [0, 1], label="line")
    legend = ax.legend(title="A legend title")
    fig.canvas.draw()
    assert legend.get_title() in figkit._texts(fig)
    plt.close(fig)


def test_style_keeps_the_callers_backend_and_figures(monkeypatch):
    """A notebook that already draws keeps its backend and open figures."""
    pytest.importorskip("matplotlib")
    import matplotlib
    import matplotlib.pyplot as plt

    monkeypatch.setattr(sys, "argv", ["pytest"])
    before = matplotlib.get_backend()
    fig = plt.figure()
    try:
        figkit.style()
        assert matplotlib.get_backend() == before
        assert fig.number in plt.get_fignums()
    finally:
        plt.close(fig)
