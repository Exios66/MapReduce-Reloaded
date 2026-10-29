"""Unit tests for the experiments' shell functions; no cluster or benchmark is started."""

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
        e5() { echo E5; }
        e6() { echo E6; }
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
        expected = ["E0", *expected, "E2|1.0|", "E3", "E4", "E5", "E6"]
    assert lines == [*expected, "defaults|1.0|"]


def test_e6_pairs_the_combiner_on_each_text_and_restores_defaults(experiment, tmp_path):
    invoke, _ = experiment
    (tmp_path / "data/input/warpeace").mkdir(parents=True)
    (tmp_path / "data/input/warpeace/warpeace.txt").write_text("text\n")
    (tmp_path / "data/input/crawl").mkdir(parents=True)
    (tmp_path / "data/input/crawl/a.warc.wet.gz").write_text("")
    result = invoke(
        """
        begin() { printf 'begin|%s\n' "$1"; }
        configure() { printf 'configure|%s\n' "$1"; }
        run() { printf '%s|%s|%s|%s|%s|%s\n' "$1" "$2" "$3" "$4" "$text" "${submit_flags[*]-}"; }
        """,
        "E6",
        after='printf \'defaults|%s|%s\\n\' "$text" "${submit_flags[*]-}"',
    )
    expected = ["begin|E6", "configure|4"]
    for text in ("crawl-eng", "crawl", "book"):
        for run, combiner in ((1, "on"), (1, "off"), (2, "off"), (2, "on"), (3, "on"), (3, "off")):
            flag = "" if combiner == "on" else "-combiner=false"
            expected.append(f"E6|{text}-{combiner}|wordcount|{run}|{text}|{flag}")
    assert result.stdout.splitlines() == [*expected, "defaults|zipf|"]


@pytest.mark.parametrize("have", ["", "book", "crawl"])
def test_e6_needs_the_book_and_the_crawl(experiment, tmp_path, have):
    invoke, _ = experiment
    if have == "book":
        (tmp_path / "data/input/warpeace").mkdir(parents=True)
        (tmp_path / "data/input/warpeace/warpeace.txt").write_text("text\n")
    if have == "crawl":
        (tmp_path / "data/input/crawl").mkdir(parents=True)
        (tmp_path / "data/input/crawl/a.warc.wet.gz").write_text("")
    result = invoke("begin() { echo begun; }", "E6", check=False)
    assert result.returncode == 1
    assert "begun" not in result.stdout
    assert "scripts/fetch-data.sh warpeace and scripts/fetch-data.sh crawl" in result.stderr


@pytest.mark.parametrize(
    "text, generator",
    [
        ("book", "bench/gen-book.py 100 42"),
        ("crawl", "bench/gen-crawl.py 100 all"),
        ("crawl-eng", "bench/gen-crawl.py 100 eng"),
    ],
)
def test_real_text_inputs(experiment, text, generator):
    invoke, log = experiment
    result = invoke(
        f"""
        in_volume() {{ return 1; }}
        python3() {{ printf 'generate:%s\n' "$*" >> "$CALL_LOG"; echo generated; }}
        compose_stub() {{ :; }}
        compose=(compose_stub)
        text={text}
        input wordcount
        """
    )
    assert result.stdout == f"/out/exp-input-wordcount-100-{text}.txt\n"
    assert log.read_text().splitlines() == [f"generate:{generator}"]


