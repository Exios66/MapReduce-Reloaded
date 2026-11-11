"""E1's plot data and table, with matplotlib replaced at the drawing boundary."""

import csv
import importlib.util
import json
import sys
from pathlib import Path
from types import ModuleType
from unittest.mock import MagicMock, Mock

import pytest

SCRIPT = Path(__file__).resolve().parents[2] / "bench" / "plot-experiments.py"


@pytest.fixture
def plotter(tmp_path, monkeypatch):
    """The plot script imported with matplotlib replaced by mocks, and that pyplot."""
    # The script runs main at import. Start with an empty results directory,
    # and use only the standard library to inspect its plotting arguments.
    matplotlib = ModuleType("matplotlib")
    matplotlib.use = Mock()
    pyplot = ModuleType("matplotlib.pyplot")
    pyplot.subplots = Mock()
    pyplot.close = Mock()
    pyplot.rc_context = MagicMock()
    matplotlib.pyplot = pyplot
    monkeypatch.setitem(sys.modules, "matplotlib", matplotlib)
    monkeypatch.setitem(sys.modules, "matplotlib.pyplot", pyplot)
    monkeypatch.setattr(sys, "argv", [str(SCRIPT), str(tmp_path / "results")])
    spec = importlib.util.spec_from_file_location("plot_experiments", SCRIPT)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module, pyplot


def light(module):
    """The light theme, the one most tests draw in."""
    return module.THEMES["light"]


def row(setting, seconds=1, shuffled=1_000_000, valid="1"):
    """One runs.csv row of word count on four workers, with a wall time E1 must not use."""
    return {
        "setting": setting,
        "program": "wordcount",
        "workers": "4",
        "seconds": str(seconds),
        # Deliberately different: E1 must use the coordinator's time.
        "wall_seconds": "999",
        "map_output_bytes": str(shuffled),
        "valid": valid,
        "input_bytes": str(2**30),
        "m": "32",
        "r": "4",
    }


def medians(axis, label):
    """The (x, y) of the median dots drawn with label on axis."""
    (call,) = [c for c in axis.plot.call_args_list if c.kwargs.get("label") == label]
    return list(call.args[0]), list(call.args[1])


def single_runs(axis, t, color):
    """Each hollow ring of color on axis, as (x, y) pairs."""
    return [
        (x, y)
        for c in axis.plot.call_args_list
        if c.kwargs.get("markerfacecolor") == t["surface"] and c.kwargs["markeredgecolor"] == color
        for x, y in zip(c.args[0], c.args[1], strict=True)
    ]


def notes(axis, *words):
    """The texts annotated on axis that contain one of words."""
    texts = [c.args[0] for c in axis.annotate.call_args_list]
    return [x for x in texts if any(w in x for w in words)]


def test_e1_dumbbells_use_valid_medians_runs_and_decimal_megabytes(plotter, tmp_path):
    """E1 joins each group's valid medians, rings each run, and labels the savings."""
    module, pyplot = plotter
    figure, shuffled, seconds = Mock(), Mock(), Mock()
    shuffled.get_legend_handles_labels.return_value = [], []
    pyplot.subplots.return_value = figure, (shuffled, seconds)
    rows = [
        row("s1.5-off", 9, 20_000_000),
        row("s0.5-on", 2, 2_000_000),
        row("s0.5-off", 6, 12_000_000),
        row("s0.5-on", 4, 4_000_000),
        row("s0.5-on", 10, 10_000_000),
        row("s0.5-off", 8, 14_000_000),
        row("s1.5-on", 3, 7_000_000),
        row("s0.5-on", "invalid", "invalid", valid="0"),
    ]
    out = tmp_path / "E1.png"
    t = light(module)

    module.e1(rows, out, t)

    # The first group is the top row, at y = 1.
    for axis, on, off, title in (
        (shuffled, [4, 7], [13, 20], "Bytes shuffled (MB, log scale)"),
        (seconds, [4, 3], [7, 9], "Job time (s)"),
    ):
        assert medians(axis, "combiner on") == (pytest.approx(on), [1, 0])
        assert medians(axis, "combiner off") == (pytest.approx(off), [1, 0])
        # A line joins each pair, from the on median to the off median.
        lines = [c.args for c in axis.hlines.call_args_list]
        assert lines == [(1, on[0], off[0]), (0, on[1], off[1])]
        ticks, labels = axis.set_yticks.call_args.args
        assert list(ticks) == [1, 0]
        assert labels == ["S = 0.5", "S = 1.5"]
        axis.set_title.assert_called_once_with(title)
    # Every valid run is a ring; the invalid one is not drawn.
    # Rings sit on a strip 0.22 below each row.
    ring = sorted((x, round(y, 2)) for x, y in single_runs(shuffled, t, t["series"][0]))
    assert ring == [(2, 0.78), (4, 0.78), (7, -0.22), (10, 0.78)]
    ring = sorted((x, round(y, 2)) for x, y in single_runs(seconds, t, t["series"][1]))
    assert ring == [(6, 0.78), (8, 0.78), (9, -0.22)]
    # Bytes are on a log axis, so a row's length is the factor saved.
    shuffled.set_xscale.assert_called_once_with("log")
    seconds.set_xscale.assert_not_called()
    assert seconds.set_xlim.call_args.args == (0, pytest.approx(10 * 1.25))
    # Each median is labeled, and each row with both medians is labeled with the saving.
    assert notes(shuffled, "fewer") == ["3× fewer", "3× fewer"]
    assert notes(seconds, "faster") == ["43% faster", "67% faster"]
    values = [c.args[0] for c in shuffled.annotate.call_args_list if c.args[0].isdigit()]
    assert values == ["4", "13", "7", "20"]
    figure.savefig.assert_called_once_with(out)
    pyplot.close.assert_called_once_with(figure)


