"""Tests for the write-ahead log, ported from go/internal/wal/wal_test.go."""

import os
import struct

import pytest

from mapreduce_reloaded import wal
from mapreduce_reloaded.formats.kv import crc32c
from mapreduce_reloaded.wal import COMPACT_SUFFIX, CorruptError, Log

HEADER = 8


def open_log(path):
    """Opens the log at path and returns it with the records it replayed."""
    got = []
    log = Log.open(str(path), lambda p: got.append(p.decode()))
    return log, got


def append_all(log, *records):
    for r in records:
        log.append(r.encode())


def test_format():
    """The framing Go's wal writes: little-endian length, CRC-32C, payload."""
    assert struct.pack("<II", 1, crc32c(b"a")) + b"a" == bytes.fromhex("010000003043d0c1") + b"a"


def test_format_on_disk(tmp_path):
    log, _ = open_log(tmp_path / "log")
    append_all(log, "a", "bb")
    log.close()
    want = b"".join(struct.pack("<II", len(r), crc32c(r)) + r for r in (b"a", b"bb"))
    assert (tmp_path / "log").read_bytes() == want


def test_reopen(tmp_path):
    path = tmp_path / "log"
    log, got = open_log(path)
    assert got == []
    append_all(log, "a", "bb")
    log.close()
    log, got = open_log(path)
    assert got == ["a", "bb"]
    append_all(log, "ccc")
    log.close()
    log, got = open_log(path)
    assert got == ["a", "bb", "ccc"]
    log.close()


def test_torn_tail(tmp_path):
    whole = tmp_path / "whole"
    log, _ = open_log(whole)
    append_all(log, "first", "second")
    log.close()
    data = whole.read_bytes()
    first_end = HEADER + len("first")
    flipped = bytearray(data)
    flipped[-1] ^= 1
    zeroed = data[:first_end] + bytes(HEADER)
    for name, content in {
        "short header": data[: first_end + HEADER - 1],
        "short payload": data[:-1],
        "bad checksum": bytes(flipped),
        "zeroed": zeroed,
        "long zeroed": zeroed + bytes(3 * HEADER),
    }.items():
        path = tmp_path / name
        path.write_bytes(content)
        log, got = open_log(path)
        assert got == ["first"], name
        assert os.path.getsize(path) == first_end, name
        append_all(log, "third")
        log.close()
        log, got = open_log(path)
        assert got == ["first", "third"], name
        log.close()


def test_corrupt(tmp_path):
    path = tmp_path / "log"
    log, _ = open_log(path)
    append_all(log, "first", "second")
    log.close()
    data = bytearray(path.read_bytes())
    data[HEADER] ^= 1
    path.write_bytes(bytes(data))
    with pytest.raises(CorruptError, match="^wal: corrupt record at offset 0$"):
        Log.open(str(path), lambda p: None)


def test_damage_followed_by_data_is_corrupt(tmp_path):
    """Zeros mid-file are a torn tail only if nothing but zeros follows."""
    path = tmp_path / "log"
    log, _ = open_log(path)
    append_all(log, "first", "second")
    log.close()
    data = path.read_bytes()
    path.write_bytes(data[: HEADER + 5] + bytes(HEADER) + b"x")
    with pytest.raises(CorruptError, match=f"offset {HEADER + 5}$"):
        Log.open(str(path), lambda p: None)


def test_errors(tmp_path):
    path = tmp_path / "log"
    log, _ = open_log(path)
    append_all(log, "a")
    with pytest.raises(ValueError, match="^wal: record of 0 bytes$"):
        log.append(b"")

    class Boom(Exception):
        pass

    def boom(_):
        raise Boom

    with pytest.raises(Boom):
        Log.open(str(path), boom)
    log.close()
    with pytest.raises(ValueError):
        log.append(b"b")  # a closed log
    with pytest.raises(ValueError):
        log.append(b"c")  # after a failed append


def test_failed_append_is_sticky(tmp_path, monkeypatch):
    log, _ = open_log(tmp_path / "log")

    def fail(_):
        raise OSError("disk full")

    monkeypatch.setattr(os, "fsync", fail)
    with pytest.raises(OSError, match="disk full"):
        log.append(b"a")
    monkeypatch.undo()
    with pytest.raises(OSError, match="disk full"):
        log.append(b"b")
    with pytest.raises(OSError, match="disk full"):
        log.compact(lambda p: True)
    log.close()


def test_short_writes_are_finished(tmp_path):
    log, _ = open_log(tmp_path / "log")
    real = log._f

    class Trickle:
        def write(self, b):
            return real.write(bytes(b[:1]))

        def fileno(self):
            return real.fileno()

        def close(self):
            real.close()

    log._f = Trickle()
    append_all(log, "abc")
    log.close()
    _, got = open_log(tmp_path / "log")
    assert got == ["abc"]


