"""The task table, mirroring go/internal/coordinator/tasktable.go."""

import math
from collections.abc import Callable
from dataclasses import dataclass, field
from typing import NamedTuple

from mapreduce_reloaded.gen.mapreduce.v1 import coordinator_pb2, types_pb2

MAP = types_pb2.TASK_KIND_MAP
REDUCE = types_pb2.TASK_KIND_REDUCE
IDLE = types_pb2.TASK_STATE_IDLE
IN_PROGRESS = types_pb2.TASK_STATE_IN_PROGRESS
COMPLETED = types_pb2.TASK_STATE_COMPLETED

# How many attempts must crash on a record before later attempts skip it:
# more than one, so that one crash with another cause loses no record (§4.6).
SKIP_AFTER = 2


class Task(NamedTuple):
    """Names one task of the job."""

    kind: int
    index: int

    def __str__(self):
        return f"{types_pb2.TaskKind.Name(self.kind)} {self.index}"


@dataclass
class _Run:
    """A running attempt."""

    worker: str
    started: float
    backup: bool  # started while another attempt of the task was running (§3.6)
    fraction: float = 0.0  # of its input the worker last reported processed
    reported: float = 0.0  # when the coordinator heard it; 0 before the first report


@dataclass
class _Task:
    state: int = IDLE
    attempts: int = 0  # attempts started; the next one gets this number
    running: dict[int, _Run] = field(default_factory=dict)  # each running attempt
    worker: str = ""  # worker of the accepted attempt, once completed
    outputs: list = field(default_factory=list)  # map tasks: one per partition, once completed
    crashes: dict[int, int] = field(default_factory=dict)  # attempts that crashed on each record
    failures: int = 0  # failed attempts, except fetches of lost map output
    skip: list[int] = field(default_factory=list)  # records later attempts skip

    def can_back_up(self, worker: str) -> bool:
        """Reports whether the task may get a backup attempt on worker: it is
        in progress with one attempt, on another worker."""
        return (
            self.state == IN_PROGRESS
            and len(self.running) == 1
            and all(r.worker != worker for r in self.running.values())
        )

    def started(self) -> float:
        """Returns when the task's earliest running attempt started."""
        return min(r.started for r in self.running.values())

    def runs_on(self, attempt: int, worker: str) -> bool:
        r = self.running.get(attempt)
        return r is not None and r.worker == worker

    def stop_attempts_on(self, worker: str) -> bool:
        """Stops the task's running attempts on worker, and reports whether
        that made the task idle."""
        stopped = [a for a, r in self.running.items() if r.worker == worker]
        for a in stopped:
            del self.running[a]
        return bool(stopped) and self.idle_if_stopped()

    def crashed(self, n: int) -> bool:
        """Counts an attempt that crashed on record n, and skips n once
        SKIP_AFTER attempts have. Reports whether it added n to the skip list."""
        self.crashes[n] = self.crashes.get(n, 0) + 1
        if self.crashes[n] != SKIP_AFTER:
            return False
        self.skip.append(n)
        return True

    def idle_if_stopped(self) -> bool:
        """Makes an in-progress task with no running attempt idle, and reports
        whether it did."""
        if self.state != IN_PROGRESS or self.running:
            return False
        self.state = IDLE
        return True

    def reset(self):
        self.state, self.worker, self.outputs = IDLE, "", []


