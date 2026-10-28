"""Tests for the MRKV reader and writer.

The golden vectors in testdata/format/ are shared with the Go tests, so
passing both proves the two languages read and write the same bytes.
"""

import base64
import importlib.util
import io
import json
import struct
import sys
from pathlib import Path

import pytest
from hypothesis import given
from hypothesis import strategies as st

from mapreduce_reloaded.formats import kv

REPO = next(p for p in Path(__file__).resolve().parents if (p / "testdata").is_dir())
VECTORS_DIR = REPO / "testdata" / "format"
MANIFEST = json.loads((VECTORS_DIR / "vectors.json").read_text())

_spec = importlib.util.spec_from_file_location(
    "mrkv_reference", REPO / "scripts" / "mrkv_reference.py"
)
mrkv = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(mrkv)


def read_all(data):
    return list(kv.read(io.BytesIO(data)))


def write_all(records, block_target=kv.BLOCK_TARGET, codec=kv.CODEC_NONE):
    buf = io.BytesIO()
    with kv.Writer(buf, block_target=block_target, codec=codec) as w:
        for key, value in records:
            w.write(key, value)
    return buf.getvalue()


@pytest.mark.parametrize("name", sorted(MANIFEST))
def test_golden_vector(name):
    entry = MANIFEST[name]
    data = (VECTORS_DIR / entry["file"]).read_bytes()
    if "error" in entry:
        with pytest.raises(kv.FormatError) as err:
            read_all(data)
        assert err.value.kind == entry["error"]
        return
    records = [(base64.b64decode(k), base64.b64decode(v)) for k, v in entry["records"]]
    assert read_all(data) == records
    if "block_target" in entry:  # compressed vectors are not byte-reproducible
        assert write_all(records, entry["block_target"]) == data


@pytest.mark.parametrize("name", [n for n in sorted(MANIFEST) if "codec" in MANIFEST[n]])
def test_reads_go_compressed_vectors(name):
    # testdata/format/go/ holds the same records compressed by the Go writer.
    data = (VECTORS_DIR / "go" / MANIFEST[name]["file"]).read_bytes()
    records = [(base64.b64decode(k), base64.b64decode(v)) for k, v in MANIFEST[name]["records"]]
    assert data[5] == MANIFEST[name]["codec"]
    assert read_all(data) == records
    assert mrkv.decode(data) == records


# Builders for hand-made malformed files.


def header(version=1, codec=0, reserved=b"\x00\x00"):
    return b"MRKV" + bytes([version, codec]) + reserved


def block(count, payload):
    return struct.pack("<III", len(payload), count, kv.crc32c(payload)) + payload


def footer(records, blocks):
    totals = struct.pack("<QQ", records, blocks)
    return struct.pack("<I", 0) + totals + struct.pack("<I", kv.crc32c(totals))


REC = b"\x01k\x01v"
OK = header()

MALFORMED = {
    "empty input": (b"", "truncated"),
    "short header": (b"MRKV", "truncated"),
    "short non-MRKV": (b"PK\x03\x04\x14", "bad_magic"),
    "version 2": (header(version=2) + footer(0, 0), "unsupported_version"),
    "unknown codec": (header(codec=3) + footer(0, 0), "unsupported_codec"),
    "reserved set": (header(reserved=b"\x00\x01") + footer(0, 0), "bad_header"),
    "no footer": (OK, "truncated"),
    "zero record count": (OK + block(0, REC) + footer(0, 1), "bad_block"),
    "count too high": (OK + block(2, REC) + footer(2, 1), "bad_block"),
    "leftover bytes": (OK + block(1, REC + b"\x00") + footer(1, 1), "bad_block"),
    "length past payload": (OK + block(1, b"\x09k\x00") + footer(1, 1), "bad_block"),
    "varint overflow": (OK + block(1, b"\xff" * 11) + footer(1, 1), "bad_block"),
    "block over limit": (OK + struct.pack("<I", 0xFFFFFFFF), "bad_block"),
    "block past end": (OK + block(1, REC)[:14], "truncated"),
    "short footer": (OK + footer(0, 0)[:10], "truncated"),
    "footer checksum": (OK + footer(0, 0)[:23] + b"\x00", "checksum_mismatch"),
    "block count wrong": (OK + block(1, REC) + footer(1, 2), "count_mismatch"),
}


@pytest.mark.parametrize("name", MALFORMED)
def test_malformed(name):
    data, kind = MALFORMED[name]
    with pytest.raises(kv.FormatError) as err:
        read_all(data)
    assert err.value.kind == kind


CODECS = [kv.CODEC_NONE, kv.CODEC_ZSTD, kv.CODEC_SNAPPY]


