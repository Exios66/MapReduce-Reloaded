"""Unit tests for E1's shell functions; no cluster or benchmark is started."""

import csv
import io
import subprocess
import textwrap
from pathlib import Path

import pytest

SCRIPT = Path(__file__).resolve().parents[2] / "bench" / "experiments.sh"


@pytest.fixture
def experiment(tmp_path, monkeypatch):
    # The script has no source guard. Load its definitions up to dispatch,
    # replacing only the initial build, then override each test's boundaries.
    definitions, marker, dispatch = SCRIPT.read_text().partition("\ncase ${1:-} in\n")
    assert marker, "experiments.sh command dispatch was not found"
    (tmp_path / "bench").mkdir()
    (tmp_path / "go").mkdir()
    log = tmp_path / "calls"
    log.touch()
    monkeypatch.setenv("CALL_LOG", str(log))
    for name, value in {"REPEAT": "3", "SIZE": "100", "M": "2", "R": "3"}.items():
        monkeypatch.setenv(name, value)

    def invoke(body, argument=None, after="", check=True):
        code = "go() { :; }\n" + definitions + "\n" + textwrap.dedent(body)
        if argument is not None:
            code += marker + dispatch
        code += "\n" + textwrap.dedent(after)
        return subprocess.run(
            ["bash", "-c", code, str(tmp_path / "bench" / "experiments.sh"), argument or ""],
            cwd=tmp_path,
            capture_output=True,
            text=True,
            check=check,
            timeout=10,
        )

    return invoke, log


@pytest.mark.parametrize("argument", ["E1", "all"])
def test_e1_pairs_all_exponents_alternates_order_and_restores_defaults(experiment, argument):
    invoke, _ = experiment
    result = invoke(
        """
        begin() { printf 'begin|%s\n' "$1"; }
        configure() { printf 'configure|%s\n' "$1"; }
        run() {
            printf '%s|%s|%s|%s|%s|%s|%s|%s\n' \
                "$1" "$2" "$3" "$4" "$5" "$6" "$zipf" "${submit_flags[*]-}"
        }
        e0() { echo E0; }
        e2() { printf 'E2|%s|%s\n' "$zipf" "${submit_flags[*]-}"; }
        e3() { echo E3; }
        e4() { echo E4; }
        """,
        argument,
        after='printf \'defaults|%s|%s\\n\' "$zipf" "${submit_flags[*]-}"',
    )
    lines = result.stdout.splitlines()
    expected = ["begin|E1", "configure|4"]
    for exponent in ("0.5", "1.0", "1.5"):
        for run, combiner in ((1, "on"), (1, "off"), (2, "off"), (2, "on"), (3, "on"), (3, "off")):
            flag = "" if combiner == "on" else "-combiner=false"
            expected.append(f"E1|s{exponent}-{combiner}|wordcount|{run}|4||{exponent}|{flag}")
    if argument == "all":
        expected = ["E0", *expected, "E2|1.0|", "E3", "E4"]
    assert lines == [*expected, "defaults|1.0|"]


@pytest.mark.parametrize("exponent", ["0.5", "1.0", "1.5"])
@pytest.mark.parametrize("cached", [False, True])
def test_wordcount_input_cache_and_generator_use_the_exponent(
    experiment, monkeypatch, exponent, cached
):
    invoke, log = experiment
    monkeypatch.setenv("ZIPF", exponent)
    monkeypatch.setenv("CACHED", "1" if cached else "0")
    result = invoke(
        """
        in_volume() {
            printf 'volume:%s\n' "$*" >> "$CALL_LOG"
            [ "$CACHED" = 1 ]
        }
        python3() {
            printf 'generate:%s\n' "$*" >> "$CALL_LOG"
            echo generated
        }
        compose_stub() {
            printf 'copy:%s:%s\n' "$1" "$3" >> "$CALL_LOG"
            [ "$(cat "$2")" = generated ]
        }
        compose=(compose_stub)
        zipf=$ZIPF
        input wordcount
        """
    )
    path = f"/out/exp-input-wordcount-100-{exponent}.txt"
    assert result.stdout == path + "\n"
    expected = [f"volume:test -f {path}"]
    if not cached:
        expected.extend(
            [f"generate:bench/gen-zipf.py 100 42 {exponent}", f"copy:cp:coordinator:{path}"]
        )
    assert log.read_text().splitlines() == expected


