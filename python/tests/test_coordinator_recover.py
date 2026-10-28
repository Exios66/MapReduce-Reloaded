"""Tests for restarting the coordinator from its write-ahead log, ported
from go/internal/coordinator/recover_test.go."""

import os
import time

import pytest
from test_coordinator_server import (
    CTX,
    MAPPING,
    REDUCING,
    SUCCEEDED,
    Coordinator,
    code,
    outputs,
    write,
)

from mapreduce_reloaded.coordinator.recover import recover
from mapreduce_reloaded.coordinator.server import HEAL_EVERY, TEMP_DIR, Config, RpcError, Server
from mapreduce_reloaded.gen.mapreduce.v1 import coordinator_pb2, log_pb2, types_pb2
from mapreduce_reloaded.wal import Log


@pytest.fixture
def restart(tmp_path):
    """Returns a function that recovers a coordinator from one log, closing
    each log when the test ends."""
    path = str(tmp_path / "coordinator.log")
    logs = []

    def start(cfg=None):
        s, log = recover(cfg or Config(), path)
        logs.append(log)
        return Coordinator(s)

    start.path = path
    yield start
    for log in logs:
        log.close()


def job_ids(c):
    return {j for j in c.s._jobs}


def test_recover(restart, tmp_path):
    """A restarted coordinator resumes its job: completed tasks stay
    completed, running attempts are given up, and attempt numbers are never
    reused. The job keeps the times it was submitted and finished."""
    inp = write(tmp_path / "in.txt")
    c = restart()
    old = c.register()
    job = c.submit(
        program="wordcount", inputs=[inp], output=str(tmp_path / "o"), num_reduce_tasks=2
    )
    submitted = c.status(job).submitted_at
    assert c.status(job).HasField("submitted_at") and not c.status(job).HasField("finished_at")
    c.succeed(old, c.next(old), outputs(0, 2))
    c.succeed(old, c.next(old))  # reduce 0
    running = c.next(old)  # reduce 1, lost in the crash

    c = restart()
    st = c.status(job)
    assert (st.map_tasks_completed, st.reduce_tasks_completed, st.counters.values["n"]) == (1, 1, 2)
    assert st.submitted_at == submitted and not st.HasField("finished_at")
    w = c.register()
    assert w != old
    retry = c.next(w)
    assert (retry.task.index, retry.attempt) == (1, running.attempt + 1)
    assert not c.succeed(old, running)  # a report from before the restart
    assert c.succeed(w, retry) and c.status(job).state == SUCCEEDED
    finished = c.status(job).finished_at
    assert finished.ToNanoseconds() >= submitted.ToNanoseconds()

    c = restart()  # replaying a finished job, and a restart, again
    st = c.status(job)
    assert st.state == SUCCEEDED and st.counters.values["n"] == 3
    assert st.submitted_at == submitted and st.finished_at == finished
    assert c.register() == "r3-w1"


def test_recover_backup(restart, tmp_path):
    """A backup attempt is logged like any other, and replays whether or not
    the restarted coordinator makes backups itself."""
    inp = write(tmp_path / "in.txt")
    c = restart(Config(backups=True))
    w1, w2 = c.register(), c.register()
    job = c.submit(
        program="wordcount", inputs=[inp], output=str(tmp_path / "o"), num_reduce_tasks=1
    )
    original = c.next(w1)
    backup = c.next(w2)
    assert (backup.task.index, backup.attempt) == (0, 1)
    assert c.succeed(w2, backup, outputs(0, 1))

    c = restart(Config(backups=False))
    st = c.status(job)
    assert (st.map_tasks_completed, st.backup_attempts, st.backup_wins) == (1, 1, 1)
    assert not c.succeed(w1, original, outputs(0, 1))


def test_replay_without_times():
    """A log written before records had times still replays; its job has no
    submit or finish time."""
    s = Server()
    s._replay(
        log_pb2.LogRecord(
            job_submitted=log_pb2.JobSubmitted(
                job_id="job-1",
                job=types_pb2.JobSpec(num_reduce_tasks=1),
                splits=[types_pb2.InputSplit()],
            )
        )
    )
    st = Coordinator(s).status("job-1")
    assert not st.HasField("submitted_at") and not st.HasField("finished_at")


def test_times_survive_the_log(restart, tmp_path):
    """Times are logged to the microsecond, so a replayed time reads back
    exactly, whatever float the clock gave."""
    inp = write(tmp_path / "in.txt")
    cfg = Config(now=lambda: 1_700_000_000.123456789)
    c = restart(cfg)
    job = c.submit(
        program="wordcount", inputs=[inp], output=str(tmp_path / "o"), num_reduce_tasks=1
    )
    before = c.status(job).submitted_at
    assert before.ToNanoseconds() == 1_700_000_000_123_457_000
    assert restart(cfg).status(job).submitted_at == before