def test_e5_rotates_compiled_and_job_files_and_clears_them(experiment):
    invoke, _ = experiment
    result = invoke(
        """
        begin() { printf 'begin|%s\n' "$1"; }
        configure() { printf 'configure|%s\n' "$1"; }
        start_sampler() { printf 'sample|%s\n' "$2"; }
        stop_sampler() { :; }
        run() { printf '%s|%s|%s|%s|%s\n' "$1" "$2" "$3" "$4" "$jobfile"; }
        """,
        "E5",
        after="printf 'defaults|%s\\n' \"$jobfile\"",
    )
    expected = ["begin|E5", "configure|4"]
    jobfiles = {
        "compiled": "",
        "interpreted": "/examples/wordcount.go",
        "naive": "/jobs/wordcount-naive.go",
    }
    orders = {
        1: ("compiled", "interpreted", "naive"),
        2: ("interpreted", "naive", "compiled"),
        3: ("naive", "compiled", "interpreted"),
    }
    for run, kind in ((run, kind) for run, kinds in orders.items() for kind in kinds):
        jobfile = jobfiles[kind]
        expected += [
            f"sample|bench/results/E5/tasks-{kind}-{run}.csv",
            f"E5|{kind}|wordcount|{run}|{jobfile}",
        ]
    assert result.stdout.splitlines() == [*expected, "defaults|"]


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
    experiment, monkeypatch, tmp_path, combiner, free_kib
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
        assert "under 5 GB free on /; stopping" in result.stderr
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
                f"run --rm -q -v {tmp_path}/examples:/examples:ro "
                f"-v {tmp_path}/bench/jobs:/jobs:ro "
                "mr submit -coordinator coordinator:7070 -m 2 -r 3 "
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
    if failure == "restart":
        assert "worker restart failed; stopping" in result.stderr
    expected = ["compose:restart worker"]
    if failure == "registration":
        expected.append("ready:4")
    assert log.read_text().splitlines() == expected


@pytest.mark.parametrize("free", ["", "unknown", "-1", "5.5", "6000000x"])
def test_fresh_rejects_unexpected_disk_output(experiment, monkeypatch, free):
    invoke, log = experiment
    monkeypatch.setenv("FREE_KIB", free)
    result = invoke(
        """
        compose_stub() { :; }
        compose=(compose_stub)
        wait_workers() { :; }
        df() {
            printf 'header\n'
            printf 'disk 1 0 %s 0%% /\n' "$FREE_KIB"
        }
        fresh 4
        echo submitted >> "$CALL_LOG"
        """,
        check=False,
    )
    assert result.returncode == 1
    assert "cannot determine free space" in result.stderr
    assert log.read_text() == ""


def test_fresh_reports_restart_errors_and_normalizes_exit_status(experiment):
    invoke, _ = experiment
    result = invoke(
        """
        compose_stub() { echo 'restart diagnostic' >&2; return 42; }
        compose=(compose_stub)
        fresh 4
        """,
        check=False,
    )
    assert result.returncode == 1
    assert "restart diagnostic" in result.stderr
    assert "worker restart failed" in result.stderr


def test_fresh_rejects_failed_df_even_with_plausible_output(experiment):
    invoke, _ = experiment
    result = invoke(
        """
        compose_stub() { :; }
        compose=(compose_stub)
        wait_workers() { :; }
        df() { printf 'header\ndisk 1 0 6000000 0%% /\n'; return 1; }
        fresh 4
        """,
        check=False,
    )
    assert result.returncode == 1
    assert "cannot determine free space" in result.stderr


def test_sampled_run_restarts_workers_once_before_sampling(experiment):
    invoke, log = experiment
    invoke(
        """
        compose_stub() { printf '%s\\n' "$*" >> "$CALL_LOG"; }
        wait_workers() { printf 'ready:%s\\n' "$1" >> "$CALL_LOG"; }
        df() { printf 'h\\ndisk 1 0 %s 0%% /\\n' $((6 << 20)); }
        python3() { printf 'sampler\\n' >> "$CALL_LOG"; }
        input() { :; }
        in_volume() { :; }
        log() { printf 'submit\\n' >> "$CALL_LOG"; exit 0; }
        compose=(compose_stub)
        start_sampler tasks bench/samples.csv
        wait
        run E2 normal distsort 1 4 ""
        """
    )
    calls = log.read_text().splitlines()
    assert calls == ["restart worker", "ready:4", "sampler", "submit"]


