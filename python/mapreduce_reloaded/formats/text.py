"""The "text" format: lines of text, mirroring go/internal/formats/text.go.

As input, each line is a record whose key is the line's byte offset in the
file, in decimal, and whose value is the line without its "\\n" or "\\r\\n".
A line belongs to the split in which it starts, so every line is read
exactly once however the file is split. As output, each record is written
as key, a tab, value and "\\n".
"""

import os
from collections.abc import Iterator
from dataclasses import dataclass

Pair = tuple[bytes, bytes]


@dataclass(frozen=True)
class Split:
    """The byte range [start, end) of one input file: one map task's input."""

    path: str
    start: int
    end: int


# The most map tasks a job may have, as go/internal/formats.MaxSplits: the
# coordinator allocates state for every task before any runs.
MAX_SPLITS = 100_000


def byte_splits(path: str, split_size: int) -> list[Split]:
    """Cuts the file at path into consecutive ranges of split_size bytes.
    Raises ValueError, before building any, if there would be more than
    MAX_SPLITS."""
    if split_size <= 0:
        raise ValueError("split size must be positive")
    size = os.path.getsize(path)
    # Count first, as Go does: a tiny split size on a large file would
    # otherwise build the whole list before any limit could reject it.
    if (n := -(-size // split_size)) > MAX_SPLITS:
        raise ValueError(
            f"formats: {path} would be cut into {n} splits; "
            f"the most is {MAX_SPLITS} (raise the split size)"
        )
    return [Split(path, s, min(s + split_size, size)) for s in range(0, size, split_size)]


class Text:
    def splits(self, path: str, split_size: int) -> list[Split]:
        return byte_splits(path, split_size)

    def read(self, split: Split) -> Iterator[Pair]:
        with open(split.path, "rb") as f:
            # Start one byte early: if that byte ends a line, the line at
            # split.start is ours; otherwise it belongs to the previous split.
            pos = max(split.start - 1, 0)
            f.seek(pos)
            if split.start > 0:
                # The partial line may end only within the split; a line
                # ending later starts beyond it, so none starts in it. Bounding
                # the read keeps every split of a very long line from reading
                # the rest of it.
                limit = split.end - split.start
                skipped = f.readline(limit)
                if not skipped.endswith(b"\n"):
                    return
                pos += len(skipped)
            while pos < split.end and (line := f.readline()):
                yield str(pos).encode(), line.removesuffix(b"\n").removesuffix(b"\r")
                pos += len(line)

    def create(self, path: str) -> "TextWriter":
        return TextWriter(path)


class TextWriter:
    def __init__(self, path: str):
        self._f = open(path, "wb")  # closed by close()

    def write(self, key: bytes, value: bytes) -> None:
        self._f.write(key + b"\t" + value + b"\n")

    def close(self) -> None:
        self._f.close()

    def __enter__(self) -> "TextWriter":
        return self

    def __exit__(self, *exc) -> None:
        self.close()
