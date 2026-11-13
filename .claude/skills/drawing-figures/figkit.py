"""The evaluation figures' house style, for any new figure, and a layout check.

    import sys; sys.path.insert(0, ".claude/skills/drawing-figures")
    import figkit
    kit = figkit.style()                 # bench/plot-experiments.py, drawing nothing
    for name, t in kit.THEMES.items():   # light and dark
        with plt.rc_context(kit.rc(t)):
            fig, ax = plt.subplots(figsize=(10, 4.5))
            ...                          # marks in t["series"], text in t["ink"]/t["ink2"]
            kit.heading(fig, t, title, subtitle)
            figkit.finish(kit, fig, out / f"NAME{figkit.suffix(name)}.png")

finish lays the figure out as bench/plot-experiments.py does, then refuses to
save it if two labels overlap, a label covers a marker, or text runs off the
image.
"""

import contextlib
import importlib.util
import io
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
SCRIPT = ROOT / "bench" / "plot-experiments.py"

# The fourth categorical slot of the dataviz reference palette, in each theme.
# THEMES validates three series all-pairs; a fourth passes only for marks whose
# neighbors are adjacent (stacked bars, lines), never scatter or small multiples,
# where it fails beside orange. It sits under 3:1 contrast on the light surface,
# so it needs a direct label or a table.
FOURTH = {"light": "#eda100", "dark": "#c98500"}


def suffix(theme):
    """The file-name suffix of a theme: "" for light, "-dark" for dark."""
    return "" if theme == "light" else f"-{theme}"


def style():
    """Returns bench/plot-experiments.py as a module, with its fonts registered.

    It has THEMES, rc, heading, stack_heading, finish, dots and the type
    sizes (TITLE, SUBTITLE, PANEL, LABEL, TICK, NOTE). Importing the script
    runs its main, which redraws bench/results; this points main at an empty
    directory, so it draws and prints nothing. The script also selects the
    Agg backend; a caller that already chose one, such as a notebook drawing
    inline, keeps it. Call it once, from the thread that draws."""
    import matplotlib

    argv, use = sys.argv, matplotlib.use
    if "matplotlib.pyplot" in sys.modules:  # the caller has a backend: keep it
        matplotlib.use = lambda *args, **kwargs: None
    with tempfile.TemporaryDirectory() as empty, contextlib.redirect_stdout(io.StringIO()):
        sys.argv = [str(SCRIPT), empty, empty]
        try:
            spec = importlib.util.spec_from_file_location("plot_experiments", SCRIPT)
            module = importlib.util.module_from_spec(spec)
            spec.loader.exec_module(module)
        finally:
            sys.argv, matplotlib.use = argv, use
    return module


def overlaps(a, b, pad=0.0):
    """Whether boxes a and b, each (x0, y0, x1, y1), overlap by more than pad."""
    return min(a[2], b[2]) - max(a[0], b[0]) > pad and min(a[3], b[3]) - max(a[1], b[1]) > pad


def inside(a, b, pad=0.0):
    """Whether box a lies within box b, give or take pad: a label set in its mark."""
    return a[0] >= b[0] - pad and a[1] >= b[1] - pad and a[2] <= b[2] + pad and a[3] <= b[3] + pad


def problems(texts, marks, width, height, pad=1.0):
    """Layout problems among named boxes, in pixels, as sentences.

    texts and marks are lists of (name, (x0, y0, x1, y1)); width and height
    are the image's. Reports two texts that overlap, a text over a mark (but
    not one set wholly inside it, like a value in its square), and a text not
    wholly inside the image. pad forgives touching edges."""
    found = []
    for i, (name, box) in enumerate(texts):
        if box[0] < -pad or box[1] < -pad or box[2] > width + pad or box[3] > height + pad:
            found.append(f"{name!r} runs off the image")
        for other, other_box in texts[i + 1 :]:
            if overlaps(box, other_box, pad):
                found.append(f"{name!r} overlaps {other!r}")
        for mark, mark_box in marks:
            if overlaps(box, mark_box, pad) and not inside(box, mark_box, pad):
                found.append(f"{name!r} covers {mark}")
    return found


def _texts(fig):
    """Every visible, nonempty text a reader sees: headings, titles, axis and
    tick labels, annotations, and legend entries and titles."""
    found = list(fig.texts)
    legends = [*fig.legends, *(ax.get_legend() for ax in fig.axes)]
    for legend in legends:
        if legend is not None:
            found += [legend.get_title(), *legend.get_texts()]
    for ax in fig.axes:
        found += [ax.title, getattr(ax, "_left_title", None), getattr(ax, "_right_title", None)]
        found += [ax.xaxis.label, ax.yaxis.label, *ax.texts]
        for axis in (ax.xaxis, ax.yaxis):
            # Only the ticks in view are drawn; get_ticklabels also returns
            # the ones beyond the axis limits, which would read as off the image.
            for tick in axis._update_ticks():
                found += [tick.label1, tick.label2]
    return [t for t in found if t is not None and t.get_visible() and t.get_text().strip()]


def _marks(fig):
    """The markers drawn in each axes, as small boxes: dots a label must not
    cover. They come from lines with markers (plot, kit.dots) and from scatter
    plots, whose points are a PathCollection rather than a line."""
    from matplotlib.collections import PathCollection

    px = fig.dpi / 72  # pixels per point
    points = []  # (x, y) in pixels, and the marker's radius in pixels
    for ax in fig.axes:
        for line in ax.get_lines():
            if line.get_marker() in (None, "None", "", " ") or not line.get_visible():
                continue
            radius = line.get_markersize() * px / 2
            points += [(x, y, radius) for x, y in ax.transData.transform(line.get_xydata())]
        for dots in ax.collections:
            if not isinstance(dots, PathCollection) or not dots.get_visible():
                continue
            offsets = dots.get_offset_transform().transform(dots.get_offsets())
            sizes = dots.get_sizes()  # marker areas in square points, one or one per point
            for i, (x, y) in enumerate(offsets):
                area = sizes[i % len(sizes)] if len(sizes) else 36  # matplotlib's default
                points.append((x, y, area**0.5 * px / 2))
    return [
        (f"the marker at ({x:.0f}, {y:.0f}) px", (x - r, y - r, x + r, y + r)) for x, y, r in points
    ]


def check(fig, slack=2.0):
    """Draws fig and returns its layout problems (see problems).

    A text's box includes room above and below its letters, so boxes that
    overlap by under slack points (two by default) are taken as touching:
    an axis's 0 and the other axis's 0.0 at a shared corner overlap by
    about one point and do not collide. A real collision is many points."""
    fig.canvas.draw()
    renderer = fig.canvas.get_renderer()
    texts = [(t.get_text(), tuple(t.get_window_extent(renderer).extents)) for t in _texts(fig)]
    pad = slack * fig.dpi / 72
    return problems(texts, _marks(fig), fig.bbox.width, fig.bbox.height, pad)


def finish(kit, fig, out):
    """kit.finish with a check: stacks the heading, lays the panels out below
    it, and saves to out only if check finds nothing; else raises
    AssertionError listing what to move. Closes the figure either way."""
    import matplotlib.pyplot as plt

    try:
        top = 0.84
        stack = fig.__dict__.get("_heading")
        if stack:
            top = kit.stack_heading(fig, *stack)
        fig.tight_layout(rect=(0, 0, 1, top))
        found = check(fig)
        if found:
            raise AssertionError(f"{Path(out).name}:\n  " + "\n  ".join(found))
        fig.savefig(out)
    finally:
        plt.close(fig)
