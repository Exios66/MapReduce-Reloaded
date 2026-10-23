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


def byte_splits(path: str, split_size: int) -> list[Split]:
    """Cuts the file at path into consecutive ranges of split_size bytes."""
    if split_size <= 0:
        raise ValueError("split size must be positive")
    size = os.path.getsize(path)
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
                pos += len(f.readline())
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
