"""Tests for the coordinator's gRPC service, ported from
go/internal/coordinator/server_test.go and failure_test.go."""

import os
import threading
import time

import grpc
import pytest

from mapreduce_reloaded.coordinator import SKIP_AFTER
from mapreduce_reloaded.coordinator.server import MAX_LOST, TEMP_DIR, Config, Server, serve
from mapreduce_reloaded.coordinator.wire import MAX_REDUCE_TASKS, input_split, path, uri, validate
from mapreduce_reloaded.formats import Split
from mapreduce_reloaded.gen.mapreduce.v1 import (
    coordinator_pb2,
    coordinator_pb2_grpc,
    log_pb2,
    types_pb2,
)
from mapreduce_reloaded.split import MAX_SPLITS

MAP, REDUCE = types_pb2.TASK_KIND_MAP, types_pb2.TASK_KIND_REDUCE
MAPPING = coordinator_pb2.JOB_STATE_MAPPING
REDUCING = coordinator_pb2.JOB_STATE_REDUCING
SUCCEEDED = coordinator_pb2.JOB_STATE_SUCCEEDED
FAILED = coordinator_pb2.JOB_STATE_FAILED


class Aborted(Exception):
    def __init__(self, code, details):
        super().__init__(f"{code}: {details}")
        self.code, self.details = code, details


class Context:
    """Stands in for a grpc.ServicerContext: abort raises."""

    def abort(self, code, details):
        raise Aborted(code, details)


CTX = Context()


def code(fn, *args):
    """Calls fn and returns the status code it aborted with."""
    with pytest.raises(Aborted) as e:
        fn(*args, CTX)
    return e.value.code


class Coordinator:
    """Calls a Server's RPCs as a client would, with keyword arguments."""

    def __init__(self, s: Server):
        self.s = s

    def register(self, host="", zone=""):
        req = coordinator_pb2.RegisterWorkerRequest(worker=types_pb2.WorkerInfo(host=host, zone=zone))
        return self.s.RegisterWorker(req, CTX).worker_id

    def submit(self, **job):
        req = coordinator_pb2.SubmitJobRequest(job=types_pb2.JobSpec(**job))
        return self.s.SubmitJob(req, CTX).job_id

    def request(self, worker, paused=False):
        req = coordinator_pb2.RequestTaskRequest(worker_id=worker, paused=paused)
        return self.s.RequestTask(req, CTX)

    def next(self, worker):
        resp = self.request(worker)
        return resp.map_task.attempt if resp.HasField("map_task") else resp.reduce_task.attempt

    def report(self, worker, attempt, **outcome):
        req = coordinator_pb2.ReportTaskRequest(worker_id=worker, attempt=attempt, **outcome)
        return self.s.ReportTask(req, CTX).accepted

    def succeed(self, worker, attempt, outs=(), counters=None):
        counters = types_pb2.Counters(values={"n": 1} if counters is None else counters)
        success = types_pb2.TaskSuccess(outputs=outs, counters=counters)
        return self.report(worker, attempt, success=success)

    def fail(self, worker, attempt, **failure):
        return self.report(worker, attempt, failure=types_pb2.TaskFailure(**failure))

    def status(self, job):
        return self.s.GetJobStatus(coordinator_pb2.GetJobStatusRequest(job_id=job), CTX)

    def heartbeat(self, worker, *running):
        progress = [types_pb2.TaskProgress(attempt=a) for a in running]
        req = coordinator_pb2.HeartbeatRequest(worker_id=worker, running=progress)
        return self.s.Heartbeat(req, CTX)


def outputs(i, r):
    return [
        types_pb2.PartitionLocation(map_index=i, partition=p, uri="file:///map-output")
        for p in range(r)
    ]


def write(p, data=b"a b\n"):
    p.write_bytes(data)
    return str(p)


@pytest.fixture
def new_job(tmp_path_factory):
    """Returns a server with one worker and one submitted job over a small
    input in one split, with r reduce tasks (0 for the default)."""

    def make(r=1, cfg=None):
        c = Coordinator(Server(cfg))
        w = c.register()
        tmp = tmp_path_factory.mktemp("job")
        job = c.submit(
            program="wordcount",
            inputs=[write(tmp / "in.txt")],
            output=str(tmp / "out"),
            num_reduce_tasks=r,
        )
        return c, w, job

    return make


def end_active(s: Server, err=""):
    with s._mu:
        s._end(s._active, err, s.now())


