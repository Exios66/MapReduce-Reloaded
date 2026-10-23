"""External merge sort and key grouping, mirroring go/internal/extsort.

Keys compare as raw bytes. Sorting is stable: records with equal keys keep
the order in which they were added, as docs/file-format.md requires.
"""

import heapq
import os
import shutil
import tempfile
from collections.abc import Callable, Iterable, Iterator
from itertools import groupby
from operator import itemgetter

from mapreduce_reloaded.formats import kv

Pair = tuple[bytes, bytes]

DEFAULT_BUDGET = 64 << 20
_RECORD_OVERHEAD = 100  # approximate memory per buffered tuple

_key = itemgetter(0)

CombineFunc = Callable[[Iterator[Pair]], Iterable[Pair]]


class Sorter:
    """Accepts records in any order and returns them sorted by key.

    Keeps about `budget` bytes in memory, spilling sorted runs to a
    temporary directory under `dir` and merging them at the end. If
    `combine` is set, it is applied to each sorted buffer as it is spilled
    (§4.3), and sorted() always spills. Use as a context manager, or call
    close(), to remove the runs.
    """

    def __init__(
        self,
        dir: str | None = None,
        budget: int = DEFAULT_BUDGET,
        combine: CombineFunc | None = None,
    ):
        self._parent = dir
        self._budget = budget
        self._combine = combine
        self.spilled_bytes = 0
        self._buf: list[Pair] = []
        self._used = 0
        self._dir: str | None = None
        self._runs: list[str] = []

    @property
    def runs(self) -> int:
        """The number of runs spilled to disk so far."""
        return len(self._runs)

    def add(self, key: bytes, value: bytes) -> None:
        self._buf.append((key, value))
        self._used += len(key) + len(value) + _RECORD_OVERHEAD
        if self._used >= self._budget:
            self._spill()

    def sorted(self) -> Iterator[Pair]:
        """Returns all records in key order."""
        if not self._runs and not self._combine:
            return iter(sorted(self._buf, key=_key))
        if self._buf:
            self._spill()
        return merge(*(_read_run(path) for path in self._runs))

    def close(self) -> None:
        if self._dir:
            shutil.rmtree(self._dir, ignore_errors=True)

    def __enter__(self) -> "Sorter":
        return self

    def __exit__(self, *exc) -> None:
        self.close()

    def _spill(self) -> None:
        if self._dir is None:
            self._dir = tempfile.mkdtemp(prefix="extsort-", dir=self._parent)
        path = os.path.join(self._dir, f"run-{len(self._runs):05d}.kv")
        records = iter(sorted(self._buf, key=_key))
        with open(path, "wb") as f:
            with kv.Writer(f) as w:
                for key, value in self._combine(records) if self._combine else records:
                    w.write(key, value)
            self.spilled_bytes += f.tell()
        self._runs.append(path)
        self._buf, self._used = [], 0


def _read_run(path: str) -> Iterator[Pair]:
    with open(path, "rb") as f:
        yield from kv.read(f)


def merge(*streams: Iterable[Pair]) -> Iterator[Pair]:
    """Merges sorted streams. Equal keys come out in stream order."""
    return heapq.merge(*streams, key=_key)


def groups(records: Iterable[Pair]) -> Iterator[tuple[bytes, Iterator[bytes]]]:
    """Yields each distinct key of a sorted stream with an iterator over its
    values. Values left unread are skipped when the next key is requested."""
    for key, group in groupby(records, key=_key):
        yield key, (value for _, value in group)
