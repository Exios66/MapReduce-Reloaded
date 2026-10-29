"""The coordinator's task table and worker registry, ported from
go/internal/coordinator/tasktable_test.go and registry_test.go."""

import itertools
import random

import pytest

from mapreduce_reloaded.coordinator import SKIP_AFTER, Registry, Task, TaskTable
from mapreduce_reloaded.coordinator.tasktable import COMPLETED, IDLE, IN_PROGRESS, MAP, REDUCE
from mapreduce_reloaded.gen.mapreduce.v1 import coordinator_pb2, types_pb2

M0, M1, R0, R1 = Task(MAP, 0), Task(MAP, 1), Task(REDUCE, 0), Task(REDUCE, 1)

# When the next attempt that assign starts begins: one second after the last.
_clock = itertools.count(1)


def outputs(i, r):
    """Returns a valid output list for map task i of a job with r reduce tasks."""
    return [
        types_pb2.PartitionLocation(map_index=i, partition=p, uri="file:///map-output")
        for p in range(r)
    ]


def assign(tt, id, worker):
    return tt.assign(id, worker, float(next(_clock)))


def lost(loc):
    return coordinator_pb2.TaskFailure(lost_output=loc)


def crash(n):
    return coordinator_pb2.TaskFailure(failed_record=n)


def test_lifecycle():
    # A job runs its maps, then its reduces, each completing exactly once.
    tt = TaskTable(2, 1)
    a = assign(tt, M0, "w1")
    assert tt.next() == M1
    b = assign(tt, M1, "w2")
    assert tt.next() is None, "a reduce task was offered before the maps completed"
    assert tt.complete(M0, a, "w1", outputs(0, 1))
    assert tt.complete(M1, b, "w2", outputs(1, 1))
    assert tt.next() == R0
    src = tt.sources(0)
    assert len(src) == 2 and src[1].map_index == 1
    c = assign(tt, R0, "w1")
    assert tt.complete(R0, c, "w1") and tt.done()


def test_next_rank():
    # Next offers the idle map task that ranks lowest, the first of any tie,
    # and ignores the rank for reduce tasks.
    tt = TaskTable(3, 2)
    ranks = [2, 1, 1]
    assert tt.next(ranks.__getitem__) == M1
    assign(tt, M1, "w1")
    assert tt.next(ranks.__getitem__) == Task(MAP, 2)
    for i in range(3):
        a = 0 if i == 1 else assign(tt, Task(MAP, i), "w1")
        assert tt.complete(Task(MAP, i), a, "w1", outputs(i, 2))

    def never(_):
        raise AssertionError("ranked a reduce task")

    assert tt.next(never) == R0
    assert TaskTable(3, 1).next([1, 2, 3].__getitem__) == M0


def test_first_completion_wins():
    tt = TaskTable(1, 1)
    a = assign(tt, M0, "w1")
    assert not tt.complete(M0, a, "w2", outputs(0, 1)), (
        "completion reported by another worker accepted"
    )
    assert tt.complete(M0, a, "w1", outputs(0, 1))
    assert not tt.complete(M0, a, "w1", outputs(0, 1)), "second completion accepted"
    assert tt.progress() == (1, 0)


def test_fail_returns_to_idle():
    tt = TaskTable(1, 1)
    a = assign(tt, M0, "w1")
    tt.fail(M0, a + 1, "w1")  # not running on that worker: ignored
    tt.fail(M0, a, "w2")
    assert tt.state(M0) == IN_PROGRESS
    tt.fail(M0, a, "w1")
    assert tt.state(M0) == IDLE
    assert assign(tt, M0, "w2") == a + 1
    assert not tt.complete(M0, a, "w1", outputs(0, 1)), "completion of the failed attempt accepted"
    assert tt.failures(M0) == 1


def test_reset_worker():
    # Losing a worker re-runs its completed maps and running tasks, but not
    # its completed reduces (§3.3).
    tt = TaskTable(2, 2)
    for id in (M0, M1):
        tt.complete(id, assign(tt, id, "w1"), "w1", outputs(id.index, 2))
    tt.complete(R0, assign(tt, R0, "w1"), "w1")
    assign(tt, R1, "w1")
    assert tt.reset_worker("w1") == [M0, M1, R1]
    assert tt.state(R0) == COMPLETED
    assert tt.progress() == (0, 1)
    assert tt.reset_worker("w2") == []

    # A task that keeps an attempt on another worker is not reset.
    tt = TaskTable(1, 1)
    assign(tt, M0, "w1")
    assign(tt, M0, "w2")
    assert tt.reset_worker("w1") == []
    assert tt.state(M0) == IN_PROGRESS