def test_submit_job(new_job, tmp_path):
    c, _, job = new_job(r=0)
    st = c.status(job)
    assert (st.reduce_tasks_total, st.map_tasks_total, st.state) == (2, 1, MAPPING)
    assert st.submitted_at.seconds > 0 and not st.HasField("finished_at")
    s = c.s
    invalid = grpc.StatusCode.INVALID_ARGUMENT

    def submit(**job):
        return code(s.SubmitJob, coordinator_pb2.SubmitJobRequest(job=types_pb2.JobSpec(**job)))

    out = str(tmp_path / "o")
    wc = {"program": "wordcount", "output": out, "num_reduce_tasks": 1}
    assert submit(inputs=[str(tmp_path / "x")], **wc) == invalid  # no input matches
    assert submit(inputs=[write(tmp_path / "empty.txt", b"")], **wc) == invalid
    assert submit(program="nope") == invalid
    assert code(s.SubmitJob, coordinator_pb2.SubmitJobRequest()) == invalid

    used = tmp_path / "used"  # an earlier job's output
    used.mkdir()
    done = write(used / "part-r-00000", b"x\n")
    req = coordinator_pb2.SubmitJobRequest(
        job=types_pb2.JobSpec(
            program="wordcount", inputs=[done], output=str(used), num_reduce_tasks=1
        )
    )
    assert code(Server().SubmitJob, req) == grpc.StatusCode.ALREADY_EXISTS


def test_submit_job_messages(tmp_path):
    s = Server()

    def details(**job):
        with pytest.raises(Aborted) as e:
            s.SubmitJob(coordinator_pb2.SubmitJobRequest(job=types_pb2.JobSpec(**job)), CTX)
        return e.value.details

    assert details() == "\n".join(
        ['wire: unknown program ""', "no Inputs", "no Output"]
    )  # R defaults to 1 with no workers
    out = str(tmp_path / "o")
    assert details(program="wordcount", inputs=[str(tmp_path / "x")], output=out).startswith(
        "split: "
    )
    empty = write(tmp_path / "e", b"")
    assert details(program="wordcount", inputs=[empty], output=out) == "the input is empty"
    assert not (tmp_path / "o").exists()  # nothing set up for a rejected job


def test_submit_job_sets_up_output_and_splits(tmp_path):
    seen = []
    c = Coordinator(Server(Config(locate=lambda s: seen.append(s.uri) or ["h"])))
    inputs = [write(tmp_path / f"{i}.txt", b"a\n" * (i + 1)) for i in range(2)]
    out = tmp_path / "out"
    job = c.submit(program="wordcount", inputs=inputs, output=uri(str(out)), num_reduce_tasks=1)
    assert (out / TEMP_DIR).is_dir()
    j = c.s._jobs[job]
    assert [(s.uri, s.offset, s.length) for s in j.splits] == [
        (uri(inputs[0]), 0, 2),
        (uri(inputs[1]), 0, 4),
    ]
    assert seen == [uri(p) for p in inputs] and all(s.locations == ["h"] for s in j.splits)
    # The submitted spec is a copy, with R filled in.
    assert j.spec.num_reduce_tasks == 1


def test_submit_job_num_map_tasks(tmp_path):
    c = Coordinator(Server())
    big = write(tmp_path / "in.txt", b"x\n" * 50)
    job = c.submit(
        program="wordcount", inputs=[big], output=str(tmp_path / "o"), num_reduce_tasks=1,
        num_map_tasks=4,
    )
    assert c.status(job).map_tasks_total == 4


def test_submit_job_default_r_counts_workers(tmp_path):
    c = Coordinator(Server())
    for _ in range(3):
        c.register()
    job = c.submit(
        program="wordcount", inputs=[write(tmp_path / "in")], output=str(tmp_path / "o")
    )
    assert c.status(job).reduce_tasks_total == 3 * 2


def test_submit_job_output_setup_fails(tmp_path):
    blocker = write(tmp_path / "file", b"")  # a file where the output dir goes
    req = coordinator_pb2.SubmitJobRequest(
        job=types_pb2.JobSpec(
            program="wordcount", inputs=[write(tmp_path / "in")], output=blocker,
            num_reduce_tasks=1,
        )
    )
    s = Server()
    assert code(s.SubmitJob, req) == grpc.StatusCode.FAILED_PRECONDITION
    assert s._jobs == {} and s._active is None


