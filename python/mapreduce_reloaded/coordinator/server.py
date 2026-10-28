"""The coordinator's gRPC service, mirroring go/internal/coordinator/server.go.

It runs one job at a time; a job submitted while another runs is rejected.
All state is guarded by one lock (ADR 0006). Nothing slow runs under it:
SubmitJob plans the input splits before locking.
"""

import glob
import logging
import os
import shutil
import threading
import time
from collections.abc import Callable, Iterator
from concurrent import futures
from contextlib import contextmanager, suppress
from dataclasses import dataclass, field, replace
from typing import NamedTuple, Protocol

import grpc

from mapreduce_reloaded.coordinator.registry import Registry
from mapreduce_reloaded.coordinator.tasktable import COMPLETED, MAP, Task, TaskTable
from mapreduce_reloaded.coordinator.wire import PROGRAMS, input_split, path, validate
from mapreduce_reloaded.formats import input_format
from mapreduce_reloaded.gen.mapreduce.v1 import (
    coordinator_pb2,
    coordinator_pb2_grpc,
    log_pb2,
    types_pb2,
)
from mapreduce_reloaded.split import default_r, plan, plan_m

log = logging.getLogger(__name__)

# How often, in seconds, a server whose log has failed tries to rebuild
# itself from the log.
HEAL_EVERY = 1.0

# The directory under a job's output that holds attempts' uncommitted files,
# as go/internal/task.TempDir.
TEMP_DIR = "_temporary"

# How many lost workers the coordinator remembers, for the status page.
MAX_LOST = 100


class Log(Protocol):
    """Where a coordinator records its decisions. After a failed append it
    must keep failing, so that a coordinator whose log has fallen behind its
    memory accepts no more changes."""

    def append(self, record: bytes) -> None:
        """Appends a serialized LogRecord, raising an exception on failure."""
        ...


@dataclass
class Config:
    """Configures a Server. Durations are in seconds."""

    # How often workers must send heartbeats.
    heartbeat_interval: float = 3.0
    # How long a worker may go unheard before it is declared dead (§3.3).
    # Zero means never.
    worker_timeout: float = 10.0
    # How long a worker with nothing to do waits before asking again.
    retry_after: float = 1.0
    # Backup attempts: once no task of the current phase is idle, a worker
    # that asks for work gets a second attempt of a task still running
    # elsewhere (§3.6).
    backups: bool = True
    # Returns the hosts or zones that hold a split, which map tasks then
    # prefer to run on (§3.4). It runs once per split, when the job is
    # submitted.
    locate: Callable[[types_pb2.InputSplit], list[str]] | None = None
    # How long the job may hold back its map tasks from workers not on a
    # host that holds their split (delay scheduling). Zero means never.
    locality_wait: float = 0.0
    # The write-ahead log: every change to a job or its tasks is appended to
    # it before any reply depends on it (§3.3).
    log: Log | None = None
    # How many ended jobs stay in memory for GetJobStatus; older ones are
    # forgotten, oldest first. Zero means all of them.
    keep_jobs: int = 0
    # Fails the job once one task has failed this many times, with the last
    # error (#176). Attempts that could not fetch lost map output do not
    # count. Zero means no limit.
    max_task_failures: int = 0
    # Replaces time.time as the coordinator's clock, which dates its
    # decisions, for tests.
    now: Callable[[], float] | None = None
    # Replaces time.monotonic, which times heartbeats and locality waits, so
    # that setting the system clock neither kills live workers nor keeps
    # dead ones, for tests.
    clock: Callable[[], float] | None = None
    # The built-in programs the workers have.
    programs: frozenset = PROGRAMS
    # Begins every worker and job ID the server gives out. A coordinator
    # without a log numbers from 1 again when restarted, so serve sets a
    # prefix of its own: a worker still holding the last run's IDs, or files
    # under a job's ID, must not take them for this run's. recover ignores
    # it, as the log keeps IDs unique. Last, so positional arguments keep
    # the meaning they had before it was added.
    id_prefix: str = ""