def test_begin_keeps_three_previous_results_and_prunes_the_volume(experiment):
    invoke, log = experiment
    result = invoke(
        """
        date() {
            c=$(( $(cat "$CALL_LOG.n" 2>/dev/null || echo 0) + 1 ))
            echo "$c" > "$CALL_LOG.n"
            echo "20260101-00000$c"
        }
        in_volume() { printf 'volume:%s\\n' "$*" >> "$CALL_LOG"; }
        git() { :; }
        docker() { :; }
        python3() { cat > /dev/null; }
        for i in 1 2 3 4 5; do
            begin E1
            echo "run $i" > bench/results/E1/marker
        done
        ls bench/results/previous
        cat bench/results/previous/*/marker bench/results/E1/marker
        """
    )
    assert result.stdout.splitlines() == [
        "E1-20260101-000002",
        "E1-20260101-000003",
        "E1-20260101-000004",
        "run 2",
        "run 3",
        "run 4",
        "run 5",
    ]
    calls = log.read_text().splitlines()
    assert sum(c.startswith("volume:sh -c cd /out") for c in calls) == 5
    assert sum(c.endswith(" sh 100 E1") for c in calls) == 5


@pytest.mark.parametrize("kind", ["tasks", "bytes"])
def test_each_sampled_run_refreshes_workers_before_discovery_and_sampling(
    experiment, monkeypatch, tmp_path, kind
):
    invoke, log = experiment
    monkeypatch.setenv("INTERVAL", "0.05")
    result = invoke(
        f"kind={kind}\n"
        + """
        compose_stub() {
            printf 'compose:%s\\n' "$*" >> "$CALL_LOG"
            case "$1" in
                ps) echo worker-id ;;
                run) echo job-1 ;;
            esac
        }
        wait_workers() { printf 'ready:%s\\n' "$1" >> "$CALL_LOG"; }
        docker() {
            printf 'inspect:%s\\n' "${@: -1}" >> "$CALL_LOG"
            echo 10.0.0.9
        }
        python3() { printf 'sample:%s\\n' "$*" >> "$CALL_LOG"; }
        df() { printf 'header\\ndisk 1 0 %s 0%% /\\n' $((6 << 20)); }
        input() { echo /out/input.txt; }
        in_volume() { :; }
        checksum() { echo same-output; }
        log() { :; }
        sleep() { :; }
        status_stub() {
            printf 'job-1\\tSUCCEEDED\\nseconds\\t2\\nbackups\\t0\\n'
            printf 'backups_won\\t0\\nmap.output.bytes\\t123\\n'
        }
        compose=(compose_stub)
        mr=status_stub
        mkdir -p bench/results/E3
        for n in 1 2; do
            start_sampler "$kind" "bench/sample-$n.csv"
            wait "$sampler"
            sampler=
            run E3 normal distsort "$n" 4 ""
            [ -z "$ready" ]
        done
        cat bench/results/E3/runs.csv
        """
    )
    sampler = "sample:bench/scrape-tasks.py 0.05 86400 http://127.0.0.1:18780/metrics"
    discovery = []
    if kind == "bytes":
        discovery = ["compose:ps -q worker", "inspect:worker-id"]
        sampler = "sample:bench/scrape-metrics.py 0.05 86400 http://10.0.0.9:8081/metrics"
    expected = []
    for n in (1, 2):
        expected.extend(
            [
                "compose:restart worker",
                "ready:4",
                *discovery,
                sampler,
                f"compose:run --rm -q -v {tmp_path}/examples:/examples:ro "
                f"-v {tmp_path}/bench/jobs:/jobs:ro "
                "mr submit -coordinator coordinator:7070 -m 2 -r 3 "
                f"distsort /out/exp-E3-normal-{n} /out/input.txt",
            ]
        )
    assert log.read_text().splitlines() == expected
    rows = list(csv.reader(io.StringIO(result.stdout)))
    assert [row[4] for row in rows] == ["1", "2"]
    assert all(row[15] == "1" for row in rows)


@pytest.mark.parametrize("settings", ["normal straggler-backup", "normal-backups", "Normal"])
def test_e2_rejects_unknown_settings_before_starting(experiment, monkeypatch, settings):
    invoke, _ = experiment
    monkeypatch.setenv("E2_SETTINGS", settings)
    result = invoke(
        """
        begin() { echo begun; }
        configure() { echo configured; }
        """,
        argument="E2",
        check=False,
    )
    assert result.returncode == 2
    assert "E2_SETTINGS: unknown setting" in result.stderr
    assert "begun" not in result.stdout
    assert "configured" not in result.stdout
