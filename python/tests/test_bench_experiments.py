"""Unit tests for the experiments' shell functions; no cluster or benchmark is started."""

import csv
import io
import json
import re
import subprocess
import sys
import textwrap
from pathlib import Path

import pytest
import yaml

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
def test_e1_pairs_all_exponents_alternates_order_and_restores_defaults(
    experiment, tmp_path, argument
):
    invoke, _ = experiment
    if argument == "all":
        # `all` checks E6's inputs before it starts E0.
        (tmp_path / "data/input/warpeace").mkdir(parents=True)
        (tmp_path / "data/input/warpeace/warpeace.txt").write_text("text\n")
        (tmp_path / "data/input/crawl").mkdir(parents=True)
        (tmp_path / "data/input/crawl/a.warc.wet.gz").write_text("")
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
    """E6 runs each input with the combiner on and off, alternating."""
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
    """E6 refuses to start without its two inputs."""
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


def test_all_checks_e6_inputs_before_e0(experiment):
    """`all` checks E6's inputs before it starts E0."""
    invoke, _ = experiment
    result = invoke("e0() { echo E0; }", "all", check=False)
    assert result.returncode == 1
    assert "E0" not in result.stdout
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
    # The time a run's logs start from varies; the rest of the call does not.
    calls = [
        c.split(" 20")[0] if c.startswith("logs ") else c for c in log.read_text().splitlines()
    ]
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
                "logs --no-color --timestamps --since",
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


def test_run_saves_the_clusters_logs_from_before_the_run(experiment, tmp_path):
    invoke, log = experiment
    result = invoke(
        """
        input() { echo /out/exp-input-wordcount-100.txt; }
        in_volume() { :; }
        log() { :; }
        sleep() { :; }
        checksum() { echo sum; }
        compose_stub() {
            printf '%s\n' "$*" >> "$CALL_LOG"
            case $1 in
            logs) echo "coordinator-1  | 2026-10-30T00:00:00Z msg=hello" ;;
            restart) ;;
            *) echo job-1 ;;
            esac
        }
        wait_workers() { :; }
        df() {
            printf 'Filesystem 1024-blocks Used Available Capacity Mounted\n'
            printf 'disk 10000000 0 9000000 0%% /\n'
        }
        status_stub() { printf 'job-1\tSUCCEEDED\nseconds\t2\n'; }
        compose=(compose_stub)
        mr=status_stub
        mkdir -p bench/results/E1
        run E1 base wordcount 2 4 ""
        """
    )
    assert result.returncode == 0, result.stderr
    saved = (tmp_path / "bench/results/E1/logs-base-wordcount-2.log").read_text()
    assert saved == "coordinator-1  | 2026-10-30T00:00:00Z msg=hello\n"
    # Taken before the workers restart, so their registrations are in it.
    calls = log.read_text().splitlines()
    (logs,) = (i for i, c in enumerate(calls) if c.startswith("logs "))
    assert calls[0] == "restart worker" and logs > 0
    assert re.fullmatch(
        r"logs --no-color --timestamps --since \d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{9}Z", calls[logs]
    )


def test_sampled_run_saves_logs_from_before_its_workers_restarted(experiment):
    invoke, log = experiment
    result = invoke(
        """
        input() { echo /out/input.txt; }
        in_volume() { :; }
        log() { :; }
        sleep() { :; }
        checksum() { echo sum; }
        save_logs() { printf 'save|%s|%s\\n' "$1" "$2" >> "$CALL_LOG"; }
        python3() { :; }
        compose_stub() { [ "$1" = restart ] || echo job-1; }
        wait_workers() { :; }
        df() {
            printf 'Filesystem 1024-blocks Used Available Capacity Mounted\\n'
            printf 'disk 10000000 0 9000000 0%% /\\n'
        }
        status_stub() { printf 'job-1\\tSUCCEEDED\\nseconds\\t2\\n'; }
        compose=(compose_stub)
        mr=status_stub
        mkdir -p bench/results/E3
        start_sampler tasks bench/sample.csv
        wait "$sampler"; sampler=
        run E3 normal distsort 1 4 ""
        """
    )
    assert result.returncode == 0, result.stderr
    (saved,) = (c for c in log.read_text().splitlines() if c.startswith("save|"))
    _, path, since = saved.split("|")
    assert path == "bench/results/E3/logs-normal-distsort-1.log"
    # The time start_sampler's restart began, not the time run began.
    assert re.fullmatch(r"\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{9}Z", since)


