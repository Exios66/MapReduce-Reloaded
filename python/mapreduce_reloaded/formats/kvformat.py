"""The "kv" format: MRKV files, mirroring go/internal/formats/kvformat.go.

Blocks cannot be located without reading the file from the start, so each
file is one split.
"""

import os
from collections.abc import Iterator

from mapreduce_reloaded.formats import kv
from mapreduce_reloaded.formats.text import Pair, Split


class KV:
    def splits(self, path: str, split_size: int) -> list[Split]:
        size = os.path.getsize(path)
        return [Split(path, 0, size)] if size else []

    def read(self, split: Split) -> Iterator[Pair]:
        if split.start:
            raise ValueError("kv files cannot be split")
        with open(split.path, "rb") as f:
            yield from kv.read(f)

    def create(self, path: str) -> "KVWriter":
        return KVWriter(path)


class KVWriter(kv.Writer):
    """A kv.Writer that owns its file."""

    def __init__(self, path: str):
        super().__init__(open(path, "wb"))  # closed by close()

    def close(self) -> None:
        super().close()
        self._f.close()

    def __exit__(self, exc_type, exc, tb) -> None:
        super().__exit__(exc_type, exc, tb)
        self._f.close()  # also on error, leaving the file without a footer