@pytest.mark.parametrize("combiner", ["on", "off"])
@pytest.mark.parametrize("valid", ["0", "1"])
def test_e1_handles_missing_groups_invalid_runs_and_zero_measurements(
    plotter, tmp_path, combiner, valid
):
    """A missing group, an invalid run or a zero never becomes a dot or a saving."""
    module, pyplot = plotter
    shuffled, seconds = Mock(), Mock()
    shuffled.get_legend_handles_labels.return_value = [], []
    pyplot.subplots.return_value = Mock(), (shuffled, seconds)
    # One group is absent, and the other either has no valid run or a real
    # zero measurement. Neither may turn into a fabricated nonzero dot, a
    # saving label, or a division by zero; a log axis cannot place a zero.
    module.e1([row(f"s1.0-{combiner}", 0, 0, valid)], tmp_path / "E1.png", light(module))

    kept = valid == "1"
    for group in ("on", "off"):
        assert medians(shuffled, f"combiner {group}") == ([], [])
        expect = ([0], [0]) if kept and group == combiner else ([], [])
        assert medians(seconds, f"combiner {group}") == expect
    for axis in (shuffled, seconds):
        axis.hlines.assert_not_called()
        assert notes(axis, "fewer", "faster") == []
    assert shuffled.set_xlim.call_args.args == (0.1, 10)
    assert seconds.set_xlim.call_args.args == (0, pytest.approx(1.25))


def test_e1_sorts_exponents_numerically(plotter, tmp_path):
    """Zipf exponents run 0.5, 2.0, 10.0 top to bottom, not in string order."""
    module, pyplot = plotter
    axes = Mock(), Mock()
    axes[0].get_legend_handles_labels.return_value = [], []
    pyplot.subplots.return_value = Mock(), axes
    module.e1([row("s10.0-on"), row("s2.0-on"), row("s0.5-on")], tmp_path / "E1.png", light(module))
    for axis in axes:
        ticks, labels = axis.set_yticks.call_args.args
        assert list(ticks) == [2, 1, 0]
        assert labels == ["S = 0.5", "S = 2.0", "S = 10.0"]


def test_e6_puts_the_three_inputs_in_order_top_to_bottom(plotter, tmp_path):
    """E6 draws the three inputs in order with their savings."""
    module, pyplot = plotter
    figure, shuffled, seconds = Mock(), Mock(), Mock()
    shuffled.get_legend_handles_labels.return_value = [], []
    pyplot.subplots.return_value = figure, (shuffled, seconds)
    rows = [
        row("book-on", 1, 1_000_000),
        row("book-off", 2, 10_000_000),
        row("crawl-eng-on", 3, 3_000_000),
        row("crawl-eng-off", 4, 12_000_000),
        row("crawl-on", 5, 5_000_000),
        row("crawl-off", 6, 14_000_000),
    ]

    module.e6(rows, tmp_path / "E6.png", light(module))

    ticks, labels = shuffled.set_yticks.call_args.args
    assert list(ticks) == [2, 1, 0]
    assert labels == ["crawled\nEnglish", "crawled,\nevery language", "book"]
    # Settings are matched by name, so the rows' order does not matter.
    assert medians(shuffled, "combiner on") == (pytest.approx([3, 5, 1]), [2, 1, 0])
    assert medians(shuffled, "combiner off") == (pytest.approx([12, 14, 10]), [2, 1, 0])
    assert medians(seconds, "combiner on") == (pytest.approx([3, 5, 1]), [2, 1, 0])
    assert medians(seconds, "combiner off") == (pytest.approx([4, 6, 2]), [2, 1, 0])
    assert notes(shuffled, "fewer") == ["4× fewer", "3× fewer", "10× fewer"]
    assert notes(seconds, "faster") == ["25% faster", "17% faster", "50% faster"]


def test_e1_table_reports_coordinator_time_and_invalid_run_counts(plotter, capsys):
    """E1's table gives coordinator seconds and counts the invalid runs."""
    module, _ = plotter
    module.table(
        "E1",
        [
            row("s0.5-on", 2),
            row("s0.5-on", 4),
            row("s0.5-on", 10),
            row("s0.5-on", "invalid", valid="0"),
            row("s0.5-off", "invalid", valid="0"),
        ],
    )
    assert capsys.readouterr().out.splitlines()[-2:] == [
        "| s0.5-on | wordcount | 4 | 4.00 (2.00–10.00) | 3 of 4 |",
        "| s0.5-off | wordcount | 4 | — | 0 of 1 |",
    ]


def test_e5_table_reports_coordinator_time_as_its_plot_does(plotter, capsys):
    """E5's table uses coordinator seconds, as its plot does."""
    module, _ = plotter
    module.table("E5", [row("compiled", 38), row("interpreted", 403)])
    assert capsys.readouterr().out.splitlines()[-2:] == [
        "| compiled | wordcount | 4 | 38.00 (38.00–38.00) | 1 of 1 |",
        "| interpreted | wordcount | 4 | 403.00 (403.00–403.00) | 1 of 1 |",
    ]


def test_main_loads_e1_csv_and_selects_its_plot(plotter, tmp_path, monkeypatch, capsys):
    """main reads E1/runs.csv, draws E1 in both themes, and prints its table."""
    module, pyplot = plotter
    results, out = tmp_path / "results", tmp_path / "plots"
    experiment = results / "E1"
    experiment.mkdir(parents=True)
    rows = [row("s1.0-on", 2), row("s1.0-off", 4)]
    with (experiment / "runs.csv").open("w", newline="") as stream:
        writer = csv.DictWriter(stream, fieldnames=rows[0])
        writer.writeheader()
        writer.writerows(rows)
    figure, left = Mock(), Mock()
    left.get_legend_handles_labels.return_value = [], []
    pyplot.subplots.return_value = figure, (left, Mock())
    monkeypatch.setattr(sys, "argv", [str(SCRIPT), str(results), str(out)])

    module.main()

    assert out.is_dir()
    # One plot per theme, each drawn under that theme's style.
    assert pyplot.subplots.call_args_list == [((1, 2), {"figsize": (11, 4.4), "sharey": True})] * 2
    assert [c.args[0] for c in figure.savefig.call_args_list] == [
        out / "E1.png",
        out / "E1-dark.png",
    ]
    assert [c.args[0]["axes.facecolor"] for c in pyplot.rc_context.call_args_list] == [
        module.THEMES["light"]["surface"],
        module.THEMES["dark"]["surface"],
    ]
    table = capsys.readouterr().out
    assert "\nE1\n" in table
    assert "| s1.0-on | wordcount | 4 | 2.00 (2.00–2.00) | 1 of 1 |" in table
    assert "| s1.0-off | wordcount | 4 | 4.00 (4.00–4.00) | 1 of 1 |" in table