def test_run_reports_why_submit_failed_and_still_saves_the_logs(experiment, tmp_path, monkeypatch):
    invoke, _ = experiment
    monkeypatch.delenv("DROP_CACHES", raising=False)
    result = invoke(
        """
        input() { echo /out/exp-input-wordcount-100.txt; }
        in_volume() { :; }
        log() { :; }
        fresh() { :; }
        compose_stub() {
            case $1 in
            logs) echo "coordinator-1  | msg=refused" ;;
            *)
                echo "rpc error: code = FailedPrecondition desc = job-3 is still running" >&2
                return 1 ;;
            esac
        }
        compose=(compose_stub)
        mkdir -p bench/results/E1
        run E1 base wordcount 1 4 ""
        """,
        check=False,
    )
    assert result.returncode == 1
    assert "mr submit failed for E1 base run 1" in result.stderr
    assert "job-3 is still running" in result.stderr
    assert "msg=refused" in (tmp_path / "bench/results/E1/logs-base-wordcount-1.log").read_text()
    rows = (tmp_path / "bench/results/E1/cache.jsonl").read_text().splitlines()
    assert [json.loads(row)["phase"] for row in rows] == ["start"]


def test_save_logs_reports_a_failure_without_stopping(experiment, tmp_path):
    invoke, _ = experiment
    result = invoke(
        """
        compose_stub() { echo "no such service" >&2; return 1; }
        compose=(compose_stub)
        save_logs out.log 2026-10-30T00:00:00Z
        echo continued
        """
    )
    assert result.stdout == "continued\n"
    assert "could not save the cluster's logs to out.log" in result.stderr


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
    assert result.stderr.splitlines() == [
        "experiments: deleting bench/results/previous/E1-20260101-000001: "
        "only the last three runs of E1 are kept; results worth keeping go in bench/evaluation/"
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
                "compose:logs --no-color --timestamps --since",
            ]
        )
    calls = [
        c.split(" 20")[0] if c.startswith("compose:logs ") else c
        for c in log.read_text().splitlines()
    ]
    assert calls == expected
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


@pytest.mark.parametrize(
    "settings", ["mixed", "mixed-paper mixed-lates", "Mixed-late", "straggler-backups"]
)
def test_e7_rejects_unknown_settings_before_starting(experiment, monkeypatch, settings):
    invoke, _ = experiment
    monkeypatch.setenv("E7_SETTINGS", settings)
    result = invoke(
        """
        begin() { echo begun; }
        configure() { echo configured; }
        """,
        argument="E7",
        check=False,
    )
    assert result.returncode == 2
    assert "E7_SETTINGS: unknown setting" in result.stderr
    assert "begun" not in result.stdout
    assert "configured" not in result.stdout


@pytest.mark.parametrize(
    "cpus", ["1 0.5 0.25", "1 0.5 0.25 0.1 0.1", "1 0.5 0.25 slow", "1 0 1 1", "1 -1 1 1"]
)
def test_e7_rejects_cpu_limits_that_do_not_fit_four_workers(experiment, monkeypatch, cpus):
    invoke, _ = experiment
    monkeypatch.setenv("E7_CPUS", cpus)
    result = invoke(
        """
        begin() { echo begun; }
        configure() { echo configured; }
        """,
        argument="E7",
        check=False,
    )
    assert result.returncode == 2
    assert "E7_CPUS" in result.stderr
    assert "begun" not in result.stdout
    assert "configured" not in result.stdout