def test_one_job_at_a_time(new_job, tmp_path):
    c, _, job = new_job()
    req = coordinator_pb2.SubmitJobRequest(
        job=types_pb2.JobSpec(
            program="wordcount", inputs=[write(tmp_path / "in")], output=str(tmp_path / "o"),
            num_reduce_tasks=1,
        )
    )
    with pytest.raises(Aborted) as e:
        c.s.SubmitJob(req, CTX)
    assert e.value.code == grpc.StatusCode.FAILED_PRECONDITION
    assert e.value.details == f"job {job} is still running"


def test_request_task(new_job):
    c, w, job = new_job()
    with pytest.raises(Aborted) as e:
        c.request("nope")
    assert e.value.code == grpc.StatusCode.NOT_FOUND
    assert e.value.details == 'unknown worker "nope": register again'

    resp = c.request(w)
    m = resp.map_task
    assert resp.HasField("map_task")
    assert (m.attempt.task.job_id, m.attempt.task.kind, m.attempt.task.index) == (job, MAP, 0)
    assert m.job.program == "wordcount" and m.split.length == 4
    resp = c.request(w)  # no backup of its own attempt
    assert resp.HasField("wait") and resp.wait.retry_after.ToNanoseconds() == 1_000_000_000
    c.s.shutdown()
    assert c.request(w).HasField("shutdown")


def test_request_task_backups(new_job):
    c, w, _ = new_job(cfg=Config(backups=False))
    c.next(w)
    other = c.register()
    for worker in (w, other):
        assert c.request(worker).HasField("wait")
    c, w, _ = new_job(cfg=Config())
    first = c.next(w)
    backup = c.request(c.register()).map_task.attempt
    assert (backup.task, backup.attempt) == (first.task, 1)


def test_locality(tmp_path):
    where = {"0": ["host-a"], "1": ["host-b"], "2": ["zone-b"]}
    inputs = [write(tmp_path / str(i)) for i in range(3)]
    c = Coordinator(Server(Config(locate=lambda s: where[s.uri.rsplit("/", 1)[1]])))
    a, b = c.register("host-a", "zone-a"), c.register("host-b", "zone-b")
    job = c.submit(
        program="wordcount", inputs=inputs, output=str(tmp_path / "o"), num_reduce_tasks=1
    )
    for worker, want in [(b, 1), (b, 2), (a, 0)]:
        attempt = c.next(worker)
        assert attempt.task.index == want
        c.succeed(worker, attempt, outputs(want, 1))
    st = c.status(job)
    assert (st.map_tasks_host_local, st.map_tasks_zone_local) == (2, 1)


def test_locality_counts_only_accepted_attempts(tmp_path):
    c = Coordinator(Server(Config(locate=lambda s: ["host-a"])))
    far, near = c.register("host-b"), c.register("host-a")
    job = c.submit(
        program="wordcount", inputs=[write(tmp_path / "in")], output=str(tmp_path / "o"),
        num_reduce_tasks=1,
    )
    remote = c.next(far)  # remote attempt 0
    local = c.next(near)  # a backup, attempt 1, host-local
    assert c.succeed(near, local, outputs(0, 1))
    assert not c.succeed(far, remote, outputs(0, 1))
    assert c.status(job).map_tasks_host_local == 1


def test_locality_wait(new_job):
    wait = 0.05
    clock = [1000.0]
    cfg = Config(locality_wait=wait, locate=lambda s: ["host-a"], now=lambda: clock[0])
    c, _, _ = new_job(cfg=cfg)
    b = c.register("host-b")
    assert c.request(b).HasField("wait")
    clock[0] += wait / 2
    assert c.request(b).HasField("wait")
    clock[0] += wait / 2
    assert c.request(b).HasField("map_task")


def test_locality_wait_resets_on_a_local_start(tmp_path):
    clock = [1000.0]
    cfg = Config(locality_wait=10, locate=lambda s: ["host-a"], now=lambda: clock[0])
    c = Coordinator(Server(cfg))
    a, b = c.register("host-a"), c.register("host-b")
    c.submit(
        program="wordcount", inputs=[write(tmp_path / "x", b"a\n" * 3)],
        output=str(tmp_path / "o"), num_reduce_tasks=1, num_map_tasks=3,
    )
    assert c.request(b).HasField("wait")  # the wait starts
    clock[0] += 9
    assert c.request(a).HasField("map_task")  # a local start ends it
    clock[0] += 2
    assert c.request(b).HasField("wait")  # so a new one starts
    clock[0] += 10
    assert c.request(b).HasField("map_task")


