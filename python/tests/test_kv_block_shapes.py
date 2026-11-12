"""Compressed MRKV blocks written byte by byte, in shapes a compressor may
choose: every width of Snappy copy and a zstd run-length block, with their
damaged forms beside them.

The Go reader's TestCompressedBlockShapes (go/internal/formats/kv) reads the
same bytes. A file one implementation reads and the other refuses would
break a job that mixes them, so both must give the same answer for each.
"""

import io
import struct

import pytest

from mapreduce_reloaded.formats import kv

REC = [(b"k", b"abcdabcd")]  # an 11-byte payload
ONES = [(b"\x01", b"\x01")] * 3  # 12 bytes of 0x01
# The payload's length, 11, then a 7-byte literal: the record up to "abcd".
LITERAL = bytes([11, 6 << 2, 1]) + b"k" + bytes([8]) + b"abcd"


def snappy_block(*copy_tag: int) -> bytes:
    return LITERAL + bytes(copy_tag)


def zstd_frame(content_size: int, block_header: int, data: bytes) -> bytes:
    """A single-segment frame with a one-byte content size and one block."""
    return bytes([0x28, 0xB5, 0x2F, 0xFD, 0x20, content_size, block_header, 0, 0]) + data


def mrkv(codec: int, count: int, payload: bytes) -> bytes:
    block = struct.pack("<III", len(payload), count, kv.crc32c(payload)) + payload
    footer = struct.pack("<IQQ", 0, count, 1)
    return (
        b"MRKV"
        + bytes([1, codec, 0, 0])
        + block
        + footer
        + struct.pack("<I", kv.crc32c(footer[4:]))
    )


SHAPES = {
    # Copies of 4 bytes from 4 bytes back, with a 1-, 2- and 4-byte offset.
    "snappy copy, 1-byte offset": (2, 1, snappy_block(0 << 2 | 1, 4), REC),
    "snappy copy, 2-byte offset": (2, 1, snappy_block(3 << 2 | 2, 4, 0), REC),
    "snappy copy, 4-byte offset": (2, 1, snappy_block(3 << 2 | 3, 4, 0, 0, 0), REC),
    "snappy copy, offset 0 (an S2 repeat)": (2, 1, snappy_block(0 << 2 | 1, 0), None),
    "snappy copy, 2-byte offset 0": (2, 1, snappy_block(3 << 2 | 2, 0, 0), None),
    "snappy copy, 4-byte offset 0": (2, 1, snappy_block(3 << 2 | 3, 0, 0, 0, 0), None),
    "snappy copy from before the start": (2, 1, snappy_block(3 << 2 | 2, 8, 0), None),
    "snappy length prefix that never ends": (2, 1, b"\xff" * 11, None),
    "snappy shorter than its length": (2, 1, LITERAL, None),
    "zstd run-length block": (1, 3, zstd_frame(12, 12 << 3 | 1 << 1 | 1, b"\x01"), ONES),
    "zstd raw block": (1, 3, zstd_frame(12, 12 << 3 | 1, b"\x01" * 12), ONES),
    "zstd content size over the data": (1, 3, zstd_frame(13, 12 << 3 | 1, b"\x01" * 12), None),
    "zstd block cut short": (1, 3, zstd_frame(12, 12 << 3 | 1, b"\x01" * 11), None),
    "zstd frame without its last block": (1, 3, zstd_frame(12, 12 << 3, b"\x01" * 12), None),
    "zstd bytes after a run-length block": (
        1,
        3,
        zstd_frame(12, 12 << 3 | 1 << 1 | 1, b"\x01\x01"),
        None,
    ),
}


@pytest.mark.parametrize("name", SHAPES)
def test_compressed_block_shape(name):
    codec, count, payload, want = SHAPES[name]
    pytest.importorskip("snappy" if codec == 2 else "zstandard")
    data = io.BytesIO(mrkv(codec, count, payload))
    if want is None:
        with pytest.raises(kv.FormatError, match="^bad_block: "):
            list(kv.read(data))
    else:
        assert list(kv.read(data)) == want
