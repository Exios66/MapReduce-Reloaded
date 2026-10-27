"""E1's plot data and table, with matplotlib replaced at the drawing boundary."""

import csv
import importlib.util
import sys
from pathlib import Path
from types import ModuleType
from unittest.mock import MagicMock, Mock

import pytest

SCRIPT = Path(__file__).resolve().parents[2] / "bench" / "plot-experiments.py"


@pytest.fixture
def plotter(tmp_path, monkeypatch):
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
    return module.THEMES["light"]


def row(setting, seconds=1, shuffled=1_000_000, valid="1"):
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


def test_e1_grouped_bars_use_valid_medians_ranges_and_decimal_megabytes(plotter, tmp_path):
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

    for axis, medians, errors, title in (
        (shuffled, ([4, 7], [13, 20]), ([[2, 0], [6, 0]], [[1, 0], [1, 0]]), "Bytes shuffled (MB)"),
        (seconds, ([4, 3], [7, 9]), ([[2, 0], [6, 0]], [[1, 0], [1, 0]]), "Job time (s)"),
    ):
        assert axis.bar.call_count == 2
        assert axis.errorbar.call_count == 2
        for bar, err, positions, heights, yerr, label, color in zip(
            axis.bar.call_args_list,
            axis.errorbar.call_args_list,
            ([-0.16, 0.84], [0.16, 1.16]),
            medians,
            errors,
            ("combiner on", "combiner off"),
            t["series"][:2],
            strict=True,
        ):
            assert bar.args[0] == pytest.approx(positions)
            assert bar.args[1] == pytest.approx(heights)
            assert bar.kwargs["width"] == 0.32
            assert bar.kwargs["label"] == label
            assert bar.kwargs["color"] == color
            assert bar.kwargs["edgecolor"] == t["surface"]
            assert err.args[0] == pytest.approx(positions)
            assert err.args[1] == pytest.approx(heights)
            assert err.kwargs["yerr"] == yerr
            assert err.kwargs["ecolor"] == t["ink2"]
        ticks, labels = axis.set_xticks.call_args.args
        assert list(ticks) == [0, 1]
        assert labels == ["S = 0.5", "S = 1.5"]
        axis.set_title.assert_called_once_with(title)
    # Each group with both bars is labeled with the saving.
    savings = [c.args[2] for c in shuffled.text.call_args_list]
    assert savings == ["3× fewer", "3× fewer"]
    assert [c.args[2] for c in seconds.text.call_args_list] == ["43% faster", "67% faster"]
    figure.savefig.assert_called_once_with(out)
    pyplot.close.assert_called_once_with(figure)


@pytest.mark.parametrize("combiner", ["on", "off"])
@pytest.mark.parametrize("valid", ["0", "1"])
def test_e1_handles_missing_groups_invalid_runs_and_zero_measurements(
    plotter, tmp_path, combiner, valid
):
    module, pyplot = plotter
    axes = Mock(), Mock()
    axes[0].get_legend_handles_labels.return_value = [], []
    pyplot.subplots.return_value = Mock(), axes
    # One group is absent, and the other either has no valid run or a real
    # zero measurement. Neither may turn into a fabricated nonzero bar, a
    # saving label, or a division by zero.
    module.e1([row(f"s1.0-{combiner}", 0, 0, valid)], tmp_path / "E1.png", light(module))

    for axis in axes:
        for call, group in zip(axis.bar.call_args_list, ("on", "off"), strict=True):
            kept = valid == "1" and group == combiner
            assert call.args[0] == ([-0.16 if group == "on" else 0.16] if kept else [])
            assert call.args[1] == ([0] if kept else [])
        for call in axis.errorbar.call_args_list:
            assert call.kwargs["yerr"] in ([[0], [0]], [[], []])
        axis.text.assert_not_called()
        assert axis.set_ylim.call_args.args == (0, pytest.approx(1.18))


def test_e1_sorts_exponents_numerically(plotter, tmp_path):
    module, pyplot = plotter
    axes = Mock(), Mock()
    axes[0].get_legend_handles_labels.return_value = [], []
    pyplot.subplots.return_value = Mock(), axes
    module.e1([row("s10.0-on"), row("s2.0-on"), row("s0.5-on")], tmp_path / "E1.png", light(module))
    for axis in axes:
        assert axis.set_xticks.call_args.args[1] == ["S = 0.5", "S = 2.0", "S = 10.0"]


def test_e1_table_reports_coordinator_time_and_invalid_run_counts(plotter, capsys):
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
    module, _ = plotter
    module.table("E5", [row("compiled", 38), row("interpreted", 403)])
    assert capsys.readouterr().out.splitlines()[-2:] == [
        "| compiled | wordcount | 4 | 38.00 (38.00–38.00) | 1 of 1 |",
        "| interpreted | wordcount | 4 | 403.00 (403.00–403.00) | 1 of 1 |",
    ]


def test_main_loads_e1_csv_and_selects_its_plot(plotter, tmp_path, monkeypatch, capsys):
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
    assert pyplot.subplots.call_args_list == [((1, 2), {"figsize": (11, 4.6)})] * 2
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


@pytest.mark.parametrize("counts", [(4, 4), (3, 3)])
def test_failure_interval_requires_a_recorded_transition(plotter, tmp_path, counts):
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


def tasks_file(path, job, samples, total=4):
    with path.open("w", newline="") as stream:
        writer = csv.writer(stream)
        writer.writerow(["seconds", "job", "kind", "state", "value"])
        for seconds, done in samples:
            writer.writerow([seconds, job, "map", "total", total])
            writer.writerow([seconds, job, "map", "completed", done])
            writer.writerow([seconds, "job-other", "map", "completed", total])


def test_map_phase_ends_when_every_map_task_is_done(plotter, tmp_path):
    module, _ = plotter
    path = tmp_path / "tasks.csv"
    tasks_file(path, "job-1", [(1.0, 0), (1.5, 2), (3.0, 4), (3.5, 4)])
    assert module.map_phase(path, "job-1") == pytest.approx(2.0)
    tasks_file(path, "job-1", [(1.0, 0), (1.5, 3)])
    assert module.map_phase(path, "job-1") is None


def test_e5_compares_job_and_map_phase_times(plotter, tmp_path):
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
    module, pyplot = plotter
    jobs, maps = Mock(), Mock()
    pyplot.subplots.return_value = Mock(), (jobs, maps)
    rows = [
        row("compiled", 10) | {"run": "1", "job": "job-1"},
        row("interpreted", 105) | {"run": "1", "job": "job-2"},
    ]

    module.e5(rows, tmp_path, tmp_path / "E5.png", light(module))

    assert jobs.bar.call_args.args[0] == [0, 1]
    assert jobs.set_xticks.call_args.args[1] == ["compiled", "job file\n(api.Words, api.Sum)"]
    assert [c.args[0] for c in jobs.annotate.call_args_list] == ["10.0 s", "105.0 s (10.5×)"]
