"""Tests for the coordinator's status page and metrics, ported from
go/internal/statusweb/statusweb_test.go, and for Server.status."""

import socket
import time
import urllib.error
import urllib.request

import pytest
from test_coordinator_server import Aborted, Coordinator, write

from mapreduce_reloaded.coordinator import Task, Worker, statusweb
from mapreduce_reloaded.coordinator.server import Config, LostWorker, Server, Status, WorkerStatus
from mapreduce_reloaded.gen.mapreduce.v1 import coordinator_pb2, types_pb2

MAP = types_pb2.TASK_KIND_MAP
M0 = Task(MAP, 0)

# When the status was taken: job-1 has run for 2.5 s, and job-2 ran for
# 90 s. job-3 was replayed from a log with no times.
AT = 1_792_843_200.0  # 2026-10-24 12:00:00 UTC


def _job(submitted=None, finished=None, **fields):
    j = coordinator_pb2.GetJobStatusResponse(**fields)
    if submitted is not None:
        j.submitted_at.FromNanoseconds(round(submitted * 1e9))
    if finished is not None:
        j.finished_at.FromNanoseconds(round(finished * 1e9))
    return j


STATUS = Status(
    at=AT,
    jobs=[
        _job(
            AT - 2.5,
            job_id="job-1",
            state=coordinator_pb2.JOB_STATE_MAPPING,
            map_tasks_total=7,
            map_tasks_completed=3,
            reduce_tasks_total=4,
            counters=types_pb2.Counters(
                values={
                    "map.input_records": 12,
                    "map.input.records": 20,
                    "map.output.records": 8,
                    'odd"name': 1,
                }
            ),
            error="<script>",
        ),
        _job(
            AT - 3600,
            AT - 3600 + 90,
            job_id="job-2",
            state=coordinator_pb2.JOB_STATE_SUCCEEDED,
            backup_attempts=3,
            backup_wins=1,
            map_tasks_host_local=5,
            map_tasks_zone_local=2,
        ),
        _job(job_id="job-3", state=coordinator_pb2.JOB_STATE_SUCCEEDED),
    ],
    workers=[
        WorkerStatus(Worker("w2", types_pb2.WorkerInfo(host="host-b"), AT), [M0]),
        WorkerStatus(Worker("w3", types_pb2.WorkerInfo(), AT), []),
    ],
    lost=[LostWorker("w1", AT, [M0])],
)


@pytest.fixture
def get():
    """Serves STATUS on a free port and returns a function that fetches a
    path, returning the response's status, headers and body."""
    srv = statusweb.serve(("127.0.0.1", 0), lambda: STATUS)

    def fetch(path):
        try:
            with urllib.request.urlopen(f"http://127.0.0.1:{srv.server_address[1]}{path}") as r:
                return r.status, r.headers, r.read().decode()
        except urllib.error.HTTPError as e:
            return e.code, e.headers, ""

    yield fetch
    srv.shutdown()
    srv.server_close()


def test_page(get):
    code, headers, body = get("/")
    assert code == 200 and headers["Content-Type"] == "text/html; charset=utf-8"
    for want in [
        "job-1",
        "badge state-mapping",
        ">mapping<",
        ">2.5s<",
        ">1m30s<",
        "3 started, 1 won",
        "3/7 &middot; 43%",
        'max="7" value="3"',
        "map in <b>20</b>",
        "map.input_records: 12",
        "host-b",
        "w3",
        "w1",
        str(M0),
        "&lt;script&gt;",
        "prefers-color-scheme: dark",
    ]:
        assert want in body, want
    assert "<script>" not in body
    assert "<body>" in body
    assert '<body class="dark">' in get("/?theme=dark")[2]
    assert get("/?refresh=1")[0] == 200
    assert get("/nope")[0] == 404


def _fetch(url):
    with urllib.request.urlopen(url) as r:
        return r.status


def _ipv6_loopback() -> bool:
    """Says whether this machine has an IPv6 loopback address: a container
    can have IPv6 sockets but no ::1."""
    try:
        with socket.socket(socket.AF_INET6) as s:
            s.bind(("::1", 0))
        return True
    except OSError:
        return False


@pytest.mark.skipif(not _ipv6_loopback(), reason="needs an IPv6 loopback address")
def test_serve_ipv6():
    """An empty host serves IPv6 and IPv4, and an IPv6 host binds, as Go's
    net.Listen."""
    for host, urls in [("", ["127.0.0.1", "[::1]"]), ("::1", ["[::1]"])]:
        srv = statusweb.serve((host, 0), lambda: STATUS)
        try:
            for u in urls:
                assert _fetch(f"http://{u}:{srv.server_address[1]}/") == 200
        finally:
            srv.shutdown()
            srv.server_close()


def test_serve_without_ipv6(monkeypatch):
    """Where the host has no IPv6, an empty host serves IPv4."""

    def no_ipv6(*args):
        raise OSError("Address family not supported by protocol")

    monkeypatch.setattr(statusweb, "_Server6", no_ipv6)
    srv = statusweb.serve(("", 0), lambda: STATUS)
    try:
        assert _fetch(f"http://127.0.0.1:{srv.server_address[1]}/") == 200
    finally:
        srv.shutdown()
        srv.server_close()


