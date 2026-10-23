"""Input and output formats named in a JobSpec (§4.4), mirroring
go/internal/formats: "text" for lines of text and "kv" for MRKV files.
"""

from collections.abc import Iterator
from typing import Protocol

from mapreduce_reloaded.formats.kvformat import KV
from mapreduce_reloaded.formats.text import Pair, Split, Text

__all__ = ["KV", "InputFormat", "OutputFormat", "Split", "Text", "input_format", "output_format"]


class RecordWriter(Protocol):
    def write(self, key: bytes, value: bytes) -> None: ...
    def close(self) -> None: ...


class InputFormat(Protocol):
    def splits(self, path: str, split_size: int) -> list[Split]: ...
    def read(self, split: Split) -> Iterator[Pair]: ...


class OutputFormat(Protocol):
    def create(self, path: str) -> RecordWriter: ...


_FORMATS = {"text": Text(), "kv": KV()}


def input_format(name: str = "") -> InputFormat:
    """Returns the input format with the given name. "" means "text"."""
    return _lookup(name)


def output_format(name: str = "") -> OutputFormat:
    """Returns the output format with the given name. "" means "text"."""
    return _lookup(name)


def _lookup(name: str):
    try:
        return _FORMATS[name or "text"]
    except KeyError:
        raise ValueError(f"unknown format {name!r}") from None
