"""An append-only log of records, kept in one file, mirroring
go/internal/wal byte for byte, so either coordinator can recover the
other's log. The coordinator writes each decision to it before acting on
it, so that a restarted coordinator can replay them (§3.3).

Each record is framed as a little-endian uint32 length, a CRC-32C of the
payload, and the payload. Every append is synced to disk before it returns.
A crash can leave the last record incomplete: open drops such a torn tail,
but reports damage anywhere else as an error.

compact rewrites the log with only the records a caller keeps, into a
temporary file that replaces the log by a rename, so a crash leaves either
the old log or the new one, never a mix.
"""

import os
import struct
from collections.abc import Callable

from mapreduce_reloaded.formats.kv import crc32c

_HEADER = struct.Struct("<II")

# Names the temporary file compact writes next to the log.
COMPACT_SUFFIX = ".compact"

_MAX_RECORD = 0xFFFFFFFF


class CorruptError(Exception):
    """A record is damaged but is not the last one in the file."""


class Log:
    """An open log file, positioned to append."""

    def __init__(self, path: str, f):
        self._path = path
        self._f = f
        self._err: Exception | None = None  # sticky: after a failed append, the end is unknown

    @classmethod
    def open(cls, path: str, replay: Callable[[bytes], None]) -> "Log":
        """Opens the log at path, creating it if needed, and calls replay with
        each record's payload in order. A torn last record is removed from
        the file. Raises CorruptError for damage before the last record, and
        whatever replay raises."""
        # A compaction that crashed before its rename left the log untouched.
        try:
            os.remove(path + COMPACT_SUFFIX)
        except FileNotFoundError:
            pass
        f = os.fdopen(os.open(path, os.O_RDWR | os.O_CREAT, 0o644), "r+b", buffering=0)
        try:
            end = _read_all(f, replay)
            f.truncate(end)
            f.seek(end)
            _sync_dir(path)
        except BaseException:
            f.close()
            raise
        return cls(path, f)

    def append(self, payload: bytes):
        """Writes a non-empty record and syncs it to disk. After an error,
        every later append fails too."""
        _check(payload)
        if self._err is not None:
            raise self._err
        try:
            self._write(payload)
            os.fsync(self._f.fileno())
        except Exception as e:
            self._err = e
            raise

    def compact(self, keep: Callable[[bytes], bool]):
        """Rewrites the log with the records for which keep returns True, in
        order; appends then go on after them. If it fails before the new log
        replaces the old one, the old log is left as it was and stays
        usable; if it fails after, every later append fails too."""
        if self._err is not None:
            raise self._err
        tmp = self._path + COMPACT_SUFFIX
        out = open(tmp, "wb", buffering=0)  # noqa: SIM115 - closed below, or replaces the log
        try:
            w = Log(tmp, out)

            def copy(payload):
                if keep(payload):
                    w._write(payload)

            _read_all(self._f, copy)
            os.fsync(out.fileno())
            out.close()
            os.replace(tmp, self._path)
        except BaseException:
            out.close()
            try:
                os.remove(tmp)
            except FileNotFoundError:
                pass
            raise
        # The new log is in place; from here a failure leaves the file's end
        # unknown, so the log refuses further appends.
        f = None
        try:
            _sync_dir(self._path)
            f = open(self._path, "r+b", buffering=0)  # noqa: SIM115 - the log's file from now on
            f.seek(0, os.SEEK_END)
        except Exception as e:
            if f is not None:
                f.close()
            self._err = e
            raise
        self._f.close()
        self._f = f

    def close(self):
        """Closes the log file."""
        self._f.close()

    def _write(self, payload: bytes):
        """Frames payload and writes it, without syncing."""
        _check(payload)
        buf = memoryview(_HEADER.pack(len(payload), crc32c(payload)) + payload)
        while buf:
            n = self._f.write(buf)
            if not n:  # 0, or None from a non-blocking file: no progress
                raise OSError(f"wal: write made no progress with {len(buf)} bytes left")
            buf = buf[n:]


def _check(payload: bytes):
    if not payload or len(payload) > _MAX_RECORD:
        raise ValueError(f"wal: record of {len(payload)} bytes")


def _read_all(f, replay: Callable[[bytes], None]) -> int:
    """Replays f's records and returns the offset where the valid records
    end."""
    fd = f.fileno()
    size = os.fstat(fd).st_size
    off = 0
    while off < size:
        if size - off < _HEADER.size:
            return off  # torn header
        n, checksum = _HEADER.unpack(os.pread(fd, _HEADER.size, off))
        end = off + _HEADER.size + n
        if end > size:
            return off  # torn payload
        payload = os.pread(fd, n, off + _HEADER.size)
        if n == 0 or crc32c(payload) != checksum:  # append never writes empty records
            if end == size:
                return off  # torn: the last record was not fully written
            if _zeroed(fd, off, size):
                return off  # torn: the file system extended the file with zeros
            raise CorruptError(f"wal: corrupt record at offset {off}")
        replay(payload)
        off = end
    return off


def _zeroed(fd: int, off: int, size: int) -> bool:
    """Reports whether the file holds only zero bytes from off to size."""
    while off < size:
        chunk = os.pread(fd, min(32 << 10, size - off), off)
        if chunk.count(0) != len(chunk):
            return False
        off += len(chunk)
    return True


def _sync_dir(path: str):
    """Makes the log file's directory entry durable."""
    fd = os.open(os.path.dirname(os.path.abspath(path)), os.O_RDONLY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)