def test_stalled_client_is_dropped(monkeypatch):
    """A client that never finishes its request is disconnected after the
    read timeout instead of holding a thread forever."""
    monkeypatch.setattr(statusweb, "READ_TIMEOUT", 0.2)
    srv = statusweb.serve(("127.0.0.1", 0), lambda: STATUS)
    try:
        with socket.create_connection(("127.0.0.1", srv.server_address[1])) as c:
            c.sendall(b"GET / HTTP/1.1\r\n")
            c.settimeout(5)
            assert c.recv(1024) == b""
    finally:
        srv.shutdown()
        srv.server_close()


def test_empty_page():
    body = statusweb.page(Status(AT, [], [], []))
    for want in ["No jobs yet.", "No workers.", "None."]:
        assert want in body


def test_metrics(get):
    code, headers, body = get("/metrics")
    assert code == 200 and headers["Content-Type"].startswith("text/plain; version=0.0.4")
    for want in [
        "# TYPE mr_workers gauge\nmr_workers 2\n",
        'mr_job_tasks{job="job-1",kind="map",state="completed"} 3\n',
        'mr_job_tasks{job="job-1",kind="reduce",state="total"} 4\n',
        'mr_job_counter{job="job-1",name="map.input_records"} 12\n',
        'mr_job_counter{job="job-1",name="odd\\"name"} 1\n',
        'mr_job_duration_seconds{job="job-1"} 2.5\n',
        'mr_job_duration_seconds{job="job-2"} 90\n',
        'mr_job_backups{job="job-2",state="started"} 3\n',
        'mr_job_backups{job="job-2",state="won"} 1\n',
        'mr_job_map_tasks_local{job="job-2",level="host"} 5\n',
        'mr_job_map_tasks_local{job="job-2",level="zone"} 2\n',
    ]:
        assert want in body, want
    assert 'mr_job_duration_seconds{job="job-3"}' not in body


def test_label_escapes():
    assert statusweb.label('a\\b"c\nd') == '"a\\\\b\\"c\\nd"'


def test_duration_clock_set_back():
    """A job submitted before the clock was set back has run for 0 s, not less."""
    j = _job(AT)
    assert statusweb.duration(j, AT - 1) == 0
    j.finished_at.FromNanoseconds(round((AT - 1) * 1e9))
    assert statusweb.duration(j, AT) == 0


@pytest.mark.parametrize(
    "ns, want",
    [
        (0, "0s"),
        (49_999_999, "0s"),
        (50_000_000, "100ms"),
        (300_000_000, "300ms"),
        (2_450_000_000, "2.5s"),  # half rounds away from zero, as Go
        (90 * 10**9, "1m30s"),
        (3600 * 10**9 + 10**8, "1h0m0.1s"),
        (7322 * 10**9, "2h2m2s"),
    ],
)
def test_go_duration(ns, want):
    assert statusweb.go_duration(ns) == want


@pytest.mark.parametrize(
    "x, want",
    [
        (0.0, "0"),
        (2.5, "2.5"),
        (90.0, "90"),
        (123456.5, "123456.5"),
        (1e6, "1e+06"),
        (1234567.0, "1.234567e+06"),
        (0.0001, "0.0001"),
        (0.00001, "1e-05"),
        (0.000123, "0.000123"),
        (-2.5, "-2.5"),
    ],
)
def test_go_float(x, want):
    assert statusweb.go_float(x) == want


def test_server_status(tmp_path):
    """Server.status shows the jobs, the live workers with their running
    tasks and when they were last seen by the wall clock, and lost workers."""
    wall, tick = [AT], [100.0]
    c = Coordinator(Server(Config(now=lambda: wall[0], clock=lambda: tick[0], worker_timeout=5)))
    assert c.s.status() == Status(AT, [], [], [])
    w1, w2 = c.register(host="h1"), c.register(host="h2")
    job = c.submit(
        program="wordcount",
        inputs=[write(tmp_path / "in.txt")],
        output=str(tmp_path / "out"),
        num_reduce_tasks=1,
    )
    c.next(w1)
    wall[0], tick[0] = AT + 2, 102.0
    c.heartbeat(w2)
    wall[0], tick[0] = AT + 3, 103.0
    st = c.s.status()
    assert st.at == AT + 3
    assert [j.job_id for j in st.jobs] == [job]
    seen = [(w.worker.id, w.worker.info.host, w.worker.last_seen, w.running) for w in st.workers]
    assert seen == [(w1, "h1", AT, [M0]), (w2, "h2", AT + 2, [])]
    assert st.lost == []
    c.s.expire(106.0)  # w1 has been silent for 6 s
    st = c.s.status()
    assert [w.worker.id for w in st.workers] == [w2]
    assert st.lost == [LostWorker(w1, AT + 3, [M0])]


def test_server_status_while_the_log_fails(tmp_path):
    """The status page keeps working while the log refuses records."""

    class Failing:
        def append(self, record):
            raise OSError("disk full")

    c = Coordinator(Server(Config(log=Failing())))
    c.register()
    with pytest.raises(Aborted):
        c.submit(
            program="wordcount",
            inputs=[write(tmp_path / "in.txt")],
            output=str(tmp_path / "out"),
            num_reduce_tasks=1,
        )
    assert len(c.s.status().workers) == 1
    assert b"mr_workers 1\n" in statusweb.metrics(c.s.status())
    assert time.time() - c.s.status().at < 60