@pytest.mark.parametrize("codec", CODECS)
def test_codec_round_trip(codec):
    assert read_all(write_all([], codec=codec)) == []
    # Repetitive records spanning many blocks.
    recs = [(f"key-{i:05d}".encode(), b"v" * 50) for i in range(2000)]
    data = write_all(recs, block_target=1024, codec=codec)
    assert data[5] == codec
    assert read_all(data) == recs
    assert mrkv.decode(data) == recs  # the reference decoder agrees
    if codec != kv.CODEC_NONE:
        assert len(data) < len(write_all(recs, block_target=1024)) / 2


def test_zstd_cross_checks_with_reference():
    recs = [(b"k%d" % i, b"value" * 10) for i in range(100)]
    assert mrkv.decode(write_all(recs, 256, kv.CODEC_ZSTD)) == recs
    assert read_all(mrkv.encode(recs, block_target=256, codec=1)) == recs


def test_unknown_writer_codec():
    with pytest.raises(ValueError):
        kv.Writer(io.BytesIO(), codec=9)


def test_missing_package(monkeypatch):
    monkeypatch.setitem(sys.modules, "zstandard", None)  # makes import fail
    with pytest.raises(ValueError, match="zstd needs the zstandard package"):
        kv.Writer(io.BytesIO(), codec=kv.CODEC_ZSTD)
    with pytest.raises(kv.FormatError, match="unsupported_codec: zstd needs the zstandard"):
        read_all(header(codec=1) + footer(0, 0))


def test_checksum_checked_before_decompression():
    bad = bytearray(block(1, b"not zstd"))
    bad[-1] ^= 1
    with pytest.raises(kv.FormatError) as err:
        read_all(header(codec=1) + bytes(bad) + footer(1, 1))
    assert err.value.kind == "checksum_mismatch"


def test_checksum_checked_before_record_count():
    bad = bytearray(block(0, b"\x00\x00"))
    bad[8] ^= 1
    with pytest.raises(kv.FormatError) as err:
        read_all(OK + bytes(bad) + footer(0, 1))
    assert err.value.kind == "checksum_mismatch"


def test_streams_good_blocks_before_error():
    data = write_all([(b"a", b"1"), (b"b", b"2")], block_target=1)
    it = kv.read(io.BytesIO(data[:-1]))
    assert [next(it), next(it)] == [(b"a", b"1"), (b"b", b"2")]
    with pytest.raises(kv.FormatError, match="truncated"):
        next(it)


def test_writer_omits_footer_on_exception():
    buf = io.BytesIO()
    with pytest.raises(RuntimeError), kv.Writer(buf) as w:
        w.write(b"k", b"v")
        raise RuntimeError
    with pytest.raises(kv.FormatError, match="truncated"):
        read_all(buf.getvalue())


def test_writer_errors():
    w = kv.Writer(io.BytesIO())
    with pytest.raises(TypeError):
        w.write("str key", b"")
    w.close()
    w.close()  # idempotent
    with pytest.raises(ValueError):
        w.write(b"k", b"v")


records = st.lists(st.tuples(st.binary(max_size=300), st.binary(max_size=300)), max_size=50)


@given(records, st.integers(min_value=1, max_value=1024))
def test_round_trip_matches_reference(recs, block_target):
    data = write_all(recs, block_target)
    assert data == mrkv.encode(recs, block_target=block_target)
    assert read_all(data) == recs


def outcome(decode, data):
    try:
        return decode(data)
    except (kv.FormatError, mrkv.MRKVError) as err:
        return err.kind


@given(records, st.data())
def test_corruption_matches_reference(recs, data):
    raw = bytearray(write_all(recs, block_target=64))
    i = data.draw(st.integers(0, len(raw) - 1))
    raw[i] ^= data.draw(st.integers(1, 255))
    assert outcome(read_all, bytes(raw)) == outcome(mrkv.decode, bytes(raw))


class Trickle(io.RawIOBase):
    """An unbuffered stream that moves at most 3 bytes per call."""

    def __init__(self, data=b""):
        self.data, self.pos = bytearray(data), 0

    def readable(self):
        return True

    def writable(self):
        return True

    def readinto(self, b):
        n = min(3, len(b), len(self.data) - self.pos)
        b[:n] = self.data[self.pos : self.pos + n]
        self.pos += n
        return n

    def write(self, b):
        n = min(3, len(b))
        self.data += bytes(b[:n])
        return n


def test_short_reads_and_writes():
    records = [(b"key", b"value"), (b"", b"x" * 100)]
    out = Trickle()
    with kv.Writer(out) as w:
        for key, value in records:
            w.write(key, value)
    assert out.data == write_all(records)
    assert list(kv.read(Trickle(out.data))) == records