@pytest.mark.parametrize(
    "rec, match",
    [
        (log_pb2.LogRecord(), "unknown record"),
        (
            log_pb2.LogRecord(task_assigned=log_pb2.TaskAssigned()),
            "assigned outside the running job",
        ),
        (
            log_pb2.LogRecord(task_reported=coordinator_pb2.ReportTaskRequest()),
            'report for unknown job ""',
        ),
    ],
)
def test_replay_rejects_bad_records(rec, match):
    with pytest.raises(ValueError, match=match):
        Server()._replay(rec)


def test_replay_rejects_inconsistent_records():
    s = Server()
    submitted = log_pb2.JobSubmitted(
        job_id="job-1", job=types_pb2.JobSpec(num_reduce_tasks=1), splits=[types_pb2.InputSplit()]
    )
    s._replay(log_pb2.LogRecord(job_submitted=submitted))
    task = types_pb2.TaskId(job_id="job-1", kind=types_pb2.TASK_KIND_MAP, index=0)
    skipped = types_pb2.TaskAttemptId(task=task, attempt=1)  # attempt 0 was never logged
    with pytest.raises(ValueError, match="replayed as attempt 0"):
        s._replay(log_pb2.LogRecord(task_assigned=log_pb2.TaskAssigned(attempt=skipped)))
    bad = types_pb2.TaskAttemptId(task=types_pb2.TaskId(job_id="job-1", index=9))
    with pytest.raises(ValueError, match="coordinator log: assigning"):
        s._replay(log_pb2.LogRecord(task_assigned=log_pb2.TaskAssigned(attempt=bad)))
    report = coordinator_pb2.ReportTaskRequest(attempt=bad, success=coordinator_pb2.TaskSuccess())
    with pytest.raises(ValueError, match="^coordinator log: "):
        s._replay(log_pb2.LogRecord(task_reported=report))


def test_a_bad_log_fails_recovery_and_is_closed(restart, monkeypatch):
    with open(restart.path, "wb") as f:  # one valid record: a report for no job
        Log(restart.path, f)._write(
            log_pb2.LogRecord(task_reported=coordinator_pb2.ReportTaskRequest()).SerializeToString()
        )
    with pytest.raises(ValueError, match="report for unknown job"):
        restart()
    os.remove(restart.path)
    closed = []
    real = Log.close
    monkeypatch.setattr(Log, "close", lambda log: closed.append(1) or real(log))
    monkeypatch.setattr(Log, "append", lambda log, b: (_ for _ in ()).throw(OSError("full")))
    with pytest.raises(RpcError, match="coordinator log: full"):
        restart()
    assert closed == [1]


def test_compact_log(restart, tmp_path):
    """The log keeps only the jobs the coordinator keeps (#178): after many
    jobs its size follows keep_jobs, and a restart recovers the kept jobs
    and the running one, forgets the rest, never reuses a job ID, and still
    numbers workers past every restart."""
    inp = write(tmp_path / "in.txt")
    cfg = Config(keep_jobs=2)
    c = restart(cfg)
    w = c.register()
    outs = iter(range(1000))

    def submit():
        out = str(tmp_path / f"o{next(outs)}")
        return c.submit(program="wordcount", inputs=[inp], output=out, num_reduce_tasks=1)

    def run():
        job = submit()
        c.succeed(w, c.next(w), outputs(0, 1))
        c.succeed(w, c.next(w))
        assert c.status(job).state == SUCCEEDED
        return job

    for _ in range(5):
        run()
    after5 = os.path.getsize(restart.path)
    ended = [run() for _ in range(20)]
    assert os.path.getsize(restart.path) <= after5 + after5 // 4
    running = submit()
    c.succeed(w, c.next(w), outputs(0, 1))  # its map task is done

    c = restart(cfg)
    for job in ended[:-2]:
        assert (
            code(c.s.GetJobStatus, coordinator_pb2.GetJobStatusRequest(job_id=job)).name
            == "NOT_FOUND"
        )
    for job in ended[-2:]:
        assert c.status(job).state == SUCCEEDED
    st = c.status(running)
    assert st.state == REDUCING and st.map_tasks_completed == 1
    w = c.register()
    assert w == "r2-w1"
    c.succeed(w, c.next(w))
    assert run() == "job-27"
    c = restart(cfg)
    assert c.register() == "r3-w1"