def test_e3_marks_worker_loss_on_the_sampling_clock(plotter, tmp_path):
    """E3 shades the sampling interval in which a worker disappeared."""
    module, pyplot = plotter
    left, right = Mock(), Mock()
    pyplot.subplots.return_value = Mock(), (left, right)
    data = tmp_path / "E3"
    data.mkdir()
    samples = data / "samples-kill-50-1.csv"
    with samples.open("w", newline="") as stream:
        writer = csv.writer(stream)
        writer.writerow(["seconds", "url", "metric", "value"])
        for seconds, workers in ((0, 4), (6.5, 4), (7, 3), (7.5, 3)):
            for worker in range(workers):
                writer.writerow([seconds, f"http://worker-{worker}", "metric", 0])
    run = row("kill-50") | {"run": "1", "event_at": "5.8", "wall_seconds": "23.5"}

    t = light(module)
    module.e3([run], tmp_path, tmp_path / "E3.png", t)

    right.axvspan.assert_called_once_with(
        6.5, 7.0, color=t["muted"], alpha=0.35, linewidth=0, zorder=1
    )
    right.axvline.assert_not_called()


def test_e3_draws_each_kill_point_as_a_stem_from_the_baseline_without_a_line(plotter, tmp_path):
    """Each kill point is a stem from the no-failure median, labeled with its cost."""
    module, pyplot = plotter
    left, right = Mock(), Mock()
    pyplot.subplots.return_value = Mock(), (left, right)
    rows = [row("normal") | {"wall_seconds": w} for w in ("10", "12", "11")]
    rows += [row("kill-25") | {"wall_seconds": w} for w in ("20", "22", "21")]
    rows += [row("kill-75") | {"wall_seconds": "40"}, row("kill-75", valid="0")]
    t = light(module)
    color = t["series"][1]

    module.e3(rows, tmp_path, tmp_path / "E3.png", t)

    left.vlines.assert_called_once_with([25, 75], 11, [21, 40], color=color, linewidth=2, zorder=2)
    assert sorted(single_runs(left, t, color)) == [(25, 20), (25, 21), (25, 22), (75, 40)]
    # The kill points are separate settings, so no line joins their medians.
    assert left.plot.call_args_list and all(c.args[2:] == ("o",) for c in left.plot.call_args_list)
    assert [c.args[0] for c in left.annotate.call_args_list][1:] == ["+10.0 s", "+29.0 s"]


@pytest.mark.parametrize("counts", [(4, 4), (3, 3)])
def test_failure_interval_requires_a_recorded_transition(plotter, tmp_path, counts):
    """Without a drop in the worker count there is no failure interval."""
    module, _ = plotter
    samples = tmp_path / "samples.csv"
    with samples.open("w", newline="") as stream:
        writer = csv.writer(stream)
        writer.writerow(["seconds", "url"])
        for seconds, workers in enumerate(counts):
            for worker in range(workers):
                writer.writerow([seconds, f"http://worker-{worker}"])
    assert module.failure_interval(samples, 4) is None


def test_e0_skips_a_program_with_a_zero_time(plotter, tmp_path):
    """A program with a zero job time has no speed-up and is left out."""
    module, pyplot = plotter
    ax = Mock()
    pyplot.subplots.return_value = Mock(), ax
    rows = [
        row("normal", seconds) | {"program": "grep", "workers": str(w)}
        for w, seconds in ((1, 2), (2, 0), (4, 1))
    ]
    module.e0(rows, tmp_path / "E0.png", light(module))
    # Only the ideal line is drawn; grep's 2-worker time of 0 has no speed-up.
    assert ax.plot.call_count == 1


@pytest.mark.parametrize("exp", ["e2", "e4"])
def test_timeline_with_no_valid_run_draws_empty_bars(plotter, tmp_path, exp):
    """E2 and E4 with no valid run draw no bars and keep an axis width."""
    module, pyplot = plotter
    left, right = Mock(), Mock()
    pyplot.subplots.return_value = Mock(), (left, right)
    rows = [
        row(setting, valid="0") | {"run": "1", "event_at": ""} for setting in ("normal", "restart")
    ]
    getattr(module, exp)(rows, tmp_path, tmp_path / "out.png", light(module))
    right.barh.assert_called_once()
    assert right.barh.call_args.args[1] == []
    right.set_xlim.assert_called_once_with(0, pytest.approx(1.3))


def test_e2_colors_backups_and_dashes_the_no_straggler_runs(plotter, tmp_path):
    """Color marks backups on or off; dashes and hatching mark the runs without a straggler."""
    module, pyplot = plotter
    left, right = Mock(), Mock()
    bars = [Mock() for _ in range(4)]
    right.barh.return_value = bars
    pyplot.subplots.return_value = Mock(), (left, right)
    settings = ("normal", "normal-no-backups", "straggler-backups", "straggler-no-backups")
    rows = [row(s) | {"run": "1", "event_at": "", "job": "job-1"} for s in settings]
    (tmp_path / "E2").mkdir()
    for setting in settings:
        tasks_file(tmp_path / "E2" / f"tasks-{setting}-1.csv", "job-1", [(0, 0), (1, 4)])
    t = light(module)
    module.e2(rows, tmp_path, tmp_path / "out.png", t)
    blue, orange = t["series"][:2]
    assert left.step.call_count == 4
    for call, color, style in zip(
        left.step.call_args_list,
        (blue, orange, blue, orange),
        ("--", "--", "-", "-"),
        strict=True,
    ):
        assert call.args == ([0, 1], [0, 4])
        assert call.kwargs["color"] == color
        assert call.kwargs["ls"] == style
    assert right.barh.call_args.kwargs["color"] == [blue, orange, blue, orange]
    hatched = [b for b in bars if b.set.called]
    assert hatched == [bars[0], bars[1]]
    assert all(b.set.call_args.kwargs["hatch"] == "///" for b in hatched)


