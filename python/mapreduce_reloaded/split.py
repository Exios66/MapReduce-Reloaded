"""Divides a job's input into the splits of its map tasks (§3.1) and picks
default task counts (§3.5), mirroring go/internal/split."""

import glob
import os

from mapreduce_reloaded.formats import InputFormat, Split

# The paper uses splits of 16 to 64 MB (§3.1).
DEFAULT_SIZE = 64 << 20

# R is "a small multiple of the number of worker machines" (§3.5).
REDUCE_TASKS_PER_WORKER = 2


def plan(fmt: InputFormat, patterns, size: int = 0) -> list[Split]:
    """Expands patterns and cuts every file into splits of about size bytes;
    0 means DEFAULT_SIZE. Splits come in file order, and M is their number."""
    return [s for path in expand(patterns) for s in fmt.splits(path, size or DEFAULT_SIZE)]


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
    """Resolves file names and glob patterns to files, sorted within each
    pattern. A pattern that matches nothing is an error."""
    files = []
    for pattern in patterns:
        matches = sorted(glob.glob(pattern))
        if not matches:
            raise FileNotFoundError(f"no input matches {pattern!r}")
        files += matches
    return files