def test_report_task(new_job):
    c, w, job = new_job()
    attempt = c.request(w).map_task.attempt
    out = outputs(0, 1)
    bad = coordinator_pb2.ReportTaskRequest(
        attempt=types_pb2.TaskAttemptId(task=types_pb2.TaskId(job_id="nope"))
    )
    assert code(c.s.ReportTask, bad) == grpc.StatusCode.NOT_FOUND
    with pytest.raises(Aborted) as e:
        c.report(w, attempt, success=types_pb2.TaskSuccess())
    assert e.value.code == grpc.StatusCode.INVALID_ARGUMENT  # no outputs
    assert e.value.details == "coordinator: map 0 reported 0 outputs, want 1"
    assert not c.succeed("other", attempt, out)  # from another worker: ignored

    for want in (True, False):  # the second report is a duplicate
        assert c.succeed(w, attempt, out) is want
    st = c.status(job)
    assert st.state == REDUCING and st.counters.values["n"] == 1

    reduce = c.request(w).reduce_task
    assert len(reduce.sources) == 1
    with pytest.raises(Aborted) as e:
        c.report(w, reduce.attempt)
    assert (e.value.code, e.value.details) == (
        grpc.StatusCode.INVALID_ARGUMENT,
        "report has neither success nor failure",
    )
    assert not c.fail(w, reduce.attempt, message="boom")
    assert c.request(w).HasField("reduce_task")  # the failed reduce task again
    assert code(c.s.GetJobStatus, coordinator_pb2.GetJobStatusRequest(job_id="nope")) == (
        grpc.StatusCode.NOT_FOUND
    )


def test_job_succeeds(new_job):
    c, w, job = new_job(cfg=Config(now=lambda: 1234.5))
    c.succeed(w, c.next(w), outputs(0, 1), {"maps": 2})
    c.succeed(w, c.next(w), (), {"reduces": 3})
    st = c.status(job)
    assert st.state == SUCCEEDED and (st.map_tasks_completed, st.reduce_tasks_completed) == (1, 1)
    assert dict(st.counters.values) == {"maps": 2, "reduces": 3}
    assert st.finished_at.ToNanoseconds() == 1234_500_000_000
    assert c.s._active is None and c.heartbeat(w).active_jobs.job_ids == []
    assert c.request(w).HasField("wait")


def test_job_end_cleans_up_output(new_job):
    c, w, job = new_job()
    tmp = path(c.s._jobs[job].spec.output) + "/" + TEMP_DIR
    c.succeed(w, c.next(w), outputs(0, 1))
    assert os.path.isdir(tmp)
    c.succeed(w, c.next(w))
    assert not os.path.exists(tmp)


def test_rerun_map_replaces_its_counts(new_job):
    c, w, job = new_job(cfg=Config(worker_timeout=60))
    c.succeed(w, c.next(w), outputs(0, 1), {"n": 5})
    c.s.expire(time.time() + 3600)
    w2 = c.register()
    c.succeed(w2, c.next(w2), outputs(0, 1), {"n": 7})
    assert c.status(job).counters.values["n"] == 7


def test_skip_records(new_job):
    c, w, _ = new_job()
    for _ in range(SKIP_AFTER):
        m = c.request(w).map_task
        assert list(m.skip_records) == []
        c.fail(w, m.attempt, failed_record=0)
    assert list(c.request(w).map_task.skip_records) == [0]


def test_skip_limit(new_job):
    c, w, job = new_job()
    c.s._jobs[job].spec.max_skipped_records = 1

    def crash(n):
        m = c.request(w).map_task
        c.fail(w, m.attempt, failed_record=n)
        return m

    crash(0)
    crash(0)
    crash(3)
    assert list(crash(3).skip_records) == [0]
    st = c.status(job)
    assert st.state == FAILED and st.error == "more than 1 bad records to skip"
    assert c.request(w).HasField("wait")


def test_skip_limit_zero(new_job):
    c, w, job = new_job()
    c.s._jobs[job].spec.max_skipped_records = 0
    for _ in range(SKIP_AFTER):
        c.fail(w, c.next(w), failed_record=4)
    assert c.status(job).error == "more than 0 bad records to skip"


class MemLog:
    """Keeps the records appended to it, and fails once err is set."""

    def __init__(self):
        self.records, self.err = [], None

    def append(self, b):
        if self.err:
            raise self.err
        self.records.append(log_pb2.LogRecord.FromString(b))

    def kinds(self):
        return [r.WhichOneof("record") for r in self.records]