def test_fail_with_lost_output():
    # A reduce that cannot fetch a map output sends that map task back to
    # idle, unless it has already run again elsewhere (§3.3).
    tt = TaskTable(1, 1)
    outs = outputs(0, 1)
    outs[0].worker_address = "w1:7000"
    tt.complete(M0, assign(tt, M0, "w1"), "w1", outs)
    gone = tt.sources(0)[0]
    loc = types_pb2.PartitionLocation
    stale = loc(map_index=0, worker_address="w9:7000")
    no_map = loc(map_index=1, worker_address="w1:7000")
    no_partition = loc(map_index=0, partition=1, worker_address="w1:7000")
    for f in (coordinator_pb2.TaskFailure(), lost(stale), lost(no_map), lost(no_partition)):
        tt.fail(R0, assign(tt, R0, "w2"), "w2", f)
        assert tt.state(M0) == COMPLETED
    tt.fail(R0, 99, "w2", lost(gone))  # not a running attempt
    assert tt.state(M0) == COMPLETED
    tt.fail(R0, assign(tt, R0, "w2"), "w2", lost(gone))
    assert tt.state(M0) == IDLE
    assert tt.state(R0) == IDLE
    assert tt.next() is not None
    # Only the failures that were not lost map output count against r0.
    assert tt.failures(R0) == 1
    check_counts(tt)


def test_fail_with_crash():
    # A record is skipped once SKIP_AFTER attempts have crashed on it. Crashes
    # that are not reported by a running attempt do not count (§4.6).
    assert SKIP_AFTER == 2
    tt = TaskTable(1, 1)
    for f in (crash(5), coordinator_pb2.TaskFailure(), crash(7), crash(5)):
        tt.fail(M0, assign(tt, M0, "w1"), "w1", f)
    tt.fail(M0, 99, "w1", crash(7))  # not a running attempt
    assert tt.skip(M0) == [5]
    assert tt.skip(R0) == [] and tt.skipped() == 1


def test_crash_on_record_zero_counts():
    # Record 0 is a record: presence, not truthiness, marks a crash.
    tt = TaskTable(1, 1)
    for _ in range(SKIP_AFTER):
        tt.fail(M0, assign(tt, M0, "w1"), "w1", crash(0))
    assert tt.skip(M0) == [0]


def test_backups():
    # An in-progress task gets at most one backup attempt, on another
    # worker. Whichever attempt completes first wins (§3.6).
    tt = TaskTable(2, 1)
    a = assign(tt, M0, "w1")
    assert tt.backup("w2") == M0
    b = assign(tt, M1, "w2")
    assert tt.backup("w1") == M1
    backup = assign(tt, M1, "w1")
    assert tt.backup("w3") == M0
    assign(tt, M0, "w3")
    assert tt.backup("w4") is None, "every task has its backup"

    # The backup of m1 wins; the original's report is rejected.
    assert tt.complete(M1, backup, "w1", outputs(1, 1))
    assert not tt.complete(M1, b, "w2", outputs(1, 1))
    # m0 loses its original to a failure, and its backup carries on.
    tt.fail(M0, a, "w1", coordinator_pb2.TaskFailure())
    assert tt.state(M0) == IN_PROGRESS
    assert tt.backup("w1") == M0
    # Losing the backup's worker too leaves m0 with no attempt.
    tt.reset_worker("w3")
    assert tt.state(M0) == IDLE
    check_counts(tt)
    assert tt.backups() == (2, 1)


def test_set_fraction():
    # A reported fraction is recorded, clamped, for an attempt running on the
    # reporting worker only, with the time it was heard. It is for status: it
    # does not change which task gets a backup (§3.6).
    tt = TaskTable(2, 1)
    a = assign(tt, M0, "w1")
    assign(tt, M1, "w2")
    nan = float("nan")
    for name, (id, attempt, worker, f, ok, want) in {
        "recorded": (M0, a, "w1", 0.5, True, 0.5),
        "another worker's": (M0, a, "w2", 0.9, False, 0.5),
        "unknown attempt": (M0, a + 1, "w1", 0.9, False, 0.5),
        "NaN": (M0, a, "w1", nan, False, 0.5),
        "latest, even if lower": (M0, a, "w1", 0.25, True, 0.25),
        "over 1": (M0, a, "w1", 7, True, 1.0),
        "under 0": (M0, a, "w1", -1, True, 0.0),
        "infinite": (M0, a, "w1", float("inf"), True, 1.0),
    }.items():
        assert tt.set_fraction(id, attempt, worker, f, 99.0) is ok, name
        assert tt.fraction(M0, "w1") == want, name
    assert tt._maps[0].running[a].reported == 99.0
    assert tt.fraction(M1, "w2") == 0, "an attempt that has not reported"
    assert tt.fraction(M0, "w2") == 0, "a worker not running the task"
    assert not tt.set_fraction(Task(MAP, 9), a, "w1", 0.5, 99.0)
    assert tt.fraction(Task(MAP, 9), "w1") == 0, "no such task"

    # M1 started later but is further along; M0 still gets the backup.
    tt.set_fraction(M1, 0, "w2", 0.9, 99.0)
    tt.set_fraction(M0, a, "w1", 0.1, 99.0)
    assert tt.backup("w3") == M0