def test_checksum_references_are_per_input_and_reject_mismatches(experiment):
    invoke, _ = experiment
    result = invoke(
        """
        checksum() { cat "$1"; }
        echo sum-a > "$tmp/a"
        echo sum-b > "$tmp/b"
        valid /out/exp-input-wordcount-100-0.5.txt "$tmp/a"
        valid /out/exp-input-wordcount-100-1.5.txt "$tmp/b"
        valid /out/exp-input-wordcount-100-0.5.txt "$tmp/b"
        valid /out/exp-input-wordcount-100-0.5.txt "$tmp/a"
        valid /out/exp-input-wordcount-100-1.5.txt "$tmp/b"
        """
    )
    assert result.stdout.splitlines() == ["1", "1", "0", "1", "1"]


@pytest.mark.parametrize("combiner", ["on", "off"])
@pytest.mark.parametrize("free_kib", [(5 << 20) - 1, 5 << 20, (5 << 20) + 1])
def test_run_restarts_workers_checks_disk_and_forwards_combiner_flags(
    experiment, monkeypatch, combiner, free_kib
):
    invoke, log = experiment
    monkeypatch.setenv("COMBINER", combiner)
    monkeypatch.setenv("FREE_KIB", str(free_kib))
    result = invoke(
        """
        input() { echo "/out/exp-input-$1-$size-$zipf.txt"; }
        in_volume() { :; }
        log() { :; }
        sleep() { :; }
        checksum() { echo "sum-$zipf"; }
        compose_stub() {
            printf '%s\n' "$*" >> "$CALL_LOG"
            [ "$1" = restart ] || echo job-1
        }
        wait_workers() { printf 'ready:%s\n' "$1" >> "$CALL_LOG"; }
        df() {
            printf 'disk:%s\n' "$*" >> "$CALL_LOG"
            printf 'Filesystem 1024-blocks Used Available Capacity Mounted\n'
            printf 'disk 10000000 0 %s 0%% /\n' "$FREE_KIB"
        }
        status_stub() {
            printf 'job-1\tSUCCEEDED\nseconds\t2\n'
            printf 'backups\t0\nbackups_won\t0\nmap.output.bytes\t123\n'
        }
        compose=(compose_stub)
        mr=status_stub
        submit_flags=()
        [ "$COMBINER" = on ] || submit_flags=(-combiner=false)
        mkdir -p bench/results/E1
        for zipf in 0.5 1.5; do
            run E1 "s$zipf-$COMBINER" wordcount 1 4 ""
        done
        cat bench/results/E1/runs.csv
        """,
        check=False,
    )
    calls = log.read_text().splitlines()
    if free_kib < 5 << 20:
        assert result.returncode == 1
        assert "under 5 GB free on /; stopping before E1" in result.stderr
        assert calls == ["restart worker", "ready:4", "disk:-Pk /"]
        assert result.stdout == ""
        return
    assert result.returncode == 0, result.stderr
    flag = "" if combiner == "on" else "-combiner=false "
    expected = []
    for exponent in ("0.5", "1.5"):
        expected.extend(
            [
                "restart worker",
                "ready:4",
                "disk:-Pk /",
                "run --rm -q mr submit -coordinator coordinator:7070 -m 2 -r 3 "
                f"{flag}wordcount /out/exp-E1-s{exponent}-{combiner}-1 "
                f"/out/exp-input-wordcount-100-{exponent}.txt",
            ]
        )
    assert calls == expected
    rows = list(csv.reader(io.StringIO(result.stdout)))
    assert len(rows) == 2
    for row, exponent in zip(rows, ("0.5", "1.5"), strict=True):
        assert len(row) == 17
        assert row[:11] == [
            "E1",
            f"s{exponent}-{combiner}",
            "wordcount",
            "4",
            "1",
            "100",
            "2",
            "3",
            "",
            "SUCCEEDED",
            "2",
        ]
        assert float(row[11]) >= 0
        assert row[12:] == ["0", "0", "123", "1", "job-1"]


@pytest.mark.parametrize("failure", ["restart", "registration"])
def test_run_does_not_submit_when_workers_cannot_restart_or_register(
    experiment, monkeypatch, failure
):
    invoke, log = experiment
    monkeypatch.setenv("FAILURE", failure)
    result = invoke(
        """
        input() { echo /out/input.txt; }
        in_volume() { :; }
        compose_stub() {
            printf 'compose:%s\n' "$*" >> "$CALL_LOG"
            [ "$FAILURE" != restart ]
        }
        wait_workers() {
            printf 'ready:%s\n' "$1" >> "$CALL_LOG"
            [ "$FAILURE" != registration ]
        }
        compose=(compose_stub)
        run E1 s1.0-on wordcount 1 4 ""
        """,
        check=False,
    )
    assert result.returncode == 1
    expected = ["compose:restart worker"]
    if failure == "registration":
        expected.append("ready:4")
    assert log.read_text().splitlines() == expected
