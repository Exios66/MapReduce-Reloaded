"""E1's plot data and table, with matplotlib replaced at the drawing boundary."""

import csv
import importlib.util
import sys
from pathlib import Path
from types import ModuleType
from unittest.mock import Mock

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
    matplotlib.pyplot = pyplot
    monkeypatch.setitem(sys.modules, "matplotlib", matplotlib)
    monkeypatch.setitem(sys.modules, "matplotlib.pyplot", pyplot)
    monkeypatch.setattr(sys, "argv", [str(SCRIPT), str(tmp_path / "results")])
    spec = importlib.util.spec_from_file_location("plot_experiments", SCRIPT)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module, pyplot


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

    module.e1(rows, out)

    for axis, medians, errors, ylabel in (
        (shuffled, ([4, 7], [13, 20]), ([[2, 0], [6, 0]], [[1, 0], [1, 0]]), "Bytes shuffled (MB)"),
        (seconds, ([4, 3], [7, 9]), ([[2, 0], [6, 0]], [[1, 0], [1, 0]]), "Job time (s)"),
    ):
        assert axis.bar.call_count == 2
        for call, positions, heights, yerr, label in zip(
            axis.bar.call_args_list,
            ([-0.2, 0.8], [0.2, 1.2]),
            medians,
            errors,
            ("combiner on", "combiner off"),
            strict=True,
        ):
            assert call.args[0] == pytest.approx(positions)
            assert call.args[1] == pytest.approx(heights)
            assert call.kwargs["yerr"] == yerr
            assert call.kwargs["width"] == 0.4
            assert call.kwargs["label"] == label
        ticks, labels = axis.set_xticks.call_args.args
        assert list(ticks) == [0, 1]
        assert labels == ["S = 0.5", "S = 1.5"]
        axis.set_ylabel.assert_called_once_with(ylabel)
        axis.set_ylim.assert_called_once_with(bottom=0)
    figure.savefig.assert_called_once_with(out)


@pytest.mark.parametrize("combiner", ["on", "off"])
@pytest.mark.parametrize("valid", ["0", "1"])
def test_e1_handles_missing_groups_invalid_runs_and_zero_measurements(
    plotter, tmp_path, combiner, valid
):
    module, pyplot = plotter
    axes = Mock(), Mock()
    pyplot.subplots.return_value = Mock(), axes
    # One group is absent, and the other either has no valid run or a real
    # zero measurement. Neither may turn into a fabricated nonzero bar.
    module.e1([row(f"s1.0-{combiner}", 0, 0, valid)], tmp_path / "E1.png")

    for axis in axes:
        for call, group in zip(axis.bar.call_args_list, ("on", "off"), strict=True):
            kept = valid == "1" and group == combiner
            assert call.args[0] == ([-0.2 if group == "on" else 0.2] if kept else [])
            assert call.args[1] == ([0] if kept else [])
            assert call.kwargs["yerr"] == ([[0], [0]] if kept else [[], []])


def test_e1_sorts_exponents_numerically(plotter, tmp_path):
    module, pyplot = plotter
    axes = Mock(), Mock()
    pyplot.subplots.return_value = Mock(), axes
    module.e1([row("s10.0-on"), row("s2.0-on"), row("s0.5-on")], tmp_path / "E1.png")
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
    figure = Mock()
    pyplot.subplots.return_value = figure, (Mock(), Mock())
    monkeypatch.setattr(sys, "argv", [str(SCRIPT), str(results), str(out)])

    module.main()

    assert out.is_dir()
    pyplot.subplots.assert_called_once_with(1, 2, figsize=(10, 4))
    figure.savefig.assert_called_once_with(out / "E1.png")
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

    module.e3([run], tmp_path, tmp_path / "E3.png")

    right.axvspan.assert_called_once_with(
        6.5, 7.0, color="red", alpha=0.2, label="worker loss observed"
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