@pytest.mark.parametrize("repeat", ["1", "2"])
def test_e7_takes_settings_in_turn_with_their_overrides_and_throttles(
    experiment, monkeypatch, repeat
):
    """Each setting gets its coordinator override and the same four CPU limits, restored after."""
    invoke, log = experiment
    monkeypatch.setenv("REPEAT", repeat)
    result = invoke(
        """
        note() { printf '%s\n' "$*" >> "$CALL_LOG"; }
        begin() { note "begin|$1"; }
        configure() { note "configure|$1|${2:-}"; }
        compose_stub() { printf 'w1\nw2\nw3\nw4\n'; }
        compose=(compose_stub)
        docker() { note "docker|$*"; }
        start_sampler() { note "sample|$1|$2"; }
        stop_sampler() { note stop; }
        run() { note "run|$1|$2|$3|$4|$5|$slow"; }
        """,
        "E7",
        after='note "defaults|$slow"',
    )
    assert result.stdout == ""
    overrides = {
        "mixed-paper": "",
        "mixed-late": "speculation-late.yaml",
        "mixed-off": "backups-off.yaml",
    }
    expected = ["begin|E7"]
    orders = {1: list(overrides), 2: list(reversed(overrides))}
    for n in range(1, int(repeat) + 1):
        for setting in orders[n]:
            expected.append(f"configure|4|{overrides[setting]}")
            expected += [
                f"docker|update --cpus {cpus} w{i}"
                for i, cpus in enumerate(("1", "0.6", "0.3", "0.15"), 1)
            ]
            expected += [
                f"sample|tasks|bench/results/E7/tasks-{setting}-{n}.csv",
                f"run|E7|{setting}|distsort|{n}|4|w1 w2 w3 w4",
                "stop",
            ]
            expected += [f"docker|update --cpus 1 w{i}" for i in range(1, 5)]
    expected += ["configure|4|", "defaults|"]
    assert log.read_text().splitlines() == expected


def test_e7_cpu_limits_and_settings_can_be_chosen(experiment, monkeypatch):
    invoke, log = experiment
    monkeypatch.setenv("REPEAT", "1")
    monkeypatch.setenv("E7_CPUS", "1 1 0.5 .2")
    monkeypatch.setenv("E7_SETTINGS", "mixed-late")
    invoke(
        """
        note() { printf '%s\n' "$*" >> "$CALL_LOG"; }
        begin() { :; }
        configure() { note "configure|$1|${2:-}"; }
        compose_stub() { printf 'w1\nw2\nw3\nw4\n'; }
        compose=(compose_stub)
        docker() { note "docker|$*"; }
        start_sampler() { :; }
        stop_sampler() { :; }
        run() { :; }
        """,
        "E7",
    )
    assert log.read_text().splitlines() == [
        "configure|4|speculation-late.yaml",
        "docker|update --cpus 1 w1",
        "docker|update --cpus 1 w2",
        "docker|update --cpus 0.5 w3",
        "docker|update --cpus .2 w4",
        *(f"docker|update --cpus 1 w{i}" for i in range(1, 5)),
        "configure|4|",
    ]


def test_e7_stops_when_the_cluster_has_another_number_of_workers(experiment):
    invoke, log = experiment
    result = invoke(
        """
        begin() { :; }
        configure() { :; }
        compose_stub() { printf 'w1\nw2\nw3\n'; }
        compose=(compose_stub)
        docker() { echo "docker|$*" >> "$CALL_LOG"; }
        run() { echo submitted >> "$CALL_LOG"; }
        """,
        "E7",
        check=False,
    )
    assert result.returncode == 1
    assert "3 workers are running; expected 4 CPU limits" in result.stderr
    assert log.read_text() == ""


def test_cleanup_restores_every_throttled_worker(experiment):
    """A script that exits mid-run (an error, Ctrl-C) leaves no worker throttled."""
    invoke, log = experiment
    invoke(
        """
        docker() { echo "docker|$*" >> "$CALL_LOG"; }
        slow="w1 w2 w3 w4"
        exit 0
        """
    )
    assert log.read_text().splitlines() == [f"docker|update --cpus 1 w{i}" for i in range(1, 5)]