def test_compaction_failure_is_logged(caplog):
    class Failing:
        def append(self, b):
            pass

        def compact(self, keep):
            raise OSError("disk full")

    s = Server(Config(log=Failing(), keep_jobs=1))
    s._compact()
    assert "compacting the coordinator log: disk full" in caplog.text


def test_compact_keeps_what_it_does_not_understand():
    kept = []

    class Recording:
        def append(self, b):
            pass

        def compact(self, keep):
            for rec in [
                log_pb2.LogRecord(restarted=log_pb2.Restarted()),
                log_pb2.LogRecord(job_submitted=log_pb2.JobSubmitted(job_id="job-9")),
                log_pb2.LogRecord(worker_lost=log_pb2.WorkerLost(worker_id="w1")),
            ]:
                kept.append(keep(rec.SerializeToString()))

    Server(Config(log=Recording(), keep_jobs=1))._compact()
    assert kept == [True, False, False]  # job-9 is not kept, nor its lost worker


def test_cleanup_after_log(restart, tmp_path):
    """A job's output is cleaned up only once the log holds the report that
    ended the job. A coordinator that dies before logging it comes back to a
    running job whose reduce attempts still have the job's TempDir; one that
    dies after logging it, but before cleaning up, cleans up as it replays
    (#182)."""
    inp = write(tmp_path / "in.txt")
    out = tmp_path / "out"
    temp = out / TEMP_DIR
    c = restart()
    w = c.register()
    job = c.submit(program="wordcount", inputs=[inp], output=str(out), num_reduce_tasks=1)
    c.succeed(w, c.next(w), outputs(0, 1))
    last = c.next(w)  # reduce 0, whose report the log never holds
    c.s._cfg.log.close()
    req = coordinator_pb2.ReportTaskRequest(
        worker_id=w, attempt=last, success=coordinator_pb2.TaskSuccess()
    )
    assert code(c.s.ReportTask, req).name == "UNAVAILABLE"
    assert temp.is_dir()  # not cleaned up before the log held the job's end

    c = restart()
    assert c.status(job).state == REDUCING
    w = c.register()
    assert c.succeed(w, c.next(w)) and c.status(job).state == SUCCEEDED
    assert not temp.exists()

    temp.mkdir()  # as if the coordinator died before cleaning up
    restart()
    assert not temp.exists()


def test_a_failed_append_stops_the_coordinator_until_the_log_works(restart, tmp_path):
    """A report that ends the job but cannot be logged leaves the job ended
    in memory only: no RPC may reveal that, nor may the output be cleaned
    up. Once the log can be reopened, the server rebuilds itself from it,
    as a restart would, without anyone restarting it."""
    inp = write(tmp_path / "in.txt")
    out = tmp_path / "out"
    wall, clock = [1000.0], [0.0]
    c = restart(Config(now=lambda: wall[0], clock=lambda: clock[0]))
    w = c.register()
    job = c.submit(program="wordcount", inputs=[inp], output=str(out), num_reduce_tasks=1)
    c.succeed(w, c.next(w), outputs(0, 1))
    last = c.next(w)
    # The log fails, and cannot be reopened either, as on a full disk.
    c.s._cfg.log.close()
    os.rename(restart.path, restart.path + ".away")
    os.mkdir(restart.path)
    req = coordinator_pb2.ReportTaskRequest(
        worker_id=w, attempt=last, success=coordinator_pb2.TaskSuccess()
    )
    assert code(c.s.ReportTask, req).name == "UNAVAILABLE"
    for round in range(2):  # the second retries reopening the log, and fails
        if round:
            clock[0] += HEAL_EVERY
        for rpc, msg in [
            (c.s.GetJobStatus, coordinator_pb2.GetJobStatusRequest(job_id=job)),
            (c.s.RegisterWorker, coordinator_pb2.RegisterWorkerRequest()),
            (c.s.Heartbeat, coordinator_pb2.HeartbeatRequest(worker_id=w)),
            (c.s.RequestTask, coordinator_pb2.RequestTaskRequest(worker_id=w)),
            (c.s.ReportTask, req),
            (c.s.SubmitJob, coordinator_pb2.SubmitJobRequest(job=types_pb2.JobSpec())),
        ]:
            assert code(rpc, msg).name == "UNAVAILABLE", rpc
    assert (out / TEMP_DIR).is_dir()
    # Space is freed. The server retries no more than once a HEAL_EVERY, on
    # the monotonic clock: setting the wall clock back does not delay it.
    wall[0] -= 86400
    os.rmdir(restart.path)
    os.rename(restart.path + ".away", restart.path)
    assert code(c.s.GetJobStatus, coordinator_pb2.GetJobStatusRequest(job_id=job)).name == (
        "UNAVAILABLE"
    )
    clock[0] += HEAL_EVERY
    try:
        assert c.status(job).state == REDUCING
        assert c.heartbeat(w).reregister  # workers register again, as after a restart
        w = c.register()
        assert c.succeed(w, c.next(w))
        assert c.status(job).state == SUCCEEDED
    finally:
        c.s.close()
    assert restart().status(job).state == SUCCEEDED  # the healed server logs again


