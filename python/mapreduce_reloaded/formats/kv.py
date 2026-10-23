"""Reader and writer for MRKV files, the on-disk key/value format.

Specified in docs/file-format.md and byte-compatible with the Go package
go/internal/formats/kv. Both are tested against testdata/format/.

    with open(path, "wb") as f, kv.Writer(f) as w:
        w.write(b"hello", b"1")

    with open(path, "rb") as f:
        for key, value in kv.read(f):
            ...
"""

import struct
from collections.abc import Iterator
from typing import BinaryIO

BLOCK_TARGET = 64 * 1024
MAX_BLOCK_SIZE = 64 * 1024 * 1024

_MAGIC = b"MRKV"
_VERSION = 1
_CODEC_NONE = 0
_U32 = struct.Struct("<I")
_BLOCK = struct.Struct("<III")  # stored_len, record_count, checksum
_TOTALS = struct.Struct("<QQ")  # total_records, block_count


class FormatError(Exception):
    """The file violates the spec. `kind` is one of the spec's error kinds."""

    def __init__(self, kind: str, detail: str = ""):
        super().__init__(f"{kind}: {detail}" if detail else kind)
        self.kind = kind


def _crc32c_table() -> list[int]:
    table = []
    for byte in range(256):
        crc = byte
        for _ in range(8):
            crc = (crc >> 1) ^ 0x82F63B78 if crc & 1 else crc >> 1
        table.append(crc)
    return table


_TABLE = _crc32c_table()


def crc32c(data: bytes) -> int:
    """CRC-32C (Castagnoli). The standard library only has CRC-32 (IEEE)."""
    crc = 0xFFFFFFFF
    for byte in data:
        crc = _TABLE[(crc ^ byte) & 0xFF] ^ (crc >> 8)
    return crc ^ 0xFFFFFFFF


def _uvarint(n: int) -> bytes:
    out = bytearray()
    while n >= 0x80:
        out.append((n & 0x7F) | 0x80)
        n >>= 7
    out.append(n)
    return bytes(out)


class Writer:
    """Writes records to a binary file, buffering at most one block.

    Used as a context manager, the footer is written on a clean exit and
    omitted if an exception escapes, so a half-written file reads as
    truncated. Neither close() nor the context manager closes the file.
    """

    def __init__(self, f: BinaryIO, *, block_target: int = BLOCK_TARGET):
        self._f = f
        self._target = block_target
        self._payload = bytearray()
        self._count = self._records = self._blocks = 0
        self._closed = False
        f.write(_MAGIC + bytes([_VERSION, _CODEC_NONE, 0, 0]))

    def write(self, key: bytes, value: bytes) -> None:
        if self._closed:
            raise ValueError("write after close")
        record = _uvarint(len(key)) + key + _uvarint(len(value)) + value
        if len(record) > MAX_BLOCK_SIZE:
            raise ValueError(f"record of {len(record)} bytes exceeds MAX_BLOCK_SIZE")
        if self._count and len(self._payload) + len(record) > MAX_BLOCK_SIZE:
            self._flush()
        self._payload += record
        self._count += 1
        if len(self._payload) >= self._target:
            self._flush()

    def close(self) -> None:
        """Flushes the last block and writes the footer."""
        if self._closed:
            return
        if self._count:
            self._flush()
        totals = _TOTALS.pack(self._records, self._blocks)
        self._f.write(_U32.pack(0) + totals + _U32.pack(crc32c(totals)))
        self._closed = True

    def _flush(self) -> None:
        payload = bytes(self._payload)
        self._f.write(_BLOCK.pack(len(payload), self._count, crc32c(payload)) + payload)
        self._records += self._count
        self._blocks += 1
        self._payload.clear()
        self._count = 0

    def __enter__(self) -> "Writer":
        return self

    def __exit__(self, exc_type, exc, tb) -> None:
        if exc_type is None:
            self.close()


def read(f: BinaryIO) -> Iterator[tuple[bytes, bytes]]:
    """Yields the (key, value) records of an MRKV file.

    Each block's checksum is verified before its records are yielded, but
    the file is only known to be valid once iteration ends without error.
    Raises FormatError on any spec violation.
    """
    header = _read_exact(f, 8)
    if header[:4] != _MAGIC:
        raise FormatError("bad_magic")
    if header[4] != _VERSION:
        raise FormatError("unsupported_version", str(header[4]))
    if header[5] != _CODEC_NONE:
        raise FormatError("unsupported_codec", str(header[5]))
    if header[6:] != b"\x00\x00":
        raise FormatError("bad_header", "reserved bytes not zero")

    records = blocks = 0
    while size := _U32.unpack(_read_exact(f, 4))[0]:
        if size > MAX_BLOCK_SIZE:
            raise FormatError("bad_block", f"block {blocks} is {size} bytes")
        count, checksum = struct.unpack("<II", _read_exact(f, 8))
        payload = _read_exact(f, size)
        if crc32c(payload) != checksum:
            raise FormatError("checksum_mismatch", f"block {blocks}")
        if count == 0:
            raise FormatError("bad_block", f"block {blocks} has record_count 0")
        yield from _records(payload, count, blocks)
        records += count
        blocks += 1

    totals = _read_exact(f, 16)
    if crc32c(totals) != _U32.unpack(_read_exact(f, 4))[0]:
        raise FormatError("checksum_mismatch", "footer")
    if _TOTALS.unpack(totals) != (records, blocks):
        raise FormatError("count_mismatch", f"read {records} records in {blocks} blocks")
    if f.read(1):
        raise FormatError("trailing_data")


def _records(payload: bytes, count: int, block: int) -> Iterator[tuple[bytes, bytes]]:
    pos = 0
    for _ in range(count):
        key, pos = _field(payload, pos, block)
        value, pos = _field(payload, pos, block)
        yield key, value
    if pos != len(payload):
        raise FormatError("bad_block", f"block {block}: bytes left over after last record")


def _field(buf: bytes, pos: int, block: int) -> tuple[bytes, int]:
    """Decodes one length-prefixed byte string starting at pos."""
    n = shift = 0
    for i in range(10):
        if pos + i >= len(buf):
            break
        byte = buf[pos + i]
        n |= (byte & 0x7F) << shift
        shift += 7
        if not byte & 0x80:
            start = pos + i + 1
            if n >= 1 << 64 or start + n > len(buf):
                break
            return buf[start : start + n], start + n
    raise FormatError("bad_block", f"block {block}: record runs past end of payload")


def _read_exact(f: BinaryIO, n: int) -> bytes:
    data = f.read(n)
    if len(data) != n:
        raise FormatError("truncated")
    return data