def test_e7_is_run_by_name_and_left_out_of_all(experiment, tmp_path):
    """E7 is groundwork for #59: `all` keeps running E0 to E6 only."""
    invoke, _ = experiment
    (tmp_path / "data/input/warpeace").mkdir(parents=True)
    (tmp_path / "data/input/warpeace/warpeace.txt").write_text("text\n")
    (tmp_path / "data/input/crawl").mkdir(parents=True)
    (tmp_path / "data/input/crawl/a.warc.wet.gz").write_text("")
    stubs = "".join(f"e{i}() {{ echo E{i}; }}\n" for i in range(8))
    assert invoke(stubs, "E7").stdout.splitlines() == ["E7"]
    assert invoke(stubs, "all").stdout.splitlines() == [f"E{i}" for i in range(7)]


def test_usage_lists_e7(experiment):
    invoke, _ = experiment
    result = invoke("", "E9", check=False)
    assert result.returncode == 2
    assert "E0|E1|E2|E3|E4|E5|E6|E7|all" in result.stderr


def test_every_compose_override_the_script_uses_exists_and_sets_the_speculation():
    """E2 and E7 name an override in deploy/compose; it must be there and change the flag."""
    text = SCRIPT.read_text()
    root = SCRIPT.parents[1] / "deploy/compose"
    used = set(re.findall(r"\bconfigure 4 ([\w.-]+\.yaml)", text))
    assert used == {"backups-off.yaml", "speculation-late.yaml"}
    flags = {"backups-off.yaml": "-backups=false", "speculation-late.yaml": "-speculation=late"}
    for name in sorted(used):
        override = yaml.safe_load((root / name).read_text())
        command = override["services"]["coordinator"]["command"]
        assert command[:5] == ["coordinator", "-addr", ":7070", "-http", ":8080"], name
        assert command[command.index("-data-dir") + 1] == "/out/wal", name
        assert command[-1] == flags[name], name


@pytest.mark.parametrize(
    ("curl", "reported"),
    [("echo 'mr_workers 2'", "has 2"), ("return 22", "has none")],
    ids=["wrong-count", "coordinator-unreachable"],
)
def test_registration_timeout_saves_the_logs_since_the_restart(
    experiment, tmp_path, curl, reported
):
    invoke, log = experiment
    result = invoke(
        f"""
        sleep() {{ :; }}
        curl() {{ {curl}; }}
        compose_stub() {{ printf 'compose:%s\\n' "$*" >> "$CALL_LOG"; }}
        compose=(compose_stub)
        metrics=http://unused
        results_dir=bench/results/E1
        fresh_at=2026-10-30T00:00:00.000000000Z
        mkdir -p "$results_dir"
        wait_workers 4
        """,
        check=False,
    )
    assert result.returncode == 1
    assert f"expected 4 workers, the coordinator {reported}" in result.stderr
    assert log.read_text().splitlines() == [
        "compose:logs --no-color --timestamps --since 2026-10-30T00:00:00.000000000Z"
    ]
    assert (tmp_path / "bench/results/E1/logs-registration-timeout.log").exists()


def test_cache_prepare_records_the_page_cache_state_without_dropping_it(
    experiment, tmp_path, monkeypatch
):
    invoke, log = experiment
    monkeypatch.delenv("DROP_CACHES", raising=False)
    invoke(
        """
        sudo() { echo "sudo:$*" >> "$CALL_LOG"; }
        cache_prepare bench/results/E1 E1 s1.0-on wordcount 2
        cache_finish bench/results/E1 E1 s1.0-on wordcount 2
        """
    )
    start, end = (
        json.loads(line)
        for line in (tmp_path / "bench/results/E1/cache.jsonl").read_text().splitlines()
    )
    assert (start["phase"], end["phase"]) == ("start", "end")
    assert start["dropped"] is False and end["dropped"] is None
    assert (start["experiment"], start["setting"], start["program"], start["run"]) == (
        "E1",
        "s1.0-on",
        "wordcount",
        2,
    )
    assert start["input_bytes"] == 100
    fields = ("mem_free_kib", "cached_kib", "dirty_kib", "ram_kib", "disk_read_sectors")
    for row in (start, end):
        for field in fields:
            assert row[field] is None or isinstance(row[field], int)
    if sys.platform.startswith("linux"):
        assert all(isinstance(start[f], int) for f in fields[:4])
    assert log.read_text() == ""  # nothing was dropped