def test_log(new_job):
    log = MemLog()
    c, w, job = new_job(cfg=Config(log=log, worker_timeout=60))
    c.succeed(w, c.next(w), outputs(0, 1))
    c.heartbeat(w)
    c.succeed(w, c.next(w))
    c.status(job)
    want = ["job_submitted", "task_assigned", "task_reported", "task_assigned", "task_reported"]
    assert log.kinds() == want
    ats = [r.at.ToNanoseconds() for r in log.records]
    assert all(ats) and ats == sorted(ats)
    sub = log.records[0].job_submitted
    assert sub.job_id == job and len(sub.splits) == 1 and sub.job.num_reduce_tasks == 1
    assert log.records[1].task_assigned.worker_id == w

    c, _, _ = new_job(cfg=Config(log=log, worker_timeout=60))
    assert log.kinds()[len(want) :] == want[:1]
    start = time.time()
    c.s.expire(start + 3600)
    assert log.kinds()[len(want) + 1 :] == ["worker_lost"]
    at = log.records[-1].at.ToNanoseconds() / 1e9
    assert start <= at <= time.time()  # the current time, not the expiry cutoff


def test_log_fails(new_job, tmp_path):
    log = MemLog()
    c, w, _ = new_job(cfg=Config(log=log))
    log.err = OSError("disk full")
    with pytest.raises(Aborted) as e:
        c.request(w)
    assert (e.value.code, e.value.details) == (
        grpc.StatusCode.UNAVAILABLE,
        "coordinator log: disk full",
    )
    attempt = types_pb2.TaskAttemptId(task=types_pb2.TaskId(job_id="job-1", kind=MAP))
    with pytest.raises(Aborted) as e:
        c.fail(w, attempt)
    assert e.value.code == grpc.StatusCode.UNAVAILABLE

    s = Server(Config(log=log))
    req = coordinator_pb2.SubmitJobRequest(
        job=types_pb2.JobSpec(
            program="wordcount", inputs=[write(tmp_path / "in", b"a\n")],
            output=str(tmp_path / "o"), num_reduce_tasks=1,
        )
    )
    assert code(s.SubmitJob, req) == grpc.StatusCode.UNAVAILABLE
    assert s._jobs == {}, "a job whose submission was not logged was kept"
    # A worker still expires, and its tasks still reset, when the log fails.
    c.s._cfg.worker_timeout = 60
    assert c.s.expire(time.time() + 3600) == [w]


def test_heartbeat(new_job):
    c, w, _ = new_job()
    assert not c.heartbeat(w).reregister
    assert c.heartbeat("nope").reregister


def test_heartbeat_active_jobs(new_job):
    c, w, job = new_job()
    assert list(c.heartbeat(w).active_jobs.job_ids) == [job]
    end_active(c.s)
    resp = c.heartbeat(w)
    assert resp.HasField("active_jobs") and list(resp.active_jobs.job_ids) == []
    assert not c.heartbeat("nope").HasField("active_jobs")


def test_keep_jobs(new_job, tmp_path):
    c, _, first = new_job(cfg=Config(keep_jobs=2))
    s = c.s
    inp = write(tmp_path / "in")
    ids = [first]
    for i in range(3):
        end_active(s)
        ids.append(
            c.submit(
                program="wordcount", inputs=[inp], output=str(tmp_path / f"o{i}"),
                num_reduce_tasks=1,
            )
        )
    assert ids == ["job-1", "job-2", "job-3", "job-4"]

    def kept():
        out = []
        for id in ids:
            try:
                c.status(id)
                out.append(id)
            except Aborted as e:
                assert e.code == grpc.StatusCode.NOT_FOUND
        return out

    assert kept() == ids[1:]
    end_active(s)
    assert len(s._jobs) == 2 and len(s._order) == 2
    assert kept() == ids[2:]


def test_keep_jobs_zero_keeps_all(new_job, tmp_path):
    c, _, _ = new_job()
    for i in range(3):
        end_active(c.s)
        c.submit(
            program="wordcount", inputs=[write(tmp_path / "in")], output=str(tmp_path / f"o{i}"),
            num_reduce_tasks=1,
        )
    assert len(c.s._jobs) == 4


def test_paused_task_request(new_job):
    c, w, _ = new_job()
    assert c.request(w, paused=True).HasField("wait")
    resp = c.request(w)
    assert resp.HasField("map_task") and resp.map_task.attempt.attempt == 0
    c.s.shutdown()
    assert c.request(w, paused=True).HasField("shutdown")