@pytest.mark.parametrize("kind", [MAP, REDUCE])
def test_backup_oldest_first(kind):
    # The task whose attempt started first, the likeliest straggler, gets the
    # first backup, whatever its index; then the next oldest (§3.6).
    tt = TaskTable(3, 3)
    if kind == REDUCE:
        for i in range(3):
            assert tt.complete(Task(MAP, i), assign(tt, Task(MAP, i), "w0"), "w0", outputs(i, 3))
    for i, w in enumerate(["w3", "w1", "w2"]):  # task 2 first, then 0, then 1
        assign(tt, Task(kind, (i + 2) % 3), w)
    for want in (2, 0, 1):
        nxt = tt.backup("w4")
        assert nxt == Task(kind, want)
        assign(tt, nxt, "w4")


def test_errors():
    tt = TaskTable(1, 2)
    for bad in (Task(REDUCE, 2), Task(0, 0), Task(MAP, -1)):
        with pytest.raises(ValueError, match="no task"):
            tt.fail(bad, 0, "w1")
    for call in (
        lambda: tt.state(M1),
        lambda: tt.skip(M1),
        lambda: tt.assign(M1, "w1", 0.0),
        lambda: tt.complete(M1, 0, "w1"),
    ):
        with pytest.raises(ValueError, match="no task"):
            call()
    assert tt.failures(M1) == 0
    assert not tt.runs(M1, 0, "w1")
    a = assign(tt, M0, "w1")
    with pytest.raises(ValueError, match="assign"):
        tt.assign(M0, "w1", 0.0)  # backed a task up on its own worker
    assign(tt, M0, "w2")
    with pytest.raises(ValueError, match="assign"):
        tt.assign(M0, "w3", 0.0)  # backed a task up twice
    nowhere = outputs(0, 2)
    nowhere[1].uri = ""
    for bad in (outputs(0, 1), outputs(1, 2), nowhere, [None, None], []):
        with pytest.raises(ValueError):
            tt.complete(M0, a, "w1", bad)
    assert tt.state(M0) == IN_PROGRESS
    assert str(M0) == "TASK_KIND_MAP 0"


def test_running_and_stop_all():
    # running lists each worker's attempts, maps first; stop_all, as on a
    # coordinator restart, stops them all and keeps completed tasks.
    tt = TaskTable(2, 1)
    tt.complete(M0, assign(tt, M0, "w1"), "w1", outputs(0, 1))
    a = assign(tt, M1, "w1")
    assign(tt, M1, "w2")
    assert tt.running() == {"w1": [M1], "w2": [M1]}
    assert tt.runs(M1, a, "w1") and not tt.runs(M1, a, "w2")
    tt.stop_all()
    assert tt.running() == {}
    assert tt.state(M0) == COMPLETED and tt.state(M1) == IDLE
    check_counts(tt)


@pytest.mark.parametrize("seed", range(200))
def test_invariants(seed):
    # Random sequences of operations keep the table's invariants.
    m, r = 4, 3
    workers = ["w1", "w2", "w3"]
    rng = random.Random(seed)
    tt = TaskTable(m, r)
    attempts = {}
    for _ in range(100):
        kind = rng.choice([MAP, REDUCE])
        id = Task(kind, rng.randrange(m) % (r if kind == REDUCE else m))
        before = tt.state(id)
        op = rng.randrange(4)
        if op == 0:
            w = rng.choice(workers)
            nxt = tt.next() or tt.backup(w)
            if nxt is not None:
                assert nxt.kind == MAP or tt.progress()[0] == m, (
                    "reduce offered before the maps completed"
                )
                assert assign(tt, nxt, w) == attempts.get(nxt, 0)
                attempts[nxt] = attempts.get(nxt, 0) + 1
        elif op == 1:
            if tt.complete(id, rng.randrange(3), rng.choice(workers), outputs(id.index, r)):
                assert before == IN_PROGRESS
        elif op == 2:
            tt.fail(id, rng.randrange(3), rng.choice(workers))
            assert before != COMPLETED or tt.state(id) == COMPLETED, "fail undid a completed task"
        else:
            reduces_before = tt.progress()[1]
            tt.reset_worker(rng.choice(workers))
            assert tt.progress()[1] == reduces_before, "a worker loss undid a completed reduce"
        check_counts(tt)