def test_cache_row_reports_null_for_what_the_host_cannot_say(experiment, tmp_path, monkeypatch):
    invoke, _ = experiment
    monkeypatch.delenv("DROP_CACHES", raising=False)
    invoke(
        """
        awk() { return 1; }
        cache_prepare bench/results/E1 E1 base grep 1
        """
    )
    row = json.loads((tmp_path / "bench/results/E1/cache.jsonl").read_text())
    for field in ("mem_free_kib", "cached_kib", "dirty_kib", "ram_kib", "disk_read_sectors"):
        assert row[field] is None


@pytest.mark.parametrize(
    ("diskstats", "expected"),
    [
        (
            "8 0 sda 0 0 11\n8 1 sda1 0 0 90\n"
            "252 0 vda 0 0 12\n202 0 xvda 0 0 13\n"
            "259 0 nvme0n1 0 0 14\n259 1 nvme0n1p1 0 0 90\n"
            "179 0 mmcblk0 0 0 15\n179 1 mmcblk0p1 0 0 90\n",
            65,
        ),
        ("179 0 mmcblk0 0 0 0\n", 0),
        ("7 0 loop0 0 0 42\n179 1 mmcblk0p1 0 0 90\n", None),
        ("", None),
    ],
)
def test_cache_row_counts_recognized_whole_disks(
    experiment, tmp_path, monkeypatch, diskstats, expected
):
    invoke, _ = experiment
    fixture = tmp_path / "diskstats"
    fixture.write_text(diskstats)
    monkeypatch.setenv("DISKSTATS_FIXTURE", str(fixture))
    invoke(
        """
        awk() {
            if [ "$2" = /proc/diskstats ]; then
                command awk "$1" "$DISKSTATS_FIXTURE"
            else
                command awk "$@"
            fi
        }
        cache_prepare bench/results/E1 E1 base grep 1
        """
    )
    row = json.loads((tmp_path / "bench/results/E1/cache.jsonl").read_text())
    assert row["disk_read_sectors"] == expected


def test_cache_prepare_drops_the_caches_when_asked(experiment, tmp_path, monkeypatch):
    invoke, log = experiment
    monkeypatch.setenv("DROP_CACHES", "1")
    invoke(
        """
        id() { echo 1000; }
        sync() { echo sync >> "$CALL_LOG"; }
        sudo() {
            echo "sudo:$*" >> "$CALL_LOG"
            IFS= read -r value
            echo "value:$value" >> "$CALL_LOG"
        }
        cache_prepare bench/results/E1 E1 s1.0-on wordcount 1
        """
    )
    assert json.loads((tmp_path / "bench/results/E1/cache.jsonl").read_text())["dropped"] is True
    assert log.read_text().splitlines() == [
        "sync",
        "sudo:-n tee /proc/sys/vm/drop_caches",
        "value:3",
    ]


def test_cache_prepare_stops_rather_than_record_a_cold_run_it_could_not_make(
    experiment, tmp_path, monkeypatch
):
    invoke, _ = experiment
    monkeypatch.setenv("DROP_CACHES", "1")
    result = invoke(
        """
        id() { echo 1000; }
        sync() { :; }
        sudo() { return 1; }
        cache_prepare bench/results/E1 E1 s1.0-on wordcount 1
        """,
        check=False,
    )
    assert result.returncode == 1
    assert "page cache could not be dropped" in result.stderr
    assert not (tmp_path / "bench/results/E1/cache.jsonl").exists()


