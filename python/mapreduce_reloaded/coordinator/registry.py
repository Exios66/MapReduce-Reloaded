"""The worker registry, mirroring go/internal/coordinator/registry.go."""

from dataclasses import dataclass

from mapreduce_reloaded.gen.mapreduce.v1 import types_pb2


@dataclass
class Worker:
    """A registered worker."""

    id: str
    info: types_pb2.WorkerInfo  # shuffle address, host, zone and task slots
    last_seen: float


class Registry:
    """Tracks the live workers. A worker that misses heartbeats for too long
    is expired and forgotten (§3.3); if it comes back, it registers again
    under a new ID, so nothing it reports under the old one is trusted."""

    def __init__(self, prefix: str):
        """Worker IDs are prefix followed by a number."""
        self._workers: dict[str, Worker] = {}
        self._prefix = prefix
        self._next = 0

    def register(self, info: types_pb2.WorkerInfo, now: float) -> str:
        """Adds a worker seen at now and returns its new ID. The registry keeps
        its own copy of info."""
        self._next += 1
        id = f"{self._prefix}{self._next}"
        self._workers[id] = _copy(Worker(id, info, now))
        return id

    def heartbeat(self, id: str, now: float) -> bool:
        """Records that worker id was seen at now. Returns False if the worker
        is unknown, which tells it to register again."""
        w = self._workers.get(id)
        if w is not None:
            w.last_seen = now
        return w is not None

    def lookup(self, id: str) -> Worker | None:
        """Returns a copy of the live worker with the given ID, or None."""
        w = self._workers.get(id)
        return None if w is None else _copy(w)

    def all(self) -> list[Worker]:
        """Returns copies of the live workers, in the order they registered."""
        return [_copy(w) for w in self._workers.values()]

    def expire(self, now: float, timeout: float) -> list[str]:
        """Removes the workers not seen for longer than timeout seconds and
        returns their IDs, sorted."""
        expired = sorted(id for id, w in self._workers.items() if now - w.last_seen > timeout)
        for id in expired:
            del self._workers[id]
        return expired

    def __len__(self) -> int:
        """Returns the number of live workers."""
        return len(self._workers)


def _copy(w: Worker) -> Worker:
    """Returns a copy of w that shares nothing mutable with it."""
    info = types_pb2.WorkerInfo()
    info.CopyFrom(w.info)
    return Worker(w.id, info, w.last_seen)