def check_counts(tt):
    """Checks that the progress counters match the task states, and that
    exactly the completed map tasks have outputs."""

    def count(tasks):
        for ts in tasks:
            assert (ts.state == IN_PROGRESS) == bool(ts.running) and len(ts.running) <= 2
        return sum(ts.state == COMPLETED for ts in tasks)

    assert tt.progress() == (count(tt._maps), count(tt._reduces))
    for ts in tt._maps:
        assert (ts.state == COMPLETED) == bool(ts.outputs)


def test_registry():
    r = Registry("w")
    given = types_pb2.WorkerInfo(shuffle_address="a:1", slots=2)
    a = r.register(given, 0.0)
    given.slots = 5  # the registry copied it
    b = r.register(types_pb2.WorkerInfo(shuffle_address="b:1"), 0.0)
    assert a != b and len(r) == 2
    w = r.lookup(a)
    assert w is not None and w.info.slots == 2
    w.last_seen = 99.0  # a copy: the registry keeps its own
    w.info.slots = 7
    r.all()[0].info.slots = 8
    assert r.lookup(a).last_seen == 0.0 and r.lookup(a).info.slots == 2
    assert [w.id for w in r.all()] == [a, b]

    assert r.heartbeat(a, 10.0)
    assert r.expire(15.0, 10.0) == [b]
    assert not r.heartbeat(b, 16.0), "heartbeat from an expired worker accepted"
    assert r.lookup(b) is None
    assert r.register(types_pb2.WorkerInfo(shuffle_address="b:1"), 0.0) != b


def test_registry_lists_in_registration_order():
    # Like Go's All, which orders w2 before w10.
    r = Registry("w")
    ids = [r.register(types_pb2.WorkerInfo(), 0.0) for _ in range(11)]
    assert [w.id for w in r.all()] == ids
    assert r.expire(1.0, 0.5) == sorted(ids)


def test_reset_worker_spares_other_workers():
    # Losing w1 stops its running map and re-runs its finished map, but leaves
    # w2's finished map and running attempts alone.
    tt = TaskTable(3, 1)
    tt.complete(M0, assign(tt, M0, "w2"), "w2", outputs(0, 1))
    tt.complete(M1, assign(tt, M1, "w1"), "w1", outputs(1, 1))
    m2 = Task(MAP, 2)
    assign(tt, m2, "w1")
    assert tt.reset_worker("w1") == [M1, m2]
    assert tt.state(M0) == COMPLETED and tt.progress() == (1, 0)
    assert tt.state(m2) == IDLE
    assert not tt.done()


def test_counts_accumulate():
    # Every counter counts past one.
    tt = TaskTable(1, 2)
    for n in range(2):
        for _ in range(SKIP_AFTER):
            tt.fail(M0, assign(tt, M0, "w1"), "w1", crash(n))
    assert tt.failures(M0) == 4 and tt.skipped() == 2
    tt.complete(M0, assign(tt, M0, "w1"), "w1", outputs(0, 2))
    for r in (R0, R1):
        assign(tt, r, "w1")
        assert tt.complete(r, assign(tt, r, "w2"), "w2")
    assert tt.progress() == (1, 2) and tt.backups() == (2, 2) and tt.done()


def test_done_needs_every_reduce():
    tt = TaskTable(1, 1)
    tt.complete(M0, assign(tt, M0, "w1"), "w1", outputs(0, 1))
    assert not tt.done()


def test_backup_tie_goes_to_the_lowest_index():
    tt = TaskTable(2, 1)
    tt.assign(M1, "w1", 5.0)
    tt.assign(M0, "w2", 5.0)
    assert tt.backup("w3") == M0


@pytest.mark.parametrize(
    "bad, message",
    [
        (outputs(0, 1), "map 0 reported 1 outputs, want 2"),
        (outputs(1, 2), "map 0 reported output 0 as map 1 partition 0"),
        ([None, None], "map 0 reported output 0 with no location"),
    ],
)
def test_bad_outputs_say_why(bad, message):
    tt = TaskTable(1, 2)
    a = assign(tt, M0, "w1")
    with pytest.raises(ValueError, match=f"^coordinator: {message}$"):
        tt.complete(M0, a, "w1", bad)


def test_registry_ids_and_expiry_boundary():
    r = Registry("worker-")
    assert [r.register(types_pb2.WorkerInfo(), 0.0) for _ in range(3)] == [
        "worker-1",
        "worker-2",
        "worker-3",
    ]
    assert r.expire(10.0, 10.0) == [], "a worker seen exactly timeout ago is still live"
    assert len(r) == 3