def test_run_records_the_cache_after_the_restart_and_around_the_job(experiment):
    invoke, log = experiment
    result = invoke(
        """
        input() { echo /out/exp-input-wordcount-100.txt; }
        in_volume() { :; }
        log() { :; }
        sleep() { :; }
        checksum() { echo sum; }
        cache_prepare() { echo "cache:start:$*" >> "$CALL_LOG"; }
        cache_finish() { echo "cache:end:$*" >> "$CALL_LOG"; }
        date() {
            [ "$1" != +%s.%N ] || echo clock >> "$CALL_LOG"
            command date "$@"
        }
        save_logs() { :; }
        compose_stub() {
            printf '%s\n' "$*" >> "$CALL_LOG"
            case $1 in
            restart) ;;
            *) echo job-1 ;;
            esac
        }
        wait_workers() { :; }
        df() {
            printf 'Filesystem 1024-blocks Used Available Capacity Mounted\n'
            printf 'disk 10000000 0 9000000 0%% /\n'
        }
        status_stub() {
            echo status >> "$CALL_LOG"
            printf 'job-1\tSUCCEEDED\nseconds\t2\n'
        }
        compose=(compose_stub)
        mr=status_stub
        mkdir -p bench/results/E1
        run E1 base wordcount 1 4 ""
        """
    )
    assert result.returncode == 0, result.stderr
    calls = log.read_text().splitlines()
    restart = calls.index("restart worker")
    start = calls.index("cache:start:bench/results/E1 E1 base wordcount 1")
    submit = next(i for i, c in enumerate(calls) if c.startswith("run "))
    clock_start, clock_end = (i for i, call in enumerate(calls) if call == "clock")
    status = calls.index("status")
    end = calls.index("cache:end:bench/results/E1 E1 base wordcount 1")
    assert restart < start < submit < clock_start < status < clock_end < end


def test_cache_rows_measure_whole_disks_and_append_each_trial(experiment, tmp_path, monkeypatch):
    invoke, _ = experiment
    monkeypatch.delenv("DROP_CACHES", raising=False)
    (tmp_path / "meminfo").write_text(
        "MemTotal: 1000 kB\nMemFree: 200 kB\nCached: 300 kB\nDirty: 4 kB\n"
    )
    (tmp_path / "diskstats").write_text(
        "   8 0 sda 1 2 11 4\n"
        "   8 1 sda1 1 2 1000 4\n"
        "   8 16 sdb 1 2 17 4\n"
        "   8 32 sdaa 1 2 23 4\n"
        " 252 0 vda 1 2 29 4\n"
        " 202 0 xvda 1 2 31 4\n"
        " 259 0 nvme0n1 1 2 37 4\n"
        " 259 1 nvme0n1p1 1 2 1000 4\n"
        "   7 0 loop0 1 2 1000 4\n"
        " 253 0 dm-0 1 2 1000 4\n"
    )
    invoke(
        """
        awk() {
            case "$2" in
                /proc/meminfo) command awk "$1" meminfo ;;
                /proc/diskstats) command awk "$1" diskstats ;;
                *) command awk "$@" ;;
            esac
        }
        date() { printf '2026-10-30T12:34:56Z\\n'; }
        cache_prepare bench/results/E1 E1 base grep 1
        printf '   8 0 sda 1 2 200 4\\n' > diskstats
        cache_finish bench/results/E1 E1 base grep 1
        cache_prepare bench/results/E1 E1 alternate distsort 2
        printf '   7 0 loop0 1 2 999 4\\n' > diskstats
        cache_finish bench/results/E1 E1 alternate distsort 2
        """
    )
    rows = [
        json.loads(line)
        for line in (tmp_path / "bench/results/E1/cache.jsonl").read_text().splitlines()
    ]
    assert [(row["phase"], row["setting"], row["program"], row["run"]) for row in rows] == [
        ("start", "base", "grep", 1),
        ("end", "base", "grep", 1),
        ("start", "alternate", "distsort", 2),
        ("end", "alternate", "distsort", 2),
    ]
    assert [row["disk_read_sectors"] for row in rows] == [148, 200, 200, None]
    assert [row["dropped"] for row in rows] == [False, None, False, None]
    for row in rows:
        assert row["experiment"] == "E1"
        assert row["at"] == "2026-10-30T12:34:56Z"
        assert row["input_bytes"] == 100
        assert [row[field] for field in ("mem_free_kib", "cached_kib", "dirty_kib", "ram_kib")] == [
            200,
            300,
            4,
            1000,
        ]