@pytest.mark.parametrize("keep", [1, 2])
def test_keep_jobs_after_completion(new_job, tmp_path, keep):
    c, w, first = new_job(cfg=Config(keep_jobs=keep))
    inp = write(tmp_path / "in", b"a\n")
    for n in range(1, keep + 2):
        assert c.succeed(w, c.next(w), outputs(0, 1)) and c.succeed(w, c.next(w))
        if n <= keep:
            c.submit(
                program="wordcount", inputs=[inp], output=str(tmp_path / f"o{n}"),
                num_reduce_tasks=1,
            )
    for n in range(2, keep + 2):
        assert c.status(f"job-{n}").state == SUCCEEDED
    with pytest.raises(Aborted):
        c.status(first)


def test_heartbeat_cancels(new_job):
    c, w, _ = new_job()
    other = c.register()
    attempt = c.request(w).map_task.attempt
    stale = types_pb2.TaskAttemptId()
    stale.CopyFrom(attempt)
    stale.task.job_id = "job-0"
    assert len(c.heartbeat(w, attempt).cancel) == 0
    assert list(c.heartbeat(other, attempt, stale).cancel) == [attempt, stale]
    c.succeed(w, attempt, outputs(0, 1))
    assert len(c.heartbeat(w, attempt).cancel) == 1


def test_heartbeat_cancels_an_ended_jobs_attempts(new_job):
    c, w, _ = new_job()
    attempt = c.next(w)
    end_active(c.s, "stopped")
    assert list(c.heartbeat(w, attempt).cancel) == [attempt]


def test_expire(new_job):
    c, w, job = new_job()
    c.s._cfg.worker_timeout = 0
    assert c.s.expire(time.time() + 3600) == []
    c.s._cfg.worker_timeout = 60
    c.succeed(w, c.request(w).map_task.attempt, outputs(0, 1))
    assert c.s.expire(time.time()) == []
    assert c.s.expire(time.time() + 120) == [w]
    assert c.status(job).map_tasks_completed == 0  # the lost map output is redone
    lost = c.s._lost[-1]
    assert lost.id == w and [str(t) for t in lost.tasks] == ["TASK_KIND_MAP 0"]
    assert code(c.s.RequestTask, coordinator_pb2.RequestTaskRequest(worker_id=w)) == (
        grpc.StatusCode.NOT_FOUND
    )
    assert c.heartbeat(w).reregister
    assert c.request(c.register()).map_task.attempt.attempt == 1


def test_expire_while_idle_remembers_the_loss(new_job):
    c, w, _ = new_job(cfg=Config(worker_timeout=1))
    end_active(c.s)
    assert c.s.expire(time.time() + 10) == [w]
    assert c.s._lost[-1].tasks == []


def test_lost_workers_are_bounded():
    c = Coordinator(Server(Config(worker_timeout=1)))
    ids = [c.register() for _ in range(MAX_LOST + 5)]
    c.s.expire(time.time() + 10)
    assert [lw.id for lw in c.s._lost] == ids[5:]


def test_monitor(new_job):
    c, w, _ = new_job(cfg=Config(heartbeat_interval=0.001, worker_timeout=0.001))
    stop = threading.Event()
    t = threading.Thread(target=c.s.monitor, args=(stop,))
    t.start()
    deadline = time.time() + 10
    while not c.heartbeat(w).reregister:
        assert time.time() < deadline, "the monitor never expired the worker"
        time.sleep(0.005)
    stop.set()
    t.join(5)
    assert not t.is_alive()


def test_monitor_returns_with_no_timeout():
    Server(Config(worker_timeout=0)).monitor(threading.Event())  # returns at once
    Server(Config(heartbeat_interval=0)).monitor(threading.Event())


@pytest.mark.parametrize(
    ("job", "want"),
    [
        ({"num_reduce_tasks": MAX_REDUCE_TASKS + 1}, "reduce tasks R"),
        ({"num_reduce_tasks": 1, "num_map_tasks": MAX_SPLITS + 1}, "map tasks M"),
    ],
)
def test_submit_job_task_count_limits(tmp_path, job, want):
    req = coordinator_pb2.SubmitJobRequest(
        job=types_pb2.JobSpec(
            program="wordcount", inputs=[write(tmp_path / "in")], output=str(tmp_path / "o"), **job
        )
    )
    with pytest.raises(Aborted) as e:
        Server().SubmitJob(req, CTX)
    assert e.value.code == grpc.StatusCode.INVALID_ARGUMENT and want in e.value.details