class RpcError(Exception):
    """An RPC's failure: a gRPC status code and a message."""

    def __init__(self, code: grpc.StatusCode, message: str):
        super().__init__(message)
        self.code = code


class LostWorker(NamedTuple):
    """A worker declared dead, when, and the tasks that became idle."""

    id: str
    at: float
    tasks: list[Task]


@dataclass(eq=False)
class _Job:
    id: str
    spec: types_pb2.JobSpec  # sent with every task
    splits: list[types_pb2.InputSplit]
    tasks: TaskTable
    submitted: float | None
    counts: dict = field(default_factory=dict)  # of each task's accepted attempt
    placed: dict = field(default_factory=dict)  # locality of each attempt, by number
    ran: dict = field(default_factory=dict)  # locality of each task's accepted attempt
    since: float | None = None  # when the job began holding back tasks, if it has
    err: str = ""  # why the job failed, if it did
    finished: float | None = None  # None while the job runs

    def status(self) -> coordinator_pb2.GetJobStatusResponse:
        """Describes the job's progress."""
        maps_done, reduces_done = self.tasks.progress()
        state = coordinator_pb2.JOB_STATE_MAPPING
        if self.err:
            state = coordinator_pb2.JOB_STATE_FAILED
        elif self.tasks.done():
            state = coordinator_pb2.JOB_STATE_SUCCEEDED
        elif maps_done == len(self.splits):
            state = coordinator_pb2.JOB_STATE_REDUCING
        backups, won = self.tasks.backups()
        near = {types_pb2.LOCALITY_HOST: 0, types_pb2.LOCALITY_ZONE: 0}
        for t, loc in self.ran.items():
            if loc in near and self.tasks.state(t) == COMPLETED:
                near[loc] += 1
        resp = coordinator_pb2.GetJobStatusResponse(
            job_id=self.id,
            state=state,
            map_tasks_total=len(self.splits),
            map_tasks_completed=maps_done,
            reduce_tasks_total=self.spec.num_reduce_tasks,
            reduce_tasks_completed=reduces_done,
            counters=types_pb2.Counters(values=self.total()),
            error=self.err,
            map_tasks_host_local=near[types_pb2.LOCALITY_HOST],
            map_tasks_zone_local=near[types_pb2.LOCALITY_ZONE],
            backup_attempts=backups,
            backup_wins=won,
        )
        if self.submitted is not None:
            resp.submitted_at.FromNanoseconds(_ns(self.submitted))
        if self.finished is not None:
            resp.finished_at.FromNanoseconds(_ns(self.finished))
        return resp

    def total(self) -> dict[str, int]:
        """Sums the counters of every task's accepted attempt, wrapping at
        int64 as Go does."""
        total: dict[str, int] = {}
        for counts in self.counts.values():
            for name, n in counts.items():
                total[name] = (total.get(name, 0) + n + 2**63) % 2**64 - 2**63
        return total

    def cleanup(self):
        """Removes what dead or failed reduce attempts left in the job's
        output's _temporary directory. Call it under the lock once the log
        holds the job's end. A missing directory is ignored; other OSError
        and ValueError failures are caught without changing the job's outcome."""
        try:
            shutil.rmtree(os.path.join(path(self.spec.output), TEMP_DIR))
        except FileNotFoundError:
            pass
        except (OSError, ValueError) as e:
            log.warning("cleaning up job output: job %s: %s", self.id, e)


def _ns(t: float) -> int:
    """Returns Unix time t in nanoseconds, rounded to the microsecond, which a
    float holds exactly enough to survive the log: a replayed time reads
    back as the time first reported."""
    return round(t * 1e6) * 1000


def _rpc(method):
    """Turns an RpcError raised by a servicer method into the RPC's status."""

    def wrapper(self, request, context):
        try:
            return method(self, request, context)
        except RpcError as e:
            context.abort(e.code, str(e))

    wrapper.__name__, wrapper.__doc__ = method.__name__, method.__doc__
    return wrapper


