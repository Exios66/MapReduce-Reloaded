"""Divides a job's input into the splits of its map tasks (§3.1) and picks
default task counts (§3.5), mirroring go/internal/split."""

import glob
import heapq
import os

from mapreduce_reloaded.formats import InputFormat, Split
from mapreduce_reloaded.formats.text import MAX_SPLITS

# The paper uses splits of 16 to 64 MB (§3.1).
DEFAULT_SIZE = 64 << 20

# R is "a small multiple of the number of worker machines" (§3.5).
REDUCE_TASKS_PER_WORKER = 2


def plan(fmt: InputFormat, patterns, size: int = 0) -> list[Split]:
    """Expands patterns and cuts every file into splits of about size bytes;
    0 means DEFAULT_SIZE. Splits come in file order, and M is their number.
    Raises ValueError once the splits pass MAX_SPLITS."""
    splits: list[Split] = []
    for path in expand(patterns):
        splits += fmt.splits(path, size or DEFAULT_SIZE)
        _check_count(splits)
    return splits


def plan_m(fmt: InputFormat, patterns, m: int) -> list[Split]:
    """Expands patterns and cuts the input into m splits (ADR 0016), shared
    out among the non-empty files by allocate, as go/internal/split.PlanM
    does, so both give the same map tasks. A file whose format cannot be cut
    that finely, a small file or a kv file, gets fewer; M is the number of
    splits returned."""
    if m < 1:
        raise ValueError("m must be at least 1")
    files = [(path, size) for path in expand(patterns) if (size := os.path.getsize(path)) > 0]
    splits: list[Split] = []
    for (path, size), k in zip(files, allocate([size for _, size in files], m), strict=True):
        splits += fmt.splits(path, -(-size // k))
        _check_count(splits)
    return splits


def allocate(sizes: list[int], m: int) -> list[int]:
    """Divides m splits among files of the given positive sizes, in
    proportion to size by largest remainder (ties to the earlier file), then
    gives a file left with none one split, taken from the file with the most.
    The result sums to max(m, len(sizes)), or is empty if sizes is. Mirrors
    go/internal/split.Allocate."""
    if not sizes or m <= len(sizes):
        return [1] * len(sizes)
    total = sum(sizes)
    # quota_i = m * size_i / total: the whole part goes to the file, and the
    # remainders decide who gets the leftovers.
    alloc, rems = zip(*(divmod(m * s, total) for s in sizes), strict=True)
    alloc = list(alloc)
    for i in sorted(range(len(sizes)), key=lambda i: -rems[i])[: m - sum(alloc)]:
        alloc[i] += 1
    # The file with the most is the first in this heap. The others, with
    # one split or none, never have the most: the splits outnumber the files.
    most = [(-a, i) for i, a in enumerate(alloc) if a > 1]
    heapq.heapify(most)
    for i in range(len(alloc)):
        if alloc[i] == 0:
            alloc[i] = 1
            _, j = heapq.heappop(most)
            alloc[j] -= 1
            if alloc[j] > 1:
                heapq.heappush(most, (-alloc[j], j))
    return alloc


def _check_count(splits: list[Split]):
    if len(splits) > MAX_SPLITS:
        raise ValueError(
            f"the input splits into more than {MAX_SPLITS} map tasks ({len(splits)} so far); "
            "raise the split size"
        )


def size_for(patterns, m: int) -> int:
    """Returns the split size that cuts the input into at most m splits per
    file, and about m in total."""
    if m < 1:
        raise ValueError("m must be at least 1")
    total = sum(os.path.getsize(path) for path in expand(patterns))
    return max(-(-total // m), 1)


def default_r(workers: int) -> int:
    """Returns the default number of reduce tasks for a cluster of workers."""
    return max(workers, 1) * REDUCE_TASKS_PER_WORKER


def expand(patterns) -> list[str]:
    """Resolves file names and glob patterns to files, sorted by bytes within
    each pattern, as Go's filepath.Glob: * matches names that start with a
    dot too. A pattern that matches nothing is an error."""
    files = []
    for pattern in patterns:
        matches = sorted(glob.glob(pattern, include_hidden=True), key=os.fsencode)
        if not matches:
            raise FileNotFoundError(f"no input matches {pattern!r}")
        files += matches
    return files