def test_timeline_needs_styles_for_more_settings_than_colors(plotter, tmp_path):
    """More settings than palette slots need explicit styles."""
    module, _ = plotter
    settings = {f"s{i}": f"setting {i}" for i in range(4)}
    with pytest.raises(ValueError, match="pass styles"):
        module.timeline_and_bars([], tmp_path, "E2", settings, "seconds", light(module), "t")


def tasks_file(path, job, samples, total=4):
    """Writes a scrape-tasks.py file of map totals and completions for job, plus another job's."""
    with path.open("w", newline="") as stream:
        writer = csv.writer(stream)
        writer.writerow(["seconds", "job", "kind", "state", "value"])
        for seconds, done in samples:
            writer.writerow([seconds, job, "map", "total", total])
            writer.writerow([seconds, job, "map", "completed", done])
            writer.writerow([seconds, "job-other", "map", "completed", total])


def test_map_phase_ends_when_every_map_task_is_done(plotter, tmp_path):
    """The map phase ends at the first sample with every map task done, or never."""
    module, _ = plotter
    path = tmp_path / "tasks.csv"
    tasks_file(path, "job-1", [(1.0, 0), (1.5, 2), (3.0, 4), (3.5, 4)])
    assert module.map_phase(path, "job-1") == pytest.approx(2.0)
    tasks_file(path, "job-1", [(1.0, 0), (1.5, 3)])
    assert module.map_phase(path, "job-1") is None


def test_e5_compares_job_and_map_phase_times(plotter, tmp_path):
    """E5 draws job and map-phase medians of the valid runs with their multiples."""
    module, pyplot = plotter
    jobs, maps = Mock(), Mock()
    pyplot.subplots.return_value = Mock(), (jobs, maps)
    (tmp_path / "E5").mkdir()
    rows = []
    for n, (setting, seconds, phase) in enumerate(
        (
            ("compiled", 10, 8),
            ("naive", 100, 80),
            ("interpreted", 15, 12),
            ("interpreted", 99, 1),
        ),
        start=1,
    ):
        job = f"job-{n}"
        rows.append(row(setting, seconds, valid="0" if n == 4 else "1") | {"run": "1", "job": job})
        tasks_file(tmp_path / "E5" / f"tasks-{setting}-1.csv", job, [(0, 0), (phase, 4)])
    # Run 4 is invalid and overwrote nothing that counts: its file is for job-4.
    tasks_file(tmp_path / "E5" / "tasks-interpreted-1.csv", "job-3", [(0, 0), (12, 4)])

    module.e5(rows, tmp_path, tmp_path / "E5.png", light(module))

    for axis, heights in ((jobs, [10, 15, 100]), (maps, [8, 12, 80])):
        call = axis.bar.call_args
        assert call.args[0] == [0, 1, 2]
        assert call.args[1] == pytest.approx(heights)
        labels = [c.args[0] for c in axis.annotate.call_args_list]
        assert labels == [
            f"{heights[0]:.1f} s",
            f"{heights[1]:.1f} s (1.5×)",
            f"{heights[2]:.1f} s (10.0×)",
        ]


def test_e5_plots_older_results_without_the_naive_setting(plotter, tmp_path):
    """E5 still plots results that have no naive setting."""
    module, pyplot = plotter
    jobs, maps = Mock(), Mock()
    pyplot.subplots.return_value = Mock(), (jobs, maps)
    rows = [
        row("compiled", 10) | {"run": "1", "job": "job-1"},
        row("interpreted", 105) | {"run": "1", "job": "job-2"},
    ]

    module.e5(rows, tmp_path, tmp_path / "E5.png", light(module))

    assert jobs.bar.call_args.args[0] == [0, 1]
    assert jobs.set_xticks.call_args.args[1] == ["compiled", "job file\n(api.Words,\napi.Sum)"]
    assert [c.args[0] for c in jobs.annotate.call_args_list] == ["10.0 s", "105.0 s (10.5×)"]


@pytest.mark.parametrize("field", ["seconds", "map_output_bytes"])
@pytest.mark.parametrize("bad_value", [None, "not-a-number"])
def test_e1_rejects_missing_or_invalid_measurements(plotter, tmp_path, field, bad_value):
    """A missing or non-numeric measurement raises and saves nothing."""
    module, pyplot = plotter
    figure = Mock()
    pyplot.subplots.return_value = figure, (Mock(), Mock())
    measurement = row("s1.0-on")
    if bad_value is None:
        del measurement[field]
    else:
        measurement[field] = bad_value

    with pytest.raises(KeyError if bad_value is None else ValueError):
        module.e1([measurement], tmp_path / "E1.png", light(module))

    figure.savefig.assert_not_called()


def test_e1_propagates_save_errors(plotter, tmp_path):
    """An error saving the figure reaches the caller."""
    module, pyplot = plotter
    figure = Mock()
    figure.savefig.side_effect = OSError("disk full")
    left, right = Mock(), Mock()
    left.get_legend_handles_labels.return_value = ([], [])
    pyplot.subplots.return_value = figure, (left, right)

    with pytest.raises(OSError, match="disk full"):
        module.e1([row("s1.0-on")], tmp_path / "E1.png", light(module))