class TaskTable:
    """Tracks the state of a job's M map and R reduce tasks:

        IDLE --assign--> IN_PROGRESS --complete--> COMPLETED
          ^                   |                        |
          +--- fail, or reset_worker on its worker ----+ (map tasks only)

    A task returns to IDLE when its last running attempt fails or its worker
    is lost. A completed map task also returns to IDLE when its worker is
    lost or a reduce task cannot fetch its output, since that output was on
    the worker's disk; a completed reduce task's output is in the global
    file system, so it stays done (§3.3). The first attempt to complete
    wins; later reports are rejected.

    Near the end of a phase, an in-progress task may get one backup attempt
    on another worker, so that one slow machine does not hold up the job
    (§3.6). The task that has run longest gets the first backup.
    """

    def __init__(self, m: int, r: int):
        self._maps = [_Task() for _ in range(m)]
        self._reduces = [_Task() for _ in range(r)]
        self._maps_done = 0
        self._reduces_done = 0
        self._skipped = 0  # records on every task's skip list
        self._backups = 0  # backup attempts started
        self._backup_wins = 0  # tasks completed by a backup attempt

    def next(self, rank: Callable[[int], int] | None = None) -> Task | None:
        """Returns an idle task to run: a map task, or a reduce task once
        every map task has completed (§3.1). If rank is given, it is the map
        task that rank ranks lowest, the first one if several tie. Returns
        None if there is none."""
        kind, tasks = self._phase()
        if kind == REDUCE:
            rank = None
        best, best_rank = None, 0
        for i, ts in enumerate(tasks):
            if ts.state != IDLE:
                continue
            if rank is None:
                return Task(kind, i)
            if (r := rank(i)) < best_rank or best is None:
                best, best_rank = i, r
        return None if best is None else Task(kind, best)

    def backup(self, worker: str) -> Task | None:
        """Returns the in-progress task of the current phase that may get a
        backup attempt on worker and whose attempt started first, so the
        likeliest straggler is backed up first (§3.6), or None."""
        kind, tasks = self._phase()
        best = None
        for i, ts in enumerate(tasks):
            if ts.can_back_up(worker) and (best is None or ts.started() < tasks[best].started()):
                best = i
        return None if best is None else Task(kind, best)

    def _phase(self):
        """Returns the kind and tasks of the current phase: map tasks until
        every one has completed, then reduce tasks (§3.1)."""
        if self._maps_done == len(self._maps):
            return REDUCE, self._reduces
        return MAP, self._maps

    def assign(self, id: Task, worker: str, started: float) -> int:
        """Starts a new attempt on worker at started, of an idle task, or a
        backup attempt of one running elsewhere, and returns its number."""
        ts = self._task(id)
        backup = ts.state != IDLE
        if backup and not ts.can_back_up(worker):
            state, n = types_pb2.TaskState.Name(ts.state), len(ts.running)
            raise ValueError(
                f"coordinator: assign {id} to {worker}: task is {state} on {n} workers"
            )
        if backup:
            self._backups += 1
        attempt = ts.attempts
        ts.attempts += 1
        ts.running[attempt] = _Run(worker, started, backup)
        ts.state = IN_PROGRESS
        return attempt

    def set_fraction(self, id: Task, attempt: int, worker: str, fraction: float, at: float) -> bool:
        """Records that attempt of task id, running on worker, reported at at
        that it had processed fraction of its input, clamped to 0 to 1.
        Reports whether it recorded it: not if the attempt is not running on
        worker, or fraction is NaN. The fraction is for status only and does
        not affect scheduling."""
        if not self.runs(id, attempt, worker) or math.isnan(fraction):
            return False
        r = self._task(id).running[attempt]
        r.fraction, r.reported = min(max(fraction, 0.0), 1.0), at
        return True

    def fraction(self, id: Task, worker: str) -> float:
        """Returns the fraction of its input that the attempt of task id
        running on worker last reported processed, or 0 if it has not
        reported or no attempt of the task runs on worker."""
        try:
            running = self._task(id).running.values()
        except ValueError:
            return 0.0
        return next((r.fraction for r in running if r.worker == worker), 0.0)

    def complete(self, id: Task, attempt: int, worker: str, outputs=()) -> bool:
        """Records that worker finished an attempt. A map task's outputs must
        hold one location per partition, in order. Reports whether the result
        was accepted: False if the task already completed or the attempt is
        not running on worker, for example because its worker was declared
        lost."""
        ts = self._task(id)
        if not ts.runs_on(attempt, worker):
            return False
        if id.kind == MAP:
            self._check_outputs(id.index, outputs)
            ts.outputs = list(outputs)
            self._maps_done += 1
        else:
            self._reduces_done += 1
        if ts.running[attempt].backup:
            self._backup_wins += 1
        ts.running.clear()
        ts.state, ts.worker = COMPLETED, worker
        return True

    def fail(
        self, id: Task, attempt: int, worker: str, f: coordinator_pb2.TaskFailure | None = None
    ):
        """Records that an attempt on worker failed as f says. The task
        becomes idle again unless another attempt is still running. If the
        attempt could not fetch a map output, that map task runs again
        (§3.3); if it crashed on a record, later attempts may skip that record
        (§4.6). Reports for attempts that are not running on worker are
        ignored."""
        ts = self._task(id)
        if not ts.runs_on(attempt, worker):
            return
        del ts.running[attempt]
        ts.idle_if_stopped()
        lost = f.lost_output if f is not None and f.HasField("lost_output") else None
        if lost is None:  # a lost map output is not this task's fault
            ts.failures += 1
        else:
            self._lose(lost)
        if f is not None and f.HasField("failed_record") and ts.crashed(f.failed_record):
            self._skipped += 1

    def failures(self, id: Task) -> int:
        """Returns how many attempts of a task failed, not counting those that
        could not fetch lost map output, or 0 for an invalid task."""
        try:
            return self._task(id).failures
        except ValueError:
            return 0

    def skip(self, id: Task) -> list[int]:
        """Returns the records that attempts of a task skip, in the order they
        were added."""
        return list(self._task(id).skip)

    def _lose(self, lost: types_pb2.PartitionLocation):
        """Makes a completed map task idle if its output for lost's partition
        is still where lost says. One already run again elsewhere is kept."""
        i, p, addr = lost.map_index, lost.partition, lost.worker_address
        if not addr or i >= len(self._maps):
            return
        ts = self._maps[i]
        if p < len(ts.outputs) and ts.outputs[p].worker_address == addr:
            ts.reset()
            self._maps_done -= 1

    def reset_worker(self, worker: str) -> list[Task]:
        """Handles the loss of a worker (§3.3): its running attempts stop, and
        its completed map tasks must run again. Returns the tasks that became
        idle."""
        reset = []
        for i, ts in enumerate(self._maps):
            if ts.state == COMPLETED and ts.worker == worker:
                ts.reset()
                self._maps_done -= 1
                reset.append(Task(MAP, i))
            elif ts.stop_attempts_on(worker):
                reset.append(Task(MAP, i))
        reset += [
            Task(REDUCE, i) for i, ts in enumerate(self._reduces) if ts.stop_attempts_on(worker)
        ]
        return reset

    def stop_all(self):
        """Stops every running attempt, as when the coordinator restarts: the
        attempts' workers will register again under new IDs. Completed tasks
        stay completed; a map output that turns out to be lost is reported by
        the reduce task that cannot fetch it."""
        for ts in self._maps + self._reduces:
            ts.running.clear()
            ts.idle_if_stopped()

    def sources(self, p: int) -> list:
        """Returns where partition p of every map task's output lives, for a
        reduce task. Call it only once all map tasks have completed."""
        return [ts.outputs[p] for ts in self._maps]

    def state(self, id: Task) -> int:
        """Returns the state of a task."""
        return self._task(id).state

    def running(self) -> dict[str, list[Task]]:
        """Returns the tasks each worker is running an attempt of, in order:
        map tasks, then reduce tasks."""
        running: dict[str, list[Task]] = {}
        for kind, tasks in ((MAP, self._maps), (REDUCE, self._reduces)):
            for i, ts in enumerate(tasks):
                for r in ts.running.values():
                    running.setdefault(r.worker, []).append(Task(kind, i))
        return running

    def runs(self, id: Task, attempt: int, worker: str) -> bool:
        """Reports whether attempt of task id is running on worker."""
        try:
            return self._task(id).runs_on(attempt, worker)
        except ValueError:
            return False

    def progress(self) -> tuple[int, int]:
        """Returns the number of completed map and reduce tasks."""
        return self._maps_done, self._reduces_done

    def skipped(self) -> int:
        """Returns the number of records on every task's skip list."""
        return self._skipped

    def backups(self) -> tuple[int, int]:
        """Returns the number of backup attempts started, and of tasks that a
        backup attempt completed (§3.6)."""
        return self._backups, self._backup_wins

    def done(self) -> bool:
        """Reports whether every task has completed."""
        return self._maps_done == len(self._maps) and self._reduces_done == len(self._reduces)

    def _task(self, id: Task) -> _Task:
        tasks = {MAP: self._maps, REDUCE: self._reduces}.get(id.kind, [])
        if not 0 <= id.index < len(tasks):
            raise ValueError(f"coordinator: no task {id}")
        return tasks[id.index]

    def _check_outputs(self, index: int, outputs):
        where = f"coordinator: map {index} reported"
        if len(outputs) != len(self._reduces):
            raise ValueError(f"{where} {len(outputs)} outputs, want {len(self._reduces)}")
        for p, o in enumerate(outputs):
            if o is None or not (o.worker_address or o.uri):
                raise ValueError(f"{where} output {p} with no location")
            if o.map_index != index or o.partition != p:
                raise ValueError(f"{where} output {p} as map {o.map_index} partition {o.partition}")