def test_submit_job_rejects_too_many_splits(tmp_path):
    big = tmp_path / "big.txt"
    with open(big, "wb") as f:
        f.truncate(MAX_SPLITS + 1)
    req = coordinator_pb2.SubmitJobRequest(
        job=types_pb2.JobSpec(
            program="wordcount", inputs=[str(big)], output=str(tmp_path / "o"),
            num_reduce_tasks=1, split_size_bytes=1,
        )
    )
    with pytest.raises(Aborted) as e:
        Server().SubmitJob(req, CTX)
    assert e.value.code == grpc.StatusCode.INVALID_ARGUMENT
    assert "raise the split size" in e.value.details


def test_job_file_is_accepted(tmp_path):
    """The Python coordinator cannot load a job file; it passes it to the
    workers, and one that does not load fails there (#198)."""
    c = Coordinator(Server())
    w = c.register()
    job = c.submit(
        program="bad.go", program_source="package job\nfunc Map(",
        inputs=[write(tmp_path / "in")], output=str(tmp_path / "o"), num_reduce_tasks=1,
    )
    m = c.request(w).map_task
    assert m.job.program_source.startswith("package job")
    c.fail(w, m.attempt, message="job file: undefined: Map", fatal=True)
    st = c.status(job)
    assert st.state == FAILED and st.error == "TASK_KIND_MAP 0 cannot run: job file: undefined: Map"


# failure_test.go


def test_fatal_failure_fails_job(new_job):
    c, w, job = new_job()
    c.fail(w, c.next(w), message="job file: undefined: Map", fatal=True)
    st = c.status(job)
    assert st.state == FAILED and "cannot run: job file: undefined: Map" in st.error


def test_stale_fatal_failure_changes_nothing(new_job):
    c, w, job = new_job()
    attempt = c.next(w)
    c.fail("other", attempt, message="x", fatal=True)
    assert c.status(job).state == MAPPING


def test_max_task_failures(new_job):
    c, w, job = new_job(cfg=Config(max_task_failures=3))
    for msg in ("boom a", "boom b"):
        c.fail(w, c.next(w), message=msg)
    assert c.status(job).state != FAILED
    c.fail(w, c.next(w), message="boom c")
    st = c.status(job)
    assert st.state == FAILED and st.error == "TASK_KIND_MAP 0 failed 3 times; the last error: boom c"

    c, w, job = new_job(cfg=Config(max_task_failures=3))
    outs = outputs(0, 1)
    outs[0].worker_address = "gone:1"
    c.succeed(w, c.next(w), outs)
    for _ in range(5):  # the map output is lost each time; the map task runs again
        c.fail(w, c.next(w), message="fetch", lost_output=outs[0])
        assert c.status(job).state != FAILED
        c.succeed(w, c.next(w), outs)

    c, w, job = new_job()  # no limit
    for _ in range(20):
        c.fail(w, c.next(w), message="boom")
    assert c.status(job).state != FAILED


def test_max_task_failures_keeps_skipping(new_job):
    c, w, job = new_job(cfg=Config(max_task_failures=SKIP_AFTER + 1))
    for _ in range(SKIP_AFTER):
        c.fail(w, c.next(w), message="panic", failed_record=7)
    m = c.request(w).map_task
    assert list(m.skip_records) == [7]
    c.succeed(w, m.attempt, outputs(0, 1))
    c.succeed(w, c.next(w))
    assert c.status(job).state == SUCCEEDED


def test_backup_status(new_job):
    c, w, job = new_job()
    first = c.next(w)
    other = c.register()
    backup = c.next(other)
    assert backup.attempt == 1
    assert c.succeed(other, backup, outputs(0, 1)) and not c.succeed(w, first, outputs(0, 1))
    st = c.status(job)
    assert (st.backup_attempts, st.backup_wins) == (1, 1)


# wire.py


def test_path():
    assert path("/a/b") == "/a/b"
    assert path("rel/x") == "rel/x"
    assert path("file:///a/b%20c") == "/a/b c"
    assert path("file://localhost/a") == "/a"
    with pytest.raises(ValueError, match=r'^wire: "file://host/a" names a remote host$'):
        path("file://host/a")
    with pytest.raises(ValueError, match="stores only local files$"):
        path("s3://bucket/a")


def test_uri(tmp_path, monkeypatch):
    monkeypatch.chdir(tmp_path)
    assert uri("a b") == "file://" + str(tmp_path).replace(" ", "%20") + "/a%20b"
    assert path(uri("a b")) == str(tmp_path / "a b")
    s = input_split(Split("x", 3, 10))
    assert (s.uri, s.offset, s.length) == (uri("x"), 3, 7)


