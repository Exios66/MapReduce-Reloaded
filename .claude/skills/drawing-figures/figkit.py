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

    texts are (name, (x0, y0, x1, y1)); marks are the same, or carry a third
    item, the matplotlib Path in pixels a mark is clipped to. width and
    height are the image's. Reports two texts that overlap, a text over a
    mark (but not one set wholly inside it, like a value in its square, nor
    one whose overlap with the mark lies outside the mark's clip path), and a
    text not wholly inside the image. pad forgives touching edges."""
    found = []
    for i, (name, box) in enumerate(texts):
        if box[0] < -pad or box[1] < -pad or box[2] > width + pad or box[3] > height + pad:
            found.append(f"{name!r} runs off the image")
        for other, other_box in texts[i + 1 :]:
            if overlaps(box, other_box, pad):
                found.append(f"{name!r} overlaps {other!r}")
        for mark, mark_box, *shape in marks:
            if overlaps(box, mark_box, pad) and not inside(box, mark_box, pad):
                if not shape or shape[0] is None or _meets(shape[0], box, mark_box):
                    found.append(f"{name!r} covers {mark}")
    return found


def _meets(path, a, b):
    """Whether the overlap of boxes a and b meets the filled path."""
    from matplotlib.transforms import Bbox

    overlap = Bbox.from_extents(max(a[0], b[0]), max(a[1], b[1]), min(a[2], b[2]), min(a[3], b[3]))
    return path.intersects_bbox(overlap, filled=True)


def _texts(fig):
    """Every visible, nonempty text a reader sees: headings, titles, axis and
    tick labels, axis offset text (a multiplier such as 1e6), annotations,
    and legend entries and titles. A hidden axes or axis, such as the second
    x-axis of ax.twinx(), contributes nothing."""
    found = list(fig.texts)
    legends = [*fig.legends, *(ax.get_legend() for ax in fig.axes)]
    for legend in legends:
        if legend is not None:
            found += [legend.get_title(), *legend.get_texts()]
    for ax in fig.axes:
        if not ax.get_visible():
            continue
        found += [ax.title, getattr(ax, "_left_title", None), getattr(ax, "_right_title", None)]
        found += ax.texts
        for axis in (ax.xaxis, ax.yaxis):
            if not ax.axison or not axis.get_visible():
                continue
            found += [axis.label, axis.get_offset_text()]
            # Only the ticks in view are drawn; get_ticklabels also returns
            # the ones beyond the axis limits, which would read as off the image.
            for tick in axis._update_ticks():
                found += [tick.label1, tick.label2]
    return [t for t in found if t is not None and t.get_visible() and t.get_text().strip()]


def _clip(artist, box):
    """box as drawn after artist's clipping, and the clip path in pixels (a
    circle, a polar axes) or None: (box, path), or None if wholly clipped
    away. The box is cut to the clip box and to the path's extent; problems
    tests the path's shape."""
    from matplotlib.transforms import Bbox

    if not artist.get_clip_on():
        return box, None
    clips, path = [], None
    if artist.get_clip_box() is not None:
        clips.append(artist.get_clip_box())
    if artist.get_clip_path() is not None:
        path, affine = artist.get_clip_path().get_transformed_path_and_affine()
        path = affine.transform_path(path)
        if not path.intersects_bbox(Bbox.from_extents(*box), filled=True):
            return None
        clips.append(path.get_extents())
    for clip in clips:
        box = (
            max(box[0], clip.x0),
            max(box[1], clip.y0),
            min(box[2], clip.x1),
            min(box[3], clip.y1),
        )
        if box[0] >= box[2] or box[1] >= box[3]:
            return None
    return box, path


def _marks(fig):
    """The markers drawn in each axes, as small boxes: dots a label must not
    cover. They come from lines with markers (plot, kit.dots) and from scatter
    plots, whose points are a PathCollection rather than a line. Each is
    placed by its own artist's transform, and cut to its clip region, so a
    marker clipped away by the axes limits or a clip path covers nothing."""
    from matplotlib.collections import PathCollection

    px = fig.dpi / 72  # pixels per point
    found = []
    for ax in fig.axes:
        if not ax.get_visible():
            continue
        for line in ax.get_lines():
            if line.get_marker() in (None, "None", "", " ") or not line.get_visible():
                continue
            r = line.get_markersize() * px / 2
            for x, y in line.get_transform().transform(line.get_xydata()):
                found.append((line, x, y, r))
        for dots in ax.collections:
            if not isinstance(dots, PathCollection) or not dots.get_visible():
                continue
            offsets = dots.get_offset_transform().transform(dots.get_offsets())
            sizes = dots.get_sizes()  # marker areas in square points, one or one per point
            for i, (x, y) in enumerate(offsets):
                area = sizes[i % len(sizes)] if len(sizes) else 36  # matplotlib's default
                found.append((dots, x, y, area**0.5 * px / 2))
    marks = []
    for artist, x, y, r in found:
        drawn = _clip(artist, (x - r, y - r, x + r, y + r))
        if drawn is not None:
            marks.append((f"the marker at ({x:.0f}, {y:.0f}) px", *drawn))
    return marks


def _extent(text, renderer):
    """A text's box as drawn. An annotation's own extent includes its arrow;
    only its words are measured, so an arrow pointing at a marker is no
    collision."""
    from matplotlib.text import Text

    return tuple(Text.get_window_extent(text, renderer).extents)


def check(fig, slack=2.0):
    """Draws fig and returns its layout problems (see problems).

    A text's box includes room above and below its letters, so boxes that
    overlap by under slack points (two by default) are taken as touching:
    an axis's 0 and the other axis's 0.0 at a shared corner overlap by
    about one point and do not collide. A real collision is many points."""
    fig.canvas.draw()
    renderer = fig.canvas.get_renderer()
    texts = [(t.get_text(), _extent(t, renderer)) for t in _texts(fig)]
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
