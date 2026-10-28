"""Tests for python -m mapreduce_reloaded.coordinator, ported from
go/cmd/coordinator/main_test.go."""

import io
import json
import logging
import os
import re
import runpy
import signal
import sys
import threading
import urllib.request

import grpc
import pytest

from mapreduce_reloaded.coordinator import __main__ as cmd
from mapreduce_reloaded.gen.mapreduce.v1 import coordinator_pb2, coordinator_pb2_grpc, types_pb2


def test_parse_defaults():
    o = cmd.parse([])
    assert o == cmd.Options(":7070", ":8080", "", True, 100, 8, "text")
    cfg = cmd.config(o)
    assert (cfg.heartbeat_interval, cfg.worker_timeout, cfg.retry_after) == (3.0, 10.0, 1.0)
    assert (cfg.backups, cfg.keep_jobs, cfg.max_task_failures) == (True, 100, 8)


def test_parse_flags():
    o = cmd.parse(
        ["-addr", ":1", "--http=", "-data-dir", "d", "-backups=false", "-keep-jobs", "0"]
        + ["-max-task-failures", "0", "-log-format", "json"]
    )
    assert o == cmd.Options(":1", "", "d", False, 0, 0, "json")
    cfg = cmd.config(o)
    assert not cfg.backups and cfg.keep_jobs == 0
    assert cmd.parse(["-backups"]).backups and cmd.parse(["-backups=T"]).backups


@pytest.mark.parametrize(
    "args, code, msg",
    [
        (["-nope"], 2, "unrecognized arguments: -nope"),
        (["extra"], 2, "unrecognized arguments: extra"),
        (["-keep-jobs", "-1"], 2, "-keep-jobs is -1"),
        (["-max-task-failures", "2"], 2, "-max-task-failures is 2"),
        (["-log-format", "xml"], 2, "log format 'xml'"),
        (["-backups=maybe"], 2, "invalid boolean value 'maybe'"),
        (["-h"], 0, ""),
    ],
)
def test_parse_errors(args, code, msg, capsys):
    with pytest.raises(SystemExit) as e:
        cmd.parse(args)
    assert e.value.code == code
    assert msg in capsys.readouterr().err


def test_addresses():
    assert cmd.grpc_address(":7070") == "[::]:7070"
    assert cmd.grpc_address("127.0.0.1:0") == "127.0.0.1:0"
    assert cmd.http_address(":8080") == ("", 8080)
    assert cmd.http_address("127.0.0.1:0") == ("127.0.0.1", 0)
    assert cmd.http_address("[::1]:80") == ("::1", 80)


@pytest.mark.parametrize("fmt", ["text", "json"])
def test_logs(fmt):
    """The logs have slog's keys and values: an RFC 3339 time with
    milliseconds and an offset, slog's level names, and in text, quoted
    values where slog quotes them."""
    out = io.StringIO()
    cmd.setup_logs(fmt, out)
    try:
        log = logging.getLogger("mapreduce_reloaded.coordinator")
        log.info("task failed: job %s", "job-1")
        log.warning("lost")
        log.critical("down")
    finally:
        logging.getLogger("mapreduce_reloaded").handlers[:] = []
    lines = out.getvalue().splitlines()
    at = r"\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}(Z|[+-]\d\d:\d\d)"
    if fmt == "json":
        recs = [json.loads(line) for line in lines]
        assert [(r["level"], r["msg"]) for r in recs] == [
            ("INFO", "task failed: job job-1"),
            ("WARN", "lost"),
            ("ERROR", "down"),
        ]
        assert re.fullmatch(at, recs[0]["time"]), recs[0]["time"]
    else:
        assert re.fullmatch(f'time={at} level=INFO msg="task failed: job job-1"', lines[0])
        assert lines[1].endswith(" level=WARN msg=lost") and " level=ERROR " in lines[2]


@pytest.mark.parametrize(
    "v, want",
    [("a", "a"), ("", '""'), ("a b", '"a b"'), ("a=b", '"a=b"')]
    + [('a"', '"a\\""'), ("a\n", '"a\\n"')],
)
def test_logfmt(v, want):
    assert cmd._logfmt(v) == want


@pytest.fixture
def running():
    """Starts run with options in a thread; returns a function that does so
    and returns the RPC and HTTP ports and a function that stops it,
    re-raising what run raised. Every run is stopped when the test ends."""
    stops = []

    def start(o: cmd.Options):
        stop, ready, done = threading.Event(), threading.Event(), []
        ports = []

        def target():
            try:
                cmd.run(o, stop, lambda r, h: (ports.extend([r, h]), ready.set()), grace=0)
            except BaseException as e:  # reported by halt
                done.append(e)
                ready.set()

        t = threading.Thread(target=target)
        t.start()
        ready.wait(10)

        def halt():
            stop.set()
            t.join(10)
            if done:
                raise done[0]

        stops.append(halt)
        if done:
            halt()
        return ports[0], ports[1], halt

    yield start
    for halt in stops:
        try:
            halt()
        except BaseException:
            pass


def client(port):
    channel = grpc.insecure_channel(f"127.0.0.1:{port}")
    return coordinator_pb2_grpc.CoordinatorServiceStub(channel), channel