def test_validate():
    ok = types_pb2.JobSpec(program="grep", inputs=["/i"], output="/o", num_reduce_tasks=1)
    assert validate(ok) == []
    assert validate(types_pb2.JobSpec(program_source="x", inputs=["i"], output="o",
                                      num_reduce_tasks=1)) == []

    def errs(**changes):
        m = types_pb2.JobSpec()
        m.CopyFrom(ok)
        for k, v in changes.items():
            if isinstance(v, list):
                del getattr(m, k)[:]
                getattr(m, k).extend(v)
            else:
                setattr(m, k, v)
        return validate(m)

    assert errs(program="nope") == ['wire: unknown program "nope"']
    assert validate(ok, frozenset(["wc"])) == ['wire: unknown program "grep"']
    assert errs(partitioner="range") == ['wire: unknown partitioner "range"']
    assert errs(partitioner="hash") == []
    assert errs(inputs=["s3://b/x"]) == ['wire: "s3://b/x": the Python coordinator stores only local files']
    assert errs(output="file://h/o") == ['wire: "file://h/o" names a remote host']
    assert errs(inputs=[]) == ["no Inputs"]
    assert errs(output="") == ["no Output"]
    assert errs(num_reduce_tasks=0) == ["number of reduce tasks R must be at least 1"]
    assert errs(num_reduce_tasks=MAX_REDUCE_TASKS) == []
    assert errs(num_reduce_tasks=MAX_REDUCE_TASKS + 1) == [
        f"number of reduce tasks R is {MAX_REDUCE_TASKS + 1}; the most is {MAX_REDUCE_TASKS}"
    ]
    assert errs(num_map_tasks=MAX_SPLITS) == []
    assert errs(num_map_tasks=MAX_SPLITS + 1) == [
        f"number of map tasks M is {MAX_SPLITS + 1}; the most is {MAX_SPLITS}"
    ]
    for f in ("text", "kv"):
        assert errs(input_format=f) == errs(output_format=f) == []
    assert errs(input_format="csv") == ['formats: unknown format "csv"']
    for codec in ("none", "zstd", "snappy"):
        assert errs(intermediate_codec=codec) == []
    assert errs(intermediate_codec="lz4") == [
        'intermediate codec: mrkv: unsupported_codec: "lz4"'
    ]
    assert errs(output_codec="lz4", output_format="csv") == [
        'output codec: mrkv: unsupported_codec: "lz4"'
    ]
    assert errs(output_format="csv") == ['formats: unknown format "csv"']
    assert errs(output_codec="zstd") == [
        'formats: output format "text" takes no codec; only kv does'
    ]
    assert errs(output_codec="zstd", output_format="text") == [
        'formats: output format "text" takes no codec; only kv does'
    ]
    assert errs(output_codec="none") == errs(output_codec="zstd", output_format="kv") == []
    assert errs(program='a"b\\') == ['wire: unknown program "a\\"b\\\\"']
    assert len(validate(types_pb2.JobSpec())) == 4


# A real gRPC server.


def test_serve(tmp_path):
    server, coordinator, port, stop = serve("127.0.0.1:0", Config(heartbeat_interval=0.5))
    try:
        with grpc.insecure_channel(f"127.0.0.1:{port}") as ch:
            stub = coordinator_pb2_grpc.CoordinatorServiceStub(ch)
            reg = stub.RegisterWorker(
                coordinator_pb2.RegisterWorkerRequest(worker=types_pb2.WorkerInfo())
            )
            assert reg.heartbeat_interval.ToMilliseconds() == 500
            job = stub.SubmitJob(
                coordinator_pb2.SubmitJobRequest(
                    job=types_pb2.JobSpec(
                        program="wordcount", inputs=[write(tmp_path / "in")],
                        output=str(tmp_path / "o"), num_reduce_tasks=1,
                    )
                )
            ).job_id
            m = stub.RequestTask(coordinator_pb2.RequestTaskRequest(worker_id=reg.worker_id))
            rep = stub.ReportTask(
                coordinator_pb2.ReportTaskRequest(
                    worker_id=reg.worker_id,
                    attempt=m.map_task.attempt,
                    success=types_pb2.TaskSuccess(outputs=outputs(0, 1)),
                )
            )
            assert rep.accepted
            st = stub.GetJobStatus(coordinator_pb2.GetJobStatusRequest(job_id=job))
            assert st.state == REDUCING
            with pytest.raises(grpc.RpcError) as e:
                stub.GetJobStatus(coordinator_pb2.GetJobStatusRequest(job_id="nope"))
            assert e.value.code() == grpc.StatusCode.NOT_FOUND
            assert e.value.details() == 'unknown job "nope"'
    finally:
        stop.set()
        server.stop(None)