def test_a_write_without_progress_fails(tmp_path):
    """A write that takes nothing fails the append instead of looping."""
    log, _ = open_log(tmp_path / "log")
    real = log._f

    class Stuck:
        def write(self, b):
            return 0

        def fileno(self):
            return real.fileno()

        def close(self):
            real.close()

    log._f = Stuck()
    with pytest.raises(OSError, match="no progress"):
        log.append(b"abc")
    with pytest.raises(OSError, match="no progress"):  # and every later one
        log.append(b"d")
    log.close()


def test_compact_closes_the_new_file_if_seek_fails(tmp_path, monkeypatch):
    path = tmp_path / "log"
    log, _ = open_log(path)
    append_all(log, "a", "b")
    opened = []
    real_open = open

    class NoSeek:
        def __init__(self, f):
            self.f = f

        def seek(self, *a):
            raise OSError("no seek")

        def close(self):
            opened.append("closed")
            self.f.close()

    def open_no_seek(file, mode="r", *a, **k):
        f = real_open(file, mode, *a, **k)
        return NoSeek(f) if mode == "r+b" else f

    monkeypatch.setattr(wal, "open", open_no_seek, raising=False)
    with pytest.raises(OSError, match="no seek"):
        log.compact(lambda p: p == b"a")
    monkeypatch.undo()
    assert opened == ["closed"]
    with pytest.raises(OSError, match="no seek"):
        log.append(b"c")
    log.close()


def test_compact(tmp_path):
    path = tmp_path / "log"
    log, _ = open_log(path)
    append_all(log, "a1", "b1", "a2", "b2", "a3")
    before = os.path.getsize(path)
    log.compact(lambda p: p.startswith(b"a"))
    assert os.path.getsize(path) < before
    append_all(log, "c1")
    log.close()
    log, got = open_log(path)
    assert got == ["a1", "a2", "a3", "c1"]
    log.close()
    assert not os.path.exists(str(path) + COMPACT_SUFFIX)


def test_compact_fails(tmp_path):
    path = tmp_path / "log"
    log, _ = open_log(path)
    append_all(log, "a", "b")

    def boom(_):
        raise RuntimeError("boom")

    with pytest.raises(RuntimeError, match="boom"):
        log.compact(boom)
    assert not os.path.exists(str(path) + COMPACT_SUFFIX)
    append_all(log, "c")
    log.close()
    log, got = open_log(path)
    assert got == ["a", "b", "c"]
    log.close()


def test_compact_fails_after_the_rename(tmp_path, monkeypatch):
    """Once the new log is in place, a failure leaves its end unknown, so
    the log refuses further appends."""
    path = tmp_path / "log"
    log, _ = open_log(path)
    append_all(log, "a", "b")
    real = wal._sync_dir

    def fail(p):
        raise OSError("no sync")

    monkeypatch.setattr(wal, "_sync_dir", fail)
    with pytest.raises(OSError, match="no sync"):
        log.compact(lambda p: p == b"a")
    monkeypatch.setattr(wal, "_sync_dir", real)
    with pytest.raises(OSError, match="no sync"):
        log.append(b"c")
    log.close()
    log, got = open_log(path)
    assert got == ["a"]
    log.close()


def test_open_removes_stale_compaction(tmp_path):
    path = tmp_path / "log"
    log, _ = open_log(path)
    append_all(log, "a")
    log.close()
    (tmp_path / ("log" + COMPACT_SUFFIX)).write_bytes(b"partial")
    log, got = open_log(path)
    assert got == ["a"]
    log.close()
    assert not os.path.exists(str(path) + COMPACT_SUFFIX)


@pytest.mark.skipif(not os.path.isdir("/proc/self/fd"), reason="counts open files via /proc")
def test_open_closes_the_file_on_error(tmp_path):
    path = tmp_path / "log"
    log, _ = open_log(path)
    append_all(log, "a")
    log.close()

    def boom(_):
        raise RuntimeError

    before = len(os.listdir("/proc/self/fd"))
    with pytest.raises(RuntimeError):
        Log.open(str(path), boom)
    assert len(os.listdir("/proc/self/fd")) == before


def test_compact_fails_with_its_file_gone(tmp_path):
    path = tmp_path / "log"
    log, _ = open_log(path)
    append_all(log, "a")

    def vanish(_):
        os.remove(str(path) + COMPACT_SUFFIX)
        raise RuntimeError("gone")

    with pytest.raises(RuntimeError, match="gone"):
        log.compact(vanish)
    append_all(log, "b")
    log.close()
    _, got = open_log(path)
    assert got == ["a", "b"]