@pytest.mark.parametrize("missing", ["meminfo", "diskstats"])
def test_cache_row_keeps_available_metrics_when_one_source_fails(
    experiment, tmp_path, monkeypatch, missing
):
    invoke, _ = experiment
    monkeypatch.delenv("DROP_CACHES", raising=False)
    monkeypatch.setenv("MISSING_SOURCE", missing)
    (tmp_path / "meminfo").write_text(
        "MemFree: 20 kB\nCached: 30 kB\nDirty: 4 kB\nMemTotal: 100 kB\n"
    )
    (tmp_path / "diskstats").write_text("8 0 sda 1 2 55 4\n")
    invoke(
        """
        awk() {
            case "$2" in
                /proc/meminfo|/proc/diskstats)
                    file=${2##*/}
                    [ "$file" != "$MISSING_SOURCE" ] || return 1
                    command awk "$1" "$file" ;;
                *) command awk "$@" ;;
            esac
        }
        cache_prepare bench/results/E1 E1 base grep 1
        """
    )
    row = json.loads((tmp_path / "bench/results/E1/cache.jsonl").read_text())
    memory = [row[field] for field in ("mem_free_kib", "cached_kib", "dirty_kib", "ram_kib")]
    assert memory == ([None] * 4 if missing == "meminfo" else [20, 30, 4, 100])
    assert row["disk_read_sectors"] == (None if missing == "diskstats" else 55)


@pytest.mark.parametrize("value", ["0", "true", "yes"])
def test_cache_prepare_drops_only_for_exact_opt_in(experiment, tmp_path, monkeypatch, value):
    invoke, log = experiment
    monkeypatch.setenv("DROP_CACHES", value)
    invoke(
        """
        sync() { echo sync >> "$CALL_LOG"; }
        sudo() { echo sudo >> "$CALL_LOG"; }
        cache_prepare bench/results/E1 E1 base grep 1
        """
    )
    assert json.loads((tmp_path / "bench/results/E1/cache.jsonl").read_text())["dropped"] is False
    assert log.read_text() == ""


def test_cache_drop_failure_stops_run_without_losing_earlier_rows(
    experiment, tmp_path, monkeypatch
):
    invoke, log = experiment
    monkeypatch.setenv("DROP_CACHES", "1")
    cache_file = tmp_path / "bench/results/E1/cache.jsonl"
    cache_file.parent.mkdir(parents=True)
    cache_file.write_text('{"phase":"end","run":1}\n')
    result = invoke(
        """
        input() { echo /out/input.txt; }
        in_volume() { :; }
        fresh() { echo restarted >> "$CALL_LOG"; }
        log() { :; }
        id() { echo 1000; }
        sync() { echo sync >> "$CALL_LOG"; }
        sudo() { cat > /dev/null; return 1; }
        compose_stub() { echo submitted >> "$CALL_LOG"; }
        compose=(compose_stub)
        run E1 base grep 2 4 ""
        """,
        check=False,
    )
    assert result.returncode == 1
    assert "page cache could not be dropped" in result.stderr
    assert log.read_text().splitlines() == ["restarted", "sync"]
    assert cache_file.read_text() == '{"phase":"end","run":1}\n'


def test_failed_job_still_records_its_end_cache_row(experiment, tmp_path, monkeypatch):
    invoke, _ = experiment
    monkeypatch.delenv("DROP_CACHES", raising=False)
    invoke(
        """
        input() { echo /out/input.txt; }
        in_volume() { :; }
        fresh() { :; }
        log() { :; }
        sleep() { :; }
        save_logs() { :; }
        compose_stub() { echo job-1; }
        status_stub() { printf 'job-1\\tFAILED\\nseconds\\t2\\n'; }
        compose=(compose_stub)
        mr=status_stub
        run E1 base grep 1 4 ""
        """
    )
    result_dir = tmp_path / "bench/results/E1"
    rows = [json.loads(line) for line in (result_dir / "cache.jsonl").read_text().splitlines()]
    assert [(row["phase"], row["run"], row["dropped"]) for row in rows] == [
        ("start", 1, False),
        ("end", 1, None),
    ]
    with (result_dir / "runs.csv").open(newline="") as result_file:
        (run,) = csv.reader(result_file)
    assert run[9] == "FAILED"
    assert run[-2:] == ["0", "job-1"]