def locality(split: types_pb2.InputSplit, w: types_pb2.WorkerInfo | None) -> int:
    """Says how close a worker is to a split. A location matches the
    worker's host or its zone; an empty one matches neither."""
    if not split.locations:
        return types_pb2.LOCALITY_UNSPECIFIED
    host, zone = (w.host, w.zone) if w is not None else ("", "")
    if host and host in split.locations:
        return types_pb2.LOCALITY_HOST
    if zone and zone in split.locations:
        return types_pb2.LOCALITY_ZONE
    return types_pb2.LOCALITY_REMOTE


class Server(coordinator_pb2_grpc.CoordinatorServiceServicer):
    """Implements CoordinatorService, as go/internal/coordinator.Server."""

    def __init__(self, cfg: Config | None = None):
        """Creates an idle coordinator, using default settings if cfg is None.
        Network serving and worker monitoring are started separately."""
        # A copy, as Go's Config is a value: recover sets its log.
        self._cfg = replace(cfg) if cfg else Config()
        self._mu = threading.Lock()
        self._workers = Registry(self._cfg.id_prefix + "w")
        self._jobs: dict[str, _Job] = {}
        self._order: list[_Job] = []  # every job kept, in the order submitted
        self._seq = 0  # jobs submitted, for the next ID; never reused
        self._active: _Job | None = None
        self._lost: list[LostWorker] = []  # since this process started
        self._stopping = False
        self._log_err: str | None = None  # why the log refused a record, if it has
        self._replay_ended: list[_Job] = []  # jobs whose end replay saw, for recover to clean up
        self._log_path: str | None = None  # where recover opened the log, to reopen it
        self._heal_at = 0.0  # when _failed may next try to reopen the log, by _clock

    @contextmanager
    def _locked(self) -> Iterator[None]:
        """Holds the lock for an RPC, raising _failed's error first."""
        with self._mu:
            self._failed()
            yield

    def _failed(self):
        """Raises RpcError with Unavailable while the log refuses records.
        The memory may then hold a change the log does not, such as a job
        ended by a report that could not be logged, so no RPC may act on it.
        A server from recover instead rebuilds itself from the log, as a
        restart would, once the log takes records again: a full disk stops
        the cluster only until space is freed. Retries are timed by the
        monotonic clock, so a wall clock set back cannot postpone them. Call
        it under the lock."""
        if self._log_err is None:
            return
        if self._log_path is not None and self._clock() >= self._heal_at:
            try:
                self._heal()
                return
            except Exception as e:
                self._log_err = str(e) or type(e).__name__
            self._heal_at = self._clock() + HEAL_EVERY
        if self._log_path is None:
            msg = f"coordinator log failed; restart the coordinator: {self._log_err}"
        else:
            msg = f"coordinator log failed, retrying: {self._log_err}"
        raise RpcError(grpc.StatusCode.UNAVAILABLE, msg)

    def _heal(self):
        """Replaces the jobs and workers with those recovered from the log,
        dropping every change the log never took. Running attempts stop and
        workers register again, as after a restart. Call it under the lock;
        raises what recover raises."""
        from mapreduce_reloaded.coordinator.recover import recover  # it imports this module

        assert self._log_path is not None
        with suppress(Exception):  # it has already failed; reopening it is what matters
            getattr(self._cfg.log, "close", lambda: None)()
        n, wal = recover(replace(self._cfg, log=None), self._log_path)
        log.info("coordinator log works again; recovered from it: %s", self._log_path)
        self._cfg.log = wal
        self._workers, self._jobs, self._order = n._workers, n._jobs, n._order
        self._seq, self._active = n._seq, n._active
        self._log_err = None

    def close(self):
        """Closes the log the server writes to, if it can be closed. Use it
        rather than the log recover returned, which a healed server has
        replaced."""
        with self._mu:
            getattr(self._cfg.log, "close", lambda: None)()

    def now(self) -> float:
        """Reads the coordinator's clock: Config.now, or time.time."""
        return self._cfg.now() if self._cfg.now else time.time()

    def _clock(self) -> float:
        """Reads the monotonic clock: Config.clock, or time.monotonic."""
        return self._cfg.clock() if self._cfg.clock else time.monotonic()

    def shutdown(self):
        """Makes every worker's next RequestTask tell it to exit."""
        with self._mu:
            self._stopping = True

    def expire(self, now: float) -> list[str]:
        """Declares dead workers last heard from more than worker_timeout
        seconds before now, on the monotonic clock, and returns their sorted
        IDs. A zero timeout disables expiry. In the active job, their running
        attempts stop and their completed map tasks become idle again, since
        map output lives on the worker's disk. Tasks with another running
        attempt continue; completed reduce tasks stay done (§3.3). A dead worker
        that calls again is told to register anew. Log failures do not
        prevent workers from expiring or their tasks from being reset."""
        if not self._cfg.worker_timeout:
            return []
        with self._mu:
            dead = self._workers.expire(now, self._cfg.worker_timeout)
            at = self.now()
            for w in dead:
                reset = []
                if self._active is not None:
                    try:
                        self._record(
                            log_pb2.LogRecord(worker_lost=log_pb2.WorkerLost(worker_id=w)), at
                        )
                    except RpcError as e:
                        log.error("logging a lost worker: worker %s: %s", w, e)
                    reset = self._active.tasks.reset_worker(w)
                self._lost = [*self._lost, LostWorker(w, at, reset)][-MAX_LOST:]
                log.warning("worker lost: worker %s, %d tasks reset", w, len(reset))
            return dead

    def monitor(self, stop: threading.Event):
        """Calls expire every heartbeat_interval seconds until stop is set.
        Returns immediately if worker_timeout or heartbeat_interval is not
        positive."""
        if self._cfg.worker_timeout <= 0 or self._cfg.heartbeat_interval <= 0:
            return
        while not stop.wait(self._cfg.heartbeat_interval):
            self.expire(self._clock())

    @_rpc
    def RegisterWorker(self, request, context):
        """Registers a copy of the worker's information and returns its new
        ID and the configured heartbeat interval."""
        with self._locked():
            resp = coordinator_pb2.RegisterWorkerResponse(
                worker_id=self._workers.register(request.worker, self._clock())
            )
        resp.heartbeat_interval.FromNanoseconds(int(self._cfg.heartbeat_interval * 1e9))
        return resp

    @_rpc
    def Heartbeat(self, request, context):
        """Tells an unknown worker to register again, and a known one which
        of the attempts it runs to stop: those no longer running on it, such
        as a backup whose twin finished first (§3.6), or those of an ended
        job. Known workers also receive the active job ID, or an empty list
        when idle, so they can prune ended jobs' map output."""
        with self._locked():
            worker = request.worker_id
            if not self._workers.heartbeat(worker, self._clock()):
                return coordinator_pb2.HeartbeatResponse(reregister=True)
            resp = coordinator_pb2.HeartbeatResponse(active_jobs=coordinator_pb2.ActiveJobs())
            if self._active is not None:
                resp.active_jobs.job_ids.append(self._active.id)
            for p in request.running:
                if not self._runs(p.attempt, worker):
                    resp.cancel.append(p.attempt)
            return resp

    def _runs(self, attempt: types_pb2.TaskAttemptId, worker: str) -> bool:
        """Reports whether attempt, of the active job, is running on worker."""
        j = self._jobs.get(attempt.task.job_id)
        t = Task(attempt.task.kind, attempt.task.index)
        return j is not None and j is self._active and j.tasks.runs(t, attempt.attempt, worker)

    @_rpc
    def SubmitJob(self, request, context):
        """Validates the job, defaulting R to a multiple of the number of
        workers (§3.5), and plans its map tasks. On success it makes the job
        active and returns its ID. Errors are InvalidArgument for invalid
        jobs or input planning failures, AlreadyExists if the output already
        holds job output, FailedPrecondition if another job is running or
        output setup fails, and Unavailable if recording the submission
        fails. Exceptions other than RpcError from Config.locate propagate."""
        if not request.HasField("job"):
            raise RpcError(grpc.StatusCode.INVALID_ARGUMENT, "no job")
        m = types_pb2.JobSpec()
        m.CopyFrom(request.job)
        if m.num_reduce_tasks == 0:
            with self._locked():
                m.num_reduce_tasks = default_r(len(self._workers))
        if errs := validate(m, self._cfg.programs):
            raise RpcError(grpc.StatusCode.INVALID_ARGUMENT, "\n".join(errs))
        try:
            fmt, inputs = input_format(m.input_format), [path(u) for u in m.inputs]
            if m.num_map_tasks:
                planned = plan_m(fmt, inputs, m.num_map_tasks)
            else:
                planned = plan(fmt, inputs, m.split_size_bytes)
        except (OSError, ValueError) as e:
            raise RpcError(grpc.StatusCode.INVALID_ARGUMENT, f"split: {e}") from e
        if not planned:
            raise RpcError(grpc.StatusCode.INVALID_ARGUMENT, "the input is empty")
        splits = [input_split(s) for s in planned]
        if self._cfg.locate is not None:
            for s in splits:
                s.locations.extend(self._cfg.locate(s))

        with self._locked():
            if self._active is not None:
                raise RpcError(
                    grpc.StatusCode.FAILED_PRECONDITION, f"job {self._active.id} is still running"
                )
            output = path(m.output)
            # Under the lock, so no job finishes in between.
            if glob.glob(os.path.join(glob.escape(output), "part-r-*")):
                raise RpcError(
                    grpc.StatusCode.ALREADY_EXISTS, f"task: {output} already holds job output"
                )
            try:
                os.makedirs(os.path.join(output, TEMP_DIR), exist_ok=True)
            except OSError as e:
                raise RpcError(grpc.StatusCode.FAILED_PRECONDITION, str(e)) from e
            rec = log_pb2.JobSubmitted(
                job_id=f"job-{self._cfg.id_prefix}{self._seq + 1}", job=m, splits=splits
            )
            now = self.now()
            self._record(log_pb2.LogRecord(job_submitted=rec), now)
            self._submit(rec, now)
            return coordinator_pb2.SubmitJobResponse(job_id=rec.job_id)

    def _submit(self, rec: log_pb2.JobSubmitted, at: float | None):
        """Makes the job rec describes, submitted at at, the active one,
        advances the job sequence, and forgets old jobs per keep_jobs."""
        j = _Job(
            id=rec.job_id,
            spec=rec.job,
            splits=list(rec.splits),
            tasks=TaskTable(len(rec.splits), rec.job.num_reduce_tasks),
            submitted=at,
        )
        self._jobs[j.id] = self._active = j
        self._order.append(j)
        # From the ID, not a count: a compacted log no longer holds every job.
        try:
            n = int(j.id.removeprefix("job-"))
        except ValueError:
            n = 0
        self._seq = max(self._seq + 1, n)
        self._forget()

    def _forget(self):
        """Drops the oldest ended jobs beyond keep_jobs. The active job, the
        newest, is never dropped."""
        keep = self._cfg.keep_jobs
        limit = keep + (self._active is not None)
        dropped = False
        while keep > 0 and len(self._order) > limit and self._order[0] is not self._active:
            del self._jobs[self._order.pop(0).id]
            dropped = True
        if dropped:
            self._compact()

    def _compact(self):
        """Rewrites the log with the records of the jobs still kept, so its
        size follows keep_jobs rather than every job ever run (#178), as Go's
        compact. It keeps every Restarted record, which recover counts to
        name workers, and a WorkerLost record only if the job submitted
        before it is kept: jobs run one at a time, so that is the job it
        applied to, if any. A failure is logged; the log is either unchanged
        or, if the failure came after its replacement, refuses further
        records, which stops the coordinator taking new decisions."""
        compact = getattr(self._cfg.log, "compact", None)
        if compact is None or self._cfg.keep_jobs <= 0:
            return
        last = ""

        def keep(b: bytes) -> bool:
            nonlocal last
            rec = log_pb2.LogRecord.FromString(b)
            kind = rec.WhichOneof("record")
            if kind == "job_submitted":
                last = rec.job_submitted.job_id
                return last in self._jobs
            if kind == "task_assigned":
                return rec.task_assigned.attempt.task.job_id in self._jobs
            if kind == "task_reported":
                return rec.task_reported.attempt.task.job_id in self._jobs
            if kind == "worker_lost":
                return last in self._jobs
            return True  # Restarted, and anything newer: keep what is not understood

        try:
            compact(keep)
        except Exception as e:
            log.error("compacting the coordinator log: %s", e)

    def _replay(self, rec: log_pb2.LogRecord):
        """Applies a logged decision as the handler that logged it did, at
        the time it was made, as Go's replay. A record without a time leaves
        submission or finish times unknown. Raises ValueError for unknown
        records, inconsistent assignments, or invalid reports."""
        at = rec.at.ToNanoseconds() / 1e9 if rec.HasField("at") else None
        kind = rec.WhichOneof("record")
        if kind == "job_submitted":
            self._submit(rec.job_submitted, at)
        elif kind == "task_assigned":
            a = rec.task_assigned
            id = a.attempt
            if self._active is None or self._active.id != id.task.job_id:
                raise ValueError(f"coordinator log: {id} assigned outside the running job")
            t = Task(id.task.kind, id.task.index)
            try:
                attempt = self._active.tasks.assign(t, a.worker_id, at or 0.0)
                if attempt != id.attempt:
                    raise ValueError(f"replayed as attempt {attempt}")
            except ValueError as e:
                raise ValueError(f"coordinator log: assigning {id}: {e}") from e
            self._active.placed.setdefault(t, []).append(a.locality)
        elif kind == "task_reported":
            job = rec.task_reported.attempt.task.job_id
            j = self._jobs.get(job)
            if j is None:
                raise ValueError(f'coordinator log: report for unknown job "{job}"')
            try:
                _, ended = self._report(j, rec.task_reported, at)
            except ValueError as e:
                raise ValueError(f"coordinator log: {e}") from e
            if ended:  # the coordinator may have died before cleaning up
                self._replay_ended.append(j)
        elif kind == "worker_lost":
            if self._active is not None:
                self._active.tasks.reset_worker(rec.worker_lost.worker_id)
        elif kind == "restarted":
            if self._active is not None:
                self._active.tasks.stop_all()
        else:
            raise ValueError(f"coordinator log: unknown record {rec}")

    @_rpc
    def RequestTask(self, request, context):
        """Hands an idle worker its next task: map tasks first, the nearest
        to their input (§3.4), reduce tasks once every map task has completed
        (§3.1), then backup attempts if enabled (§3.6). It returns a wait
        instruction when no task is ready, or a shutdown instruction after
        shutdown. A paused worker receives only wait or shutdown instructions.
        Errors are NotFound for an unknown worker, Internal if
        assigning a task fails, or Unavailable if recording it fails."""
        with self._locked():
            worker, now, tick = request.worker_id, self.now(), self._clock()
            if not self._workers.heartbeat(worker, tick):
                raise RpcError(
                    grpc.StatusCode.NOT_FOUND, f'unknown worker "{worker}": register again'
                )
            if self._stopping:
                return coordinator_pb2.RequestTaskResponse(
                    shutdown=coordinator_pb2.ShutdownInstruction()
                )
            j = self._active
            if j is not None and not request.paused:
                info = self._worker_info(worker)
                t = j.tasks.next(lambda i: locality(j.splits[i], info))
                if t is not None and self._hold_back(t, info, tick):
                    t = None
                elif t is None and self._cfg.backups:
                    t = j.tasks.backup(worker)
                if t is not None:
                    return self._assign(t, worker, now)
            resp = coordinator_pb2.RequestTaskResponse(wait=coordinator_pb2.WaitInstruction())
            resp.wait.retry_after.FromNanoseconds(int(self._cfg.retry_after * 1e9))
            return resp

    def _assign(self, t: Task, worker: str, now: float) -> coordinator_pb2.RequestTaskResponse:
        """Starts an attempt at t, a task of the active job, on worker at now
        (Unix seconds), and returns its map or reduce assignment. Raises
        RpcError with Internal if assignment fails or Unavailable if recording
        fails. An error does not roll back the attempt."""
        j = self._active
        try:
            attempt = j.tasks.assign(t, worker, now)
        except ValueError as e:
            raise RpcError(grpc.StatusCode.INTERNAL, str(e)) from e
        skip = j.tasks.skip(t)
        id = types_pb2.TaskAttemptId(
            task=types_pb2.TaskId(job_id=j.id, kind=t.kind, index=t.index), attempt=attempt
        )
        rec = log_pb2.TaskAssigned(attempt=id, worker_id=worker)
        if t.kind == MAP:
            rec.locality = locality(j.splits[t.index], self._worker_info(worker))
        j.placed.setdefault(t, []).append(rec.locality)
        self._record(log_pb2.LogRecord(task_assigned=rec), now)
        if t.kind == MAP:
            return coordinator_pb2.RequestTaskResponse(
                map_task=coordinator_pb2.MapTask(
                    attempt=id, job=j.spec, split=j.splits[t.index], skip_records=skip
                )
            )
        return coordinator_pb2.RequestTaskResponse(
            reduce_task=coordinator_pb2.ReduceTask(
                attempt=id, job=j.spec, sources=j.tasks.sources(t.index), skip_records=skip
            )
        )

    def _hold_back(self, t: Task, info, now: float) -> bool:
        """Reports whether a worker should wait, as of now by the monotonic
        clock, rather than run t, the nearest task to it, because t is a map
        task whose split is on other hosts. The job holds such tasks back for
        at most locality_wait at a time: the wait starts when it first holds
        one back, and ends when it starts one on a host that holds its split."""
        j = self._active
        if t.kind != MAP or not self._cfg.locality_wait:
            return False
        if locality(j.splits[t.index], info) in (
            types_pb2.LOCALITY_UNSPECIFIED,
            types_pb2.LOCALITY_HOST,
        ):
            j.since = None
            return False
        if j.since is None:
            j.since = now
        return now - j.since < self._cfg.locality_wait

    def _worker_info(self, id: str) -> types_pb2.WorkerInfo | None:
        """Returns what a live worker said about itself when it registered."""
        w = self._workers.lookup(id)
        return None if w is None else w.info

    @_rpc
    def ReportTask(self, request, context):
        """Records the outcome of an attempt, from the worker it was assigned
        to. The first successful attempt of a task is accepted, and only its
        counters are kept (§3.3, §4.9). Failure reports, stale attempts and
        reports for ended jobs return accepted=False. Errors are NotFound for
        an unknown job, InvalidArgument for a report with no outcome or for task or
        output validation errors, and Unavailable if recording the report
        fails. A report that ends the job cleans up its output only once the
        log holds it (#182)."""
        with self._locked():
            job_id = request.attempt.task.job_id
            j = self._jobs.get(job_id)
            if j is None:
                raise RpcError(grpc.StatusCode.NOT_FOUND, f'unknown job "{job_id}"')
            if request.WhichOneof("outcome") is None:
                raise RpcError(
                    grpc.StatusCode.INVALID_ARGUMENT, "report has neither success nor failure"
                )
            now = self.now()
            try:
                accepted, ended = self._report(j, request, now)
            except ValueError as e:
                raise RpcError(grpc.StatusCode.INVALID_ARGUMENT, str(e)) from e
            self._record(log_pb2.LogRecord(task_reported=request), now)
            if ended:
                j.cleanup()
            return coordinator_pb2.ReportTaskResponse(accepted=accepted)

    def _report(self, j: _Job, req, at: float | None) -> tuple[bool, bool]:
        """Applies req, received at at, to j, and says whether it completed
        its task and whether it ended the job. A report for an ended job
        changes nothing: its outcome and progress are final. Raises
        ValueError for invalid tasks or map outputs."""
        if self._active is not j:
            return False, False
        id = req.attempt
        t = Task(id.task.kind, id.task.index)
        if req.HasField("failure"):
            f = req.failure
            counted = j.tasks.runs(t, id.attempt, req.worker_id)
            j.tasks.fail(t, id.attempt, req.worker_id, f)
            n = j.tasks.failures(t)
            limit = j.spec.max_skipped_records if j.spec.HasField("max_skipped_records") else None
            if not counted:  # a stale report changes nothing
                pass
            elif f.fatal:
                self._end(j, f"{t} cannot run: {f.message}", at)
            elif (
                self._cfg.max_task_failures > 0
                and n >= self._cfg.max_task_failures
                and not f.HasField("lost_output")
            ):
                self._end(j, f"{t} failed {n} times; the last error: {f.message}", at)
            elif limit is not None and j.tasks.skipped() > limit:
                self._end(j, f"more than {limit} bad records to skip", at)
            return False, self._active is not j
        accepted = j.tasks.complete(t, id.attempt, req.worker_id, req.success.outputs)
        if accepted:
            # A map task run again after its worker died replaces its counts.
            j.counts[t] = dict(req.success.counters.values)
            j.ran[t] = j.placed[t][id.attempt]
            if j.tasks.done():
                self._end(j, "", at)
        return accepted, self._active is not j

    def _end(self, j: _Job, err: str, at: float | None):
        """Ends j at at if it is the active job: it failed with err, or
        succeeded if err is empty. The caller cleans up j's output."""
        if self._active is not j:
            return
        j.err, j.finished = err, at
        self._active = None
        self._forget()

    @_rpc
    def GetJobStatus(self, request, context):
        """Returns the retained job's state, progress, counters and timestamps.
        Returns NotFound for an unknown job, including one forgotten per keep_jobs."""
        with self._locked():
            j = self._jobs.get(request.job_id)
            if j is None:
                raise RpcError(grpc.StatusCode.NOT_FOUND, f'unknown job "{request.job_id}"')
            return j.status()

    def _record(self, rec: log_pb2.LogRecord, at: float):
        """Appends rec, a decision made at at, to the log, if there is one.
        It runs under the lock, so no reply reveals a change before the log
        holds it (ADR 0007). Raises RpcError with Unavailable if serialization
        or appending fails; timestamp conversion errors propagate."""
        if self._cfg.log is None:
            return
        rec.at.FromNanoseconds(_ns(at))
        try:
            self._cfg.log.append(rec.SerializeToString())
        except Exception as e:
            self._log_err = str(e) or type(e).__name__
            raise RpcError(grpc.StatusCode.UNAVAILABLE, f"coordinator log: {e}") from e