def test_run_recovers_and_serves_status(running, tmp_path):
    """With -data-dir the coordinator logs its job, and a new one on the same
    directory resumes it. With -http it serves its status page and metrics.
    Setting stop stops it without an error."""
    (tmp_path / "in.txt").write_bytes(b"a b a\n")
    o = cmd.Options(addr="127.0.0.1:0", http="127.0.0.1:0", data_dir=str(tmp_path / "wal"))
    rpc, http, halt = running(o)
    stub, channel = client(rpc)
    job = types_pb2.JobSpec(
        program="wordcount",
        inputs=[str(tmp_path / "in.txt")],
        output=str(tmp_path / "out"),
        num_reduce_tasks=1,
    )
    sub = stub.SubmitJob(coordinator_pb2.SubmitJobRequest(job=job))
    channel.close()
    for path, want in [
        ("/", sub.job_id),
        ("/metrics", f'mr_job_tasks{{job="{sub.job_id}",kind="map",state="total"}} 1'),
    ]:
        with urllib.request.urlopen(f"http://127.0.0.1:{http}{path}") as r:
            assert r.status == 200 and want in r.read().decode()
    halt()
    assert os.path.exists(tmp_path / "wal" / "coordinator.log")

    o.http = ""
    rpc, http, _ = running(o)
    assert http is None
    stub, channel = client(rpc)
    st = stub.GetJobStatus(coordinator_pb2.GetJobStatusRequest(job_id=sub.job_id))
    channel.close()
    assert st.state == coordinator_pb2.JOB_STATE_MAPPING


def test_run_without_log(running, tmp_path):
    """Without -data-dir there is no log, and a restart forgets the job. It
    must not give out the last run's IDs again: a worker still remembers
    them, and keeps files under a job's ID."""
    (tmp_path / "in.txt").write_bytes(b"a b a\n")
    ids, last = set(), "job-1"
    for run in range(2):
        rpc, _, halt = running(cmd.Options(addr="127.0.0.1:0", http=""))
        stub, channel = client(rpc)
        with pytest.raises(grpc.RpcError):
            stub.GetJobStatus(coordinator_pb2.GetJobStatusRequest(job_id=last))
        reg = stub.RegisterWorker(coordinator_pb2.RegisterWorkerRequest())
        job = types_pb2.JobSpec(
            program="wordcount",
            inputs=[str(tmp_path / "in.txt")],
            output=str(tmp_path / f"out{run}"),
            num_reduce_tasks=1,
        )
        sub = stub.SubmitJob(coordinator_pb2.SubmitJobRequest(job=job))
        channel.close()
        for id in (reg.worker_id, sub.job_id):
            assert id not in ids, f"ID {id} given out again after a restart"
            ids.add(id)
        last = sub.job_id
        halt()


def test_run_errors(running, tmp_path):
    """An RPC or status address in use stops the coordinator, which still
    closes its log; a -data-dir that cannot be made is reported. A second
    coordinator on the same port must not share it, as SO_REUSEPORT would
    let it: the two would split the workers and write one log."""
    busy = cmd.Options(addr="127.0.0.1:0", http="127.0.0.1:0", data_dir=str(tmp_path / "wal"))
    rpc, http, _ = running(busy)
    with pytest.raises(RuntimeError):
        running(cmd.Options(addr=f"127.0.0.1:{rpc}", http="", data_dir=str(tmp_path / "wal2")))
    with pytest.raises(OSError):
        running(cmd.Options(addr="127.0.0.1:0", http=f"127.0.0.1:{http}"))
    (tmp_path / "file").write_text("")
    with pytest.raises(OSError):
        running(cmd.Options(addr="127.0.0.1:0", http="", data_dir=str(tmp_path / "file")))


def test_main(tmp_path, capsys):
    """main returns 1 and prints the error when the coordinator cannot run,
    and installs handlers that stop it on SIGINT and SIGTERM."""
    (tmp_path / "file").write_text("")
    saved = {s: signal.getsignal(s) for s in (signal.SIGINT, signal.SIGTERM)}
    try:
        assert cmd.main(["-http", "", "-data-dir", str(tmp_path / "file")]) == 1
        assert capsys.readouterr().err.startswith("coordinator: ")
        assert signal.getsignal(signal.SIGTERM) is not saved[signal.SIGTERM]
    finally:
        for s, h in saved.items():
            signal.signal(s, h)
        logging.getLogger("mapreduce_reloaded").handlers[:] = []


def test_main_stops_on_signal(tmp_path, monkeypatch):
    """A signal sets the stop event that run waits on."""
    seen = {}

    def fake_run(o, stop):
        signal.getsignal(signal.SIGTERM)(signal.SIGTERM, None)
        seen["stopped"] = stop.is_set()

    monkeypatch.setattr(cmd, "run", fake_run)
    saved = {s: signal.getsignal(s) for s in (signal.SIGINT, signal.SIGTERM)}
    try:
        assert cmd.main(["-http", ""]) == 0
    finally:
        for s, h in saved.items():
            signal.signal(s, h)
        logging.getLogger("mapreduce_reloaded").handlers[:] = []
    assert seen == {"stopped": True}


def test_module_runs_main(monkeypatch, capsys):
    """python -m mapreduce_reloaded.coordinator runs main with the arguments."""
    monkeypatch.setattr("sys.argv", ["coordinator", "-h"])
    # runpy warns if the module it runs as __main__ is already imported.
    monkeypatch.delitem(sys.modules, "mapreduce_reloaded.coordinator.__main__", raising=False)
    with pytest.raises(SystemExit) as e:
        runpy.run_module("mapreduce_reloaded.coordinator", run_name="__main__")
    assert e.value.code == 0 and "-data-dir" in capsys.readouterr().out