@pytest.mark.parametrize("contents", ["", "setting,seconds,valid\n"])
def test_main_skips_empty_e1_results(plotter, tmp_path, monkeypatch, capsys, contents):
    """An empty or header-only runs.csv draws and prints nothing."""
    module, pyplot = plotter
    results = tmp_path / "results"
    experiment = results / "E1"
    experiment.mkdir(parents=True)
    (experiment / "runs.csv").write_text(contents)
    monkeypatch.setattr(sys, "argv", [str(SCRIPT), str(results)])

    module.main()

    pyplot.subplots.assert_not_called()
    assert capsys.readouterr().out == ""


def test_e0_title_places_caption_on_a_second_line(plotter, tmp_path):
    """E0's subtitle explains the measure on one line and the setup on the next."""
    module, pyplot = plotter
    figure, axis = Mock(), Mock()
    axis.errorbar.return_value = [Mock()]
    pyplot.subplots.return_value = figure, axis

    module.e0([row("normal")], tmp_path / "E0.png", light(module))

    title, subtitle = (call.args[2] for call in figure.text.call_args_list)
    assert title == "E0: scaling over workers"
    first, second = subtitle.split("\n")
    assert first.startswith("Speed-up = job time on 1 worker")
    assert "M = 32, R = 4" in second
    figure.savefig.assert_called_once_with(tmp_path / "E0.png")


def test_e2_comparison_reads_each_setting_from_its_own_directory(plotter, tmp_path):
    """The mixed E2 chart reads no-straggler runs from E2-pairs and straggler runs from E2."""
    module, pyplot = plotter
    left, right = Mock(), Mock()
    right.barh.return_value = [Mock() for _ in range(4)]
    fig = Mock()
    pyplot.subplots.return_value = fig, (left, right)
    kinds = {"normal": "E2-pairs", "normal-no-backups": "E2-pairs"}
    pairs = [row(s) | {"run": "1", "event_at": "", "job": "job-1"} for s in kinds]
    main = [
        row(s) | {"run": "1", "event_at": "", "job": "job-1"}
        for s in ("normal", "straggler-backups", "straggler-no-backups")
    ]
    for exp in ("E2", "E2-pairs"):
        (tmp_path / exp).mkdir()
    for setting in kinds:
        tasks_file(tmp_path / "E2-pairs" / f"tasks-{setting}-1.csv", "job-1", [(0, 0), (1, 4)])
    for setting in ("straggler-backups", "straggler-no-backups"):
        tasks_file(tmp_path / "E2" / f"tasks-{setting}-1.csv", "job-1", [(0, 0), (1, 4)])
    module.e2_comparison(pairs, main, tmp_path, tmp_path / "out.png", light(module))
    # Four curves: the main run's own no-straggler row is not used.
    assert left.step.call_count == 4
    subtitle = [c.args[2] for c in fig.text.call_args_list if "paired runs" in c.args[2]]
    assert subtitle


def test_e2_comparison_refuses_datasets_of_different_jobs(plotter, tmp_path):
    """Datasets with different M are not drawn on one chart."""
    module, _ = plotter
    pairs = [row("normal") | {"run": "1", "event_at": "", "job": "job-1"}]
    main = [row("straggler-backups") | {"run": "1", "event_at": "", "job": "job-1", "m": "64"}]
    with pytest.raises(ValueError, match="different jobs"):
        module.e2_comparison(pairs, main, tmp_path, tmp_path / "out.png", light(module))


def test_e2_comparison_takes_each_setting_from_its_source_and_checks_valid_runs(plotter, tmp_path):
    """A setting from the wrong source, or an unplotted invalid run, does not count."""
    module, pyplot = plotter
    left, right = Mock(), Mock()
    right.barh.return_value = [Mock() for _ in range(4)]
    pyplot.subplots.return_value = Mock(), (left, right)
    base = {"run": "1", "event_at": "", "job": "job-1"}
    pairs = [row("normal") | base, row("straggler-backups", seconds=99) | base]
    # An invalid run of another job is never plotted, so it must not fail the check.
    main = [
        row("straggler-backups") | base,
        row("straggler-no-backups", valid="0") | base | {"m": "64"},
    ]
    module.e2_comparison(pairs, main, tmp_path, tmp_path / "out.png", light(module))
    times = right.barh.call_args.args[1]
    assert 99 not in times


def titles(figure):
    """The title and subtitle a figure was given, in that order."""
    return [call.args[2] for call in figure.text.call_args_list]


def test_span_collapses_ends_that_agree_after_formatting(plotter):
    """span gives one value when both ends format alike."""
    module, _ = plotter
    assert module.span([15.2, 78.4, 17.0], lambda x: f"{x:.0f}") == "15–78"
    assert module.span([5.04, 4.96], lambda x: f"{x:.0f}") == "5"


def test_e0_titles_the_best_speed_up_and_names_a_run_far_below_its_median(plotter, tmp_path):
    """E0's title names the best speed-up, and only the outlier run is labeled."""
    module, pyplot = plotter
    figure, axis = Mock(), Mock()
    pyplot.subplots.return_value = figure, axis
    times = {
        ("grep", 1): (2.0, 2.0, 2.0),
        ("grep", 4): (0.8, 0.9, 5.5),  # one cold run
        ("wordcount", 1): (40.0, 40.0, 40.0),
        ("wordcount", 4): (10.0, 10.0, 10.0),
    }
    rows = [
        row("normal", seconds) | {"program": program, "workers": str(w)}
        for (program, w), runs in times.items()
        for seconds in runs
    ]
    module.e0(rows, tmp_path / "E0.png", light(module))
    title, subtitle = titles(figure)
    best = module.PROGRAMS["wordcount"]
    assert title == f"E0: {best} runs 4.0× faster on 4 workers"
    first, second, third = subtitle.split("\n")
    assert first.startswith("Speed-up = job time on 1 worker")
    assert second == "Grep takes 2.0 s on one worker, so fixed per-job costs cap its speed-up."
    assert "bars span the range" not in third
    labels = [call.args[0] for call in axis.annotate.call_args_list]
    assert "one grep run: 5.5 s" in labels
    assert sum("run:" in label for label in labels) == 1, "only the outlier is named"
    # Every run is drawn as a hollow marker: two worker counts, three runs, two programs.
    rings = [c for c in axis.plot.call_args_list if c.kwargs.get("linestyle") == "none"]
    assert sorted(len(c.args[1]) for c in rings) == [3, 3, 3, 3]
    assert all(c.kwargs["markerfacecolor"] == light(module)["surface"] for c in rings)