def _base36(n: int) -> str:
    """Formats n, which is not negative, in base 36, as Go's
    strconv.FormatInt(n, 36)."""
    digits = ""
    while True:
        n, d = divmod(n, 36)
        digits = "0123456789abcdefghijklmnopqrstuvwxyz"[d] + digits
        if n == 0:
            return digits


def serve(address: str, cfg: Config | None = None, threads: int = 16):
    """Starts an insecure gRPC coordinator on address, such as "[::]:18770",
    and its worker monitor. Port 0 selects an available port; threads limits
    concurrent RPC handlers. Returns the gRPC server, the Server, the bound
    port and the event that stops the monitor. The caller must also stop the
    gRPC server separately. Raises ValueError if threads is nonpositive and
    propagates RuntimeError if binding fails."""
    cfg = cfg or Config()
    if cfg.log is None and not cfg.id_prefix:
        # Without a log, IDs start from 1 again: tell them from the last run's.
        cfg = replace(cfg, id_prefix=_base36(time.time_ns()) + "-")
    coordinator = Server(cfg)
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=threads))
    coordinator_pb2_grpc.add_CoordinatorServiceServicer_to_server(coordinator, server)
    port = server.add_insecure_port(address)
    server.start()
    stop = threading.Event()
    threading.Thread(target=coordinator.monitor, args=(stop,), daemon=True).start()
    return server, coordinator, port, stop
