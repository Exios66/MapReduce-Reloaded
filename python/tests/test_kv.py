"""Tests for the MRKV reader and writer.

The golden vectors in testdata/format/ are shared with the Go tests, so
passing both proves the two languages read and write the same bytes.
"""

import base64
import importlib.util
import io
import json
import struct
from pathlib import Path

import pytest
from hypothesis import given
from hypothesis import strategies as st

from mapreduce_reloaded.formats import kv

REPO = Path(__file__).resolve().parents[2]
VECTORS_DIR = REPO / "testdata" / "format"
MANIFEST = json.loads((VECTORS_DIR / "vectors.json").read_text())

_spec = importlib.util.spec_from_file_location(
    "mrkv_reference", REPO / "scripts" / "mrkv_reference.py"
)
mrkv = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(mrkv)


def read_all(data):
    return list(kv.read(io.BytesIO(data)))


def write_all(records, block_target=kv.BLOCK_TARGET):
    buf = io.BytesIO()
    with kv.Writer(buf, block_target=block_target) as w:
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
    assert write_all(records, entry["block_target"]) == data


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
    "version 2": (header(version=2) + footer(0, 0), "unsupported_version"),
    "zstd codec": (header(codec=1) + footer(0, 0), "unsupported_codec"),
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