def test_close_without_a_log():
    Server().close()


def test_replay_keeps_reused_output(restart, tmp_path):
    """A job may write to the output of an ended job whose files were
    deleted. Replaying the old job's end must not remove the running job's
    TempDir."""
    inp = write(tmp_path / "in.txt")
    out = tmp_path / "out"
    c = restart()
    w = c.register()
    first = c.submit(program="wordcount", inputs=[inp], output=str(out), num_reduce_tasks=1)
    c.succeed(w, c.next(w), outputs(0, 1))
    c.succeed(w, c.next(w))
    assert c.status(first).state == SUCCEEDED
    for f in out.glob("part-r-*"):
        f.unlink()
    second = c.submit(program="wordcount", inputs=[inp], output=str(out), num_reduce_tasks=1)

    c = restart()
    assert (out / TEMP_DIR).is_dir()
    assert c.status(second).state == MAPPING


@pytest.mark.parametrize("keep", [0, 1, 2])
def test_recover_job_retention(restart, tmp_path, keep):
    """Eviction must replay consistently, and restarting must not reuse IDs
    belonging to forgotten jobs. Zero retention means keep all jobs."""
    inp = write(tmp_path / "in.txt", b"a\n")
    c = restart(Config(keep_jobs=keep))
    w = c.register()
    outs = iter(range(100))

    def submit():
        out = str(tmp_path / f"o{next(outs)}")
        return c.submit(program="wordcount", inputs=[inp], output=out, num_reduce_tasks=1)

    for n in range(1, 5):
        assert submit() == f"job-{n}"
        if n < 4:
            assert c.succeed(w, c.next(w), outputs(0, 1)) and c.succeed(w, c.next(w))

    def check_retained():
        for n in range(1, 5):
            req = coordinator_pb2.GetJobStatusRequest(job_id=f"job-{n}")
            if keep > 0 and n < 4 - keep:
                assert code(c.s.GetJobStatus, req).name == "NOT_FOUND"
            else:
                c.s.GetJobStatus(req, CTX)

    check_retained()
    c = restart(Config(keep_jobs=keep))
    check_retained()
    w = c.register()
    assert list(c.heartbeat(w).active_jobs.job_ids) == ["job-4"]
    assert c.succeed(w, c.next(w), outputs(0, 1)) and c.succeed(w, c.next(w))
    assert submit() == "job-5"


def test_recover_preserves_job_source(restart, tmp_path):
    inp = write(tmp_path / "in.txt", b"a\n")
    c = restart()
    spec = types_pb2.JobSpec(
        program="custom.go",
        program_source="package job\n",
        inputs=[inp],
        output=str(tmp_path / "o"),
        num_reduce_tasks=1,
        use_combiner=True,
        params={"label": "recovered"},
    )
    c.s.SubmitJob(coordinator_pb2.SubmitJobRequest(job=spec), CTX)
    c = restart()
    resp = c.request(c.register())
    got = resp.map_task.job
    assert got.program_source == spec.program_source and got.params == spec.params
    assert got.use_combiner


def test_recover_does_not_change_the_callers_config(restart):
    cfg = Config()
    restart(cfg)
    assert cfg.log is None


def test_recover_replays_a_lost_worker(restart, tmp_path):
    """A worker lost while its job ran takes its completed map output with
    it, after a restart as before one (§3.3)."""
    inp = write(tmp_path / "in.txt")
    cfg = Config(worker_timeout=10)
    c = restart(cfg)
    w = c.register()
    job = c.submit(
        program="wordcount", inputs=[inp], output=str(tmp_path / "o"), num_reduce_tasks=1
    )
    c.succeed(w, c.next(w), outputs(0, 1))
    assert c.s.expire(time.monotonic() + 3600) == [w]
    assert c.status(job).map_tasks_completed == 0
    assert restart(cfg).status(job).map_tasks_completed == 0
