"""Tests for error paths that the other tests do not reach (#161)."""

import io
import os
import runpy
import sys

import pytest

from mapreduce_reloaded.api import Context, TaskID
from mapreduce_reloaded.examples import termvector
from mapreduce_reloaded.formats import Split, kv
from mapreduce_reloaded.formats.kvformat import KV


@pytest.fixture
def small_blocks(monkeypatch):
    """Lowers MAX_BLOCK_SIZE to 1 KiB, so its checks are cheap to reach."""
    monkeypatch.setattr(kv, "MAX_BLOCK_SIZE", 1024)


def records(data: bytes) -> list[tuple[bytes, bytes]]:
    return list(kv.read(io.BytesIO(data)))


def test_record_over_max_block_size(small_blocks):
    buf = io.BytesIO()
    w = kv.Writer(buf)
    with pytest.raises(ValueError, match="exceeds MAX_BLOCK_SIZE"):
        w.write(b"k", b"v" * 1024)
    w.write(b"k", b"v")  # the writer is still usable
    w.close()
    assert records(buf.getvalue()) == [(b"k", b"v")]


def test_block_closed_before_it_would_exceed_max(small_blocks):
    buf = io.BytesIO()
    with kv.Writer(buf) as w:
        w.write(b"a", b"x" * 600)
        w.write(b"b", b"y" * 600)  # 1,200 bytes together: a block each
    data = buf.getvalue()
    assert records(data) == [(b"a", b"x" * 600), (b"b", b"y" * 600)]
    assert data[-20:-12] == (2).to_bytes(8, "little")  # total_records
    assert data[-12:-4] == (2).to_bytes(8, "little")  # block_count


def test_compressed_block_over_max_block_size(small_blocks):
    pytest.importorskip("zstandard")
    w = kv.Writer(io.BytesIO(), codec=kv.CODEC_ZSTD)
    w.write(b"", os.urandom(1018))  # 1,020 bytes, which zstd cannot shrink
    with pytest.raises(ValueError, match="compressed block of .* exceeds MAX_BLOCK_SIZE"):
        w.close()


def test_bad_snappy_length_prefix():
    pytest.importorskip("snappy")
    with pytest.raises(kv.FormatError, match="bad_block: block 0: bad snappy length prefix"):
        kv._decompress(kv.CODEC_SNAPPY, kv._load(kv.CODEC_SNAPPY), b"\x80" * 10, 0)


def test_write_that_accepts_nothing():
    class Stuck(io.RawIOBase):
        def writable(self):
            return True

        def write(self, b):
            return 0

    with pytest.raises(OSError, match="write accepted no bytes"):
        kv.Writer(Stuck())


def test_kv_files_cannot_be_split(tmp_path):
    path = tmp_path / "in.kv"
    path.write_bytes(b"")
    with pytest.raises(ValueError, match="kv files cannot be split"):
        list(KV().read(Split(str(path), 1, 2)))


def test_side_output_outside_a_task():
    with pytest.raises(RuntimeError, match="side outputs are not available"):
        Context(TaskID("map", 0)).side_output("x")


@pytest.mark.parametrize("vector", [b"term", b"term:", b"term:x"])
def test_termvector_bad_term(vector):
    with pytest.raises(ValueError, match="termvector: bad term"):
        termvector.total([vector])


def test_run_as_module(monkeypatch, capsys):
    monkeypatch.setattr(sys, "argv", ["mapreduce_reloaded", "--help"])
    # as python -m does, rather than over the module other tests imported
    monkeypatch.delitem(sys.modules, "mapreduce_reloaded.__main__", raising=False)
    with pytest.raises(SystemExit) as exit:
        runpy.run_module("mapreduce_reloaded", run_name="__main__")
    assert exit.value.code == 0
    assert "usage" in capsys.readouterr().out