def cache_rows(path, reads):
    """Writes cache.jsonl to path: a start and an end row per (setting, program,
    run), whose disk_read_sectors differ by that run's bytes read / 512."""
    lines = []
    for (setting, program, run), read in reads.items():
        for phase, sectors in (("start", 1000), ("end", 1000 + read // 512)):
            lines.append(
                json.dumps(
                    {
                        "phase": phase,
                        "setting": setting,
                        "program": program,
                        "run": run,
                        "disk_read_sectors": sectors,
                    }
                )
            )
    path.write_text("\n".join(lines) + "\n")


def test_disk_reads_pairs_start_and_end_and_skips_incomplete_runs(plotter, tmp_path):
    """Each run's reads are its end counter minus its start counter, in bytes."""
    module, _ = plotter
    (tmp_path / "E0").mkdir()
    path = tmp_path / "E0" / "cache.jsonl"
    cache_rows(path, {("w1", "grep", 1): 850 * 2**20, ("w4", "grep", 1): 0})
    with open(path, "a") as f:
        f.write(
            json.dumps(
                {
                    "phase": "start",
                    "setting": "w2",
                    "program": "grep",
                    "run": 1,
                    "disk_read_sectors": 5,
                }
            )
            + "\n"
        )
        f.write(
            json.dumps(
                {
                    "phase": "start",
                    "setting": "w2",
                    "program": "grep",
                    "run": 2,
                    "disk_read_sectors": None,
                }
            )
            + "\n"
        )
        f.write(
            json.dumps(
                {
                    "phase": "end",
                    "setting": "w2",
                    "program": "grep",
                    "run": 2,
                    "disk_read_sectors": 9,
                }
            )
            + "\n"
        )
    assert module.disk_reads(tmp_path, "E0") == {
        ("w1", "grep", "1"): 850 * 2**20,
        ("w4", "grep", "1"): 0,
    }
    assert module.disk_reads(tmp_path, "E9") == {}


def test_e0_crosses_runs_that_read_far_more_from_disk_and_labels_them_together(plotter, tmp_path):
    """A run that read a quarter of the input more than its group's median is a cross.

    The two slow one-worker grep runs share one label; the other runs stay rings.
    """
    module, pyplot = plotter
    figure, axis = Mock(), Mock()
    pyplot.subplots.return_value = figure, axis
    times = {1: (2.0, 8.5, 2.1, 8.6, 2.0), 4: (1.4, 1.3, 1.4, 1.3, 0.9)}
    rows = [
        row(f"w{w}", seconds) | {"program": "grep", "workers": str(w), "run": str(n)}
        for w, runs in times.items()
        for n, seconds in enumerate(runs, 1)
    ]
    reads = {(r["setting"], "grep", r["run"]): 0 for r in rows}
    reads[("w1", "grep", "2")] = reads[("w1", "grep", "4")] = 850 * 2**20
    reads[("w4", "grep", "5")] = 2**28 - 1  # just under a quarter of the input: a ring
    module.e0(rows, tmp_path / "E0.png", light(module), reads)
    t = light(module)
    color = t["series"][0]
    crosses = [c for c in axis.plot.call_args_list if c.args[2:] == ("x",)]
    assert [(list(c.args[0]), list(c.args[1])) for c in crosses] == [
        ([1.07, 1.07], [2.1 / 8.5, 2.1 / 8.6])  # just right of the median
    ]
    assert len(single_runs(axis, t, color)) == 8
    assert notes(axis, "run") == ["2 grep runs: 8.5–8.6 s"]
    _, subtitle = titles(figure)
    # The chart states what was measured, not a cause: sort's heavy reads
    # come from the worker memory limit, not a cold input (#347).
    assert "Crosses: runs that read ¼ of the input more from disk" in subtitle
    assert "cold" not in subtitle


def test_e2_title_states_the_straggler_times_and_falls_back(plotter):
    """E2's title gives the straggler's times, or a plain title without both medians."""
    module, _ = plotter
    rows = [row("straggler-backups", 11.2), row("straggler-no-backups", 17.6)]
    assert module.e2_title(rows) == "E2: backups cut a straggler's job from 17.6 s to 11.2 s"
    assert module.e2_title([row("straggler-backups", 11.2)]).startswith("E2: backup tasks")


def test_e4_title_uses_wall_time_and_falls_back(plotter):
    """E4's title gives the restart's added wall time, or a plain title."""
    module, _ = plotter
    rows = [
        row("normal") | {"wall_seconds": "12.4"},
        row("restart") | {"wall_seconds": "14.0"},
    ]
    assert module.e4_title(rows) == "E4: a coordinator restart adds 1.6 s; the job resumes"
    assert module.e4_title(rows[:1]) == "E4: a coordinator restart, recovered from its log"


def test_e5_title_gives_each_multiple_and_skips_a_setting_without_valid_runs(plotter):
    """E5's title gives each job file's multiple and skips a setting with no valid run."""
    module, _ = plotter
    rows = [row("compiled", 38), row("interpreted", 91), row("naive", 399)]
    every = ["compiled", "interpreted", "naive"]
    assert module.e5_title(rows, every) == (
        "E5: a job file runs 2.4× slower, 10.5× without helpers"
    )
    assert module.e5_title(rows, ["compiled", "naive"]) == (
        "E5: a job file without helpers runs 10.5× slower"
    )
    rows[2]["valid"] = "0"
    assert module.e5_title(rows, every) == "E5: a job file runs 2.4× slower than compiled"


def test_e1_title_states_the_byte_and_time_savings(plotter, tmp_path):
    """E1's title gives the ranges of bytes and time saved."""
    module, pyplot = plotter
    figure, left, right = Mock(), Mock(), Mock()
    for axis in (left, right):
        axis.get_legend_handles_labels.return_value = [], []
    pyplot.subplots.return_value = figure, (left, right)
    rows = [
        row("s0.5-on", 30, 100_000_000),
        row("s0.5-off", 40, 1_500_000_000),
        row("s1.5-on", 20, 20_000_000),
        row("s1.5-off", 40, 1_560_000_000),
    ]
    module.e1(rows, tmp_path / "E1.png", light(module))
    assert titles(figure)[0] == "E1: the combiner shuffles 15–78× fewer bytes, 25–50% faster"


def fault_rows():
    """E2, E3 and E4 rows whose disturbances add 25, 100, 100, 150, 300 and 20%."""
    e2 = [row("normal", 9), row("straggler-backups", 11.25), row("straggler-no-backups", 18)]
    e3 = [row("normal") | {"wall_seconds": "10"}] + [
        row(f"kill-{p}") | {"wall_seconds": str(w)} for p, w in ((25, 20), (50, 25), (75, 40))
    ]
    e4 = [row("normal") | {"wall_seconds": "12.5"}, row("restart") | {"wall_seconds": "15"}]
    return e2, e3, e4


def test_faults_draws_the_time_each_disturbance_adds(plotter, tmp_path):
    """One bar per disturbance, as the share it adds, grouped by experiment,
    colored as E2 colors backups: on in slot 1, off in slot 2."""
    module, pyplot = plotter
    figure, axis = Mock(), Mock()
    bars = [Mock() for _ in range(6)]
    axis.barh.return_value = bars
    pyplot.subplots.return_value = figure, axis
    t = light(module)
    module.faults(*fault_rows(), tmp_path / "faults.png", t)
    ys, added = axis.barh.call_args.args
    # The first disturbance on top; a gap between E2, E3 and E4, whose baselines differ.
    assert ys == pytest.approx([6.2, 5.2, 3.6, 2.6, 1.6, 0])
    assert added == pytest.approx([25, 100, 100, 150, 300, 20])
    on, off = t["series"][0], t["series"][1]
    assert axis.barh.call_args.kwargs["color"] == [on, off, on, on, on, on]
    assert not any(b.set.called for b in bars), "color alone marks backups off, as in E2"
    assert axis.errorbar.called, "each bar has its range"
    assert axis.set_yticks.call_args.args[1] == [
        "straggler, backups on",
        "straggler, backups off",
        "worker killed at 25%",
        "worker killed at 50%",
        "worker killed at 75%",
        "coordinator restarted",
    ]
    labels = [call.args[0] for call in axis.annotate.call_args_list]
    assert labels[0] == "+25%   11.2 s vs 9.0 s"
    assert labels[-1] == "+20%   15.0 s vs 12.5 s"
    title, subtitle = titles(figure)
    assert title == "Every disturbance gave the undisturbed run's output"
    assert "bars span the range" in subtitle
    figure.savefig.assert_called_once_with(tmp_path / "faults.png")


@pytest.mark.parametrize("all_failed", [False, True], ids=["one-run-failed", "every-run-failed"])
def test_faults_claims_correct_output_only_when_every_disturbed_run_is_valid(
    plotter, tmp_path, all_failed
):
    """One invalid disturbed run, drawn or not, drops the output claim from the title."""
    module, pyplot = plotter
    figure, axis = Mock(), Mock()
    axis.barh.return_value = [Mock() for _ in range(5 if all_failed else 6)]
    pyplot.subplots.return_value = figure, axis
    e2, e3, e4 = fault_rows()
    if all_failed:  # the setting has no median, so no bar, but its failure still counts
        e3 = [r for r in e3 if r["setting"] != "kill-50"]
    e3.append(row("kill-50", valid="0") | {"wall_seconds": "90"})
    module.faults(e2, e3, e4, tmp_path / "faults.png", light(module))
    if all_failed:
        assert "worker killed at 50%" not in axis.set_yticks.call_args.args[1]
    assert titles(figure)[0] == "Fault tolerance: the time each disturbance adds"


def test_faults_leaves_out_an_experiment_without_an_undisturbed_median(plotter, tmp_path):
    """An experiment with no undisturbed median has no bars."""
    module, pyplot = plotter
    figure, axis = Mock(), Mock()
    axis.barh.return_value = [Mock() for _ in range(5)]
    pyplot.subplots.return_value = figure, axis
    e2, e3, e4 = fault_rows()
    module.faults(e2, e3, e4[1:], tmp_path / "faults.png", light(module))
    assert axis.barh.call_args.args[1] == pytest.approx([25, 100, 100, 150, 300])


def test_titles_stay_short_enough_to_read_at_a_glance(plotter, tmp_path):
    """Every computed title fits on one line of the figure: at most 60 characters."""
    module, pyplot = plotter
    figure, axis = Mock(), Mock()
    axis.barh.return_value = [Mock() for _ in range(6)]
    pyplot.subplots.return_value = figure, axis
    module.faults(*fault_rows(), tmp_path / "faults.png", light(module))
    e2, e3, e4 = fault_rows()
    for title in (
        titles(figure)[0],
        module.e2_title(e2),
        module.e4_title(e4),
        module.e5_title(
            [row("compiled", 38), row("interpreted", 91), row("naive", 399)],
            ["compiled", "interpreted", "naive"],
        ),
    ):
        assert len(title) <= 60, title


def test_e1_calls_a_combiner_that_costs_time_slower_not_faster(plotter, tmp_path):
    """A negative time saving is labeled slower, and the title claims no speed-up."""
    module, pyplot = plotter
    figure, left, right = Mock(), Mock(), Mock()
    left.get_legend_handles_labels.return_value = [], []
    pyplot.subplots.return_value = figure, (left, right)
    rows = [row("s0.5-on", 12, 1_000_000), row("s0.5-off", 10, 5_000_000)]
    module.e1(rows, tmp_path / "E1.png", light(module))
    assert notes(right, "faster", "slower") == ["20% slower"]
    assert titles(figure)[0] == "E1: the combiner shuffles 5× fewer bytes"


def test_e0_titles_the_largest_worker_count_it_has(plotter, tmp_path):
    """Without a four-worker run the title names the two workers it measured."""
    module, pyplot = plotter
    figure, axis = Mock(), Mock()
    pyplot.subplots.return_value = figure, axis
    rows = [
        row("normal", seconds) | {"program": "grep", "workers": str(w)}
        for w, seconds in ((1, 10), (2, 5))
    ]
    module.e0(rows, tmp_path / "E0.png", light(module))
    assert titles(figure)[0] == "E0: grep runs 2.0× faster on 2 workers"


def test_e0_leaves_out_warm_up_runs(plotter, tmp_path):
    """A slow warm-up run on four workers changes neither the title nor the rings."""
    module, pyplot = plotter
    figure, axis = Mock(), Mock()
    pyplot.subplots.return_value = figure, axis
    rows = [
        row(f"w{w}", seconds) | {"program": "grep", "workers": str(w)}
        for w, seconds in ((1, 10), (4, 2.5))
    ]
    rows.append(row("warmup", 40) | {"program": "grep", "workers": "4"})
    module.e0(rows, tmp_path / "E0.png", light(module))
    assert titles(figure)[0] == "E0: grep runs 4.0× faster on 4 workers"
    rings = [c for c in axis.plot.call_args_list if c.kwargs.get("linestyle") == "none"]
    assert sorted(len(c.args[1]) for c in rings) == [1, 1]


def test_faults_draws_a_disturbance_that_sped_the_job_up_left_of_zero(plotter, tmp_path):
    """A negative share is labeled with its sign and the axis extends below zero."""
    module, pyplot = plotter
    figure, axis = Mock(), Mock()
    axis.barh.return_value = [Mock() for _ in range(6)]
    pyplot.subplots.return_value = figure, axis
    e2, e3, e4 = fault_rows()
    e4[1]["wall_seconds"] = "10"  # the restarted run was faster: -20%
    module.faults(e2, e3, e4, tmp_path / "faults.png", light(module))
    labels = [call.args[0] for call in axis.annotate.call_args_list]
    assert labels[-1] == "-20%   10.0 s vs 12.5 s"
    low, high = axis.set_xlim.call_args.args
    assert low == pytest.approx(-20 * 1.45) and high == pytest.approx(300 * 1.38)


def test_every_chart_on_a_page_has_its_dark_variant():
    """Each chart a Markdown page shows sits in a <picture> whose dark source
    exists, so no page shows a light chart to a reader in dark mode."""
    import re

    root = SCRIPT.parents[1]
    pages = [p for p in root.rglob("*.md") if not {".git", "node_modules", ".venv"} & set(p.parts)]
    pictures = re.compile(r"<picture>.*?</picture>", re.S)
    checked = 0
    for page in pages:
        text = page.read_text()
        for block in pictures.findall(text):
            for dark in re.findall(
                r'<source media="\(prefers-color-scheme: dark\)" srcset="([^"]+)"', block
            ):
                assert (page.parent / dark).is_file(), f"{page}: {dark}"
                checked += 1
        bare = re.findall(r'<img [^>]*src="([^"]+\.png)"', pictures.sub("", text))
        bare += re.findall(r"!\[[^\]]*\]\(([^)]+\.png)\)", pictures.sub("", text))
        charts = [b for b in bare if "/evaluation/" in b or b.startswith("evaluation/")]
        assert not charts, f"{page} shows {charts} without a dark variant"
    assert checked, "the check found no pictures"


def e7_rows():
    """E7 rows: no backups about 25 s, the paper's rule about 17 s, LATE about 18 s."""
    rows = []
    for setting, times, backups in (
        ("mixed-off", (25, 26, 24), (0, 0)),
        ("mixed-paper", (17, 16, 18), (4, 3)),
        ("mixed-late", (18, 17, 19), (1, 1)),
    ):
        for n, s in enumerate(times, 1):
            rows.append(
                row(setting, s)
                | {"program": "distsort", "run": str(n), "backups": str(backups[0])}
                | {"backups_won": str(backups[1])}
            )
    return rows


def test_e7_compares_both_backup_rules_with_none(plotter, tmp_path):
    """No backups on top, then the paper's rule and LATE, each a median dot
    with its runs, the backups each launched in its label, and the time each
    rule saves in the title; overlapping ranges are called no clear difference."""
    module, pyplot = plotter
    figure, axis = Mock(), Mock()
    pyplot.subplots.return_value = figure, axis
    t = light(module)
    module.e7(e7_rows(), tmp_path / "E7.png", t)
    labels = axis.set_yticks.call_args.args[1]
    assert labels == [
        "no backups",
        "paper's rule\n(4 backups, 3 won)",
        "LATE\n(1 backup, 1 won)",
    ]
    assert len(single_runs(axis, t, t["series"][0])) == 3  # the paper's rule, slot 1
    assert len(single_runs(axis, t, t["series"][1])) == 3  # no backups, slot 2, as in E2
    assert len(single_runs(axis, t, t["series"][2])) == 3  # LATE, slot 3
    assert notes(axis, "%") == ["17.0 s  −32%", "18.0 s  −28%"]
    title, subtitle = titles(figure)
    assert title == "E7: backups cut a mixed-speed sort by 28–32%"
    assert len(title) <= 60
    assert "1, 0.6, 0.3 and 0.15 CPUs" in subtitle
    assert "no clear difference" in subtitle
    figure.savefig.assert_called_once_with(tmp_path / "E7.png")
