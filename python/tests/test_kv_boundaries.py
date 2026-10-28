"""Exact boundaries of the MRKV format, which mutation testing found no
other test pinning (#161, docs/testing.md)."""

import io
import random
import struct

import pytest

from mapreduce_reloaded.formats import kv


def records(data: bytes) -> list[tuple[bytes, bytes]]:
    return list(kv.read(io.BytesIO(data)))


def write(recs, **opts) -> bytes:
    buf = io.BytesIO()
    with kv.Writer(buf, **opts) as w:
        for key, value in recs:
            w.write(key, value)
    return buf.getvalue()


def block_sizes(data: bytes) -> list[int]:
    """The stored length of each block of an MRKV file."""
    sizes, pos = [], 8
    while size := struct.unpack_from("<I", data, pos)[0]:
        sizes.append(size)
        pos += 12 + size
    return sizes


@pytest.mark.parametrize(
    "n, encoded",
    [
        (0, b"\x00"),
        (127, b"\x7f"),
        (128, b"\x80\x01"),
        (300, b"\xac\x02"),
        (16384, b"\x80\x80\x01"),
    ],
)
def test_uvarint(n, encoded):
    assert kv._uvarint(n) == encoded


def test_lengths_of_128_and_more():
    recs = [(b"k" * 128, b"v" * 128), (b"k", b"v" * 16384)]
    assert records(write(recs)) == recs


def test_record_of_exactly_max_block_size(monkeypatch):
    monkeypatch.setattr(kv, "MAX_BLOCK_SIZE", 1024)
    recs = [(b"k", b"v" * 1020)]  # 1 + 1 + 2 + 1020 bytes
    data = write(recs)
    assert block_sizes(data) == [1024]
    assert records(data) == recs


def test_block_filled_to_exactly_max_block_size(monkeypatch):
    monkeypatch.setattr(kv, "MAX_BLOCK_SIZE", 1024)
    recs = [(b"a", b"x" * 508), (b"b", b"y" * 508)]  # 512 bytes each
    assert block_sizes(write(recs)) == [1024]


def test_compressed_block_of_exactly_max_block_size(monkeypatch):
    value = random.Random(1).randbytes(1024)  # incompressible: zstd adds a few bytes
    stored = block_sizes(write([(b"k", value)], codec=kv.CODEC_ZSTD))[0]
    monkeypatch.setattr(kv, "MAX_BLOCK_SIZE", stored)
    assert block_sizes(write([(b"k", value)], codec=kv.CODEC_ZSTD)) == [stored]


@pytest.mark.parametrize("codec", [kv.CODEC_ZSTD, kv.CODEC_SNAPPY])
def test_declared_size_of_exactly_max_block_size(monkeypatch, codec):
    # 20,000 bytes: a three-byte length in the snappy prefix and in the record.
    monkeypatch.setattr(kv, "MAX_BLOCK_SIZE", 20000)
    recs = [(b"k", b"v" * 19995)]  # 1 + 1 + 3 + 19,995 bytes
    data = write(recs, codec=codec)
    assert records(data) == recs
    monkeypatch.setattr(kv, "MAX_BLOCK_SIZE", 19999)
    with pytest.raises(kv.FormatError) as err:
        records(data)
    assert err.value.kind == "bad_block"


def test_length_prefix_cut_off():
    payload = b"\x80"  # a length that continues past the end of the block
    totals = struct.pack("<QQ", 1, 1)
    data = (
        b"MRKV\x01\x00\x00\x00"
        + struct.pack("<III", len(payload), 1, kv.crc32c(payload))
        + payload
        + struct.pack("<I", 0)
        + totals
        + struct.pack("<I", kv.crc32c(totals))
    )
    with pytest.raises(kv.FormatError) as err:
        records(data)
    assert err.value.kind == "bad_block"
