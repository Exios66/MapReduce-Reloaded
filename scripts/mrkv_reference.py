"""Reference encoder/decoder for the MRKV file format (docs/file-format.md).

This is a deliberately small, literal transcription of the spec. It exists to
generate and check the golden vectors in testdata/format/. The production
readers and writers (issue #8) are written separately and tested against the
same vectors, so a misreading of the spec in one place cannot hide in the
other. Only codec 0 (no compression) is supported.
"""

import struct

MAGIC = b"MRKV"
VERSION = 1
CODEC_NONE = 0
DEFAULT_BLOCK_TARGET = 64 * 1024
MAX_BLOCK_SIZE = 64 * 1024 * 1024


class MRKVError(Exception):
    """A spec violation. `kind` is one of the error kinds in the spec."""

    def __init__(self, kind, detail=""):
        """Stores the error kind and appends nonempty detail to the exception message."""
        super().__init__(f"{kind}: {detail}" if detail else kind)
        self.kind = kind


def _crc32c_table():
    """Returns the 256-entry lookup table for reflected CRC-32C (Castagnoli)."""
    table = []
    for byte in range(256):
        crc = byte
        for _ in range(8):
            crc = (crc >> 1) ^ 0x82F63B78 if crc & 1 else crc >> 1
        table.append(crc)
    return table


_TABLE = _crc32c_table()


def crc32c(data):
    """CRC-32C (Castagnoli), as defined in the spec's Conventions section."""
    crc = 0xFFFFFFFF
    for byte in data:
        crc = _TABLE[(crc ^ byte) & 0xFF] ^ (crc >> 8)
    return crc ^ 0xFFFFFFFF


def _uvarint(n):
    """Encodes a nonnegative integer as unsigned LEB128 bytes.

    Raises ValueError for a negative integer. No 64-bit limit is enforced.
    """
    out = bytearray()
    while n >= 0x80:
        out.append((n & 0x7F) | 0x80)
        n >>= 7
    out.append(n)
    return bytes(out)


def _read_uvarint(buf, pos):
    """Returns an unsigned LEB128 value and the byte offset immediately after it.

    Reads from the zero-based byte offset pos. Raises MRKVError with kind
    'bad_block' if the buffer ends before a terminating byte or no terminating
    byte occurs within 10 bytes. No 64-bit value limit is enforced.
    """
    result = shift = 0
    for i in range(10):
        if pos + i >= len(buf):
            raise MRKVError("bad_block", "varint runs past end of payload")
        byte = buf[pos + i]
        result |= (byte & 0x7F) << shift
        if not byte & 0x80:
            return result, pos + i + 1
        shift += 7
    raise MRKVError("bad_block", "varint longer than 10 bytes")


def encode(records, block_target=DEFAULT_BLOCK_TARGET):
    """Returns an uncompressed MRKV file as bytes from (key, value) byte pairs.

    Preserves input order without sorting. block_target is the payload size
    threshold in bytes, checked after each complete record, so blocks may
    exceed it. A nonpositive target puts each record in its own block. Empty
    input produces a header and footer with no blocks.

    Raises struct.error if a block's payload length or record count exceeds
    the format's unsigned 32-bit fields, or footer totals exceed 64-bit fields.
    """
    out = bytearray(MAGIC + bytes([VERSION, CODEC_NONE, 0, 0]))
    blocks = []
    payload, count = bytearray(), 0
    for key, value in records:
        payload += _uvarint(len(key)) + key + _uvarint(len(value)) + value
        count += 1
        if len(payload) >= block_target:
            blocks.append((bytes(payload), count))
            payload, count = bytearray(), 0
    if count:
        blocks.append((bytes(payload), count))

    for payload, count in blocks:
        out += struct.pack("<III", len(payload), count, crc32c(payload)) + payload
    totals = struct.pack("<QQ", sum(c for _, c in blocks), len(blocks))
    out += struct.pack("<I", 0) + totals + struct.pack("<I", crc32c(totals))
    return bytes(out)


def decode(data):
    """Decodes MRKV file bytes into a list of (key, value) byte pairs in file order.

    Supports version 1 with no compression and returns only after validating
    the footer. A valid file with no blocks returns an empty list. Key order
    is not validated.

    Raises MRKVError for invalid or unsupported headers, truncated data,
    checksum mismatches, malformed block records, footer count mismatches,
    or bytes after the footer. Its kind attribute identifies the error using
    the spec's error kinds, including 'bad_block' from varint decoding.
    """
    if data[:4] != MAGIC[: len(data)]:
        raise MRKVError("bad_magic")
    if len(data) < 8:
        raise MRKVError("truncated", "shorter than header")
    if data[4] != VERSION:
        raise MRKVError("unsupported_version", str(data[4]))
    if data[5] != CODEC_NONE:
        raise MRKVError("unsupported_codec", str(data[5]))
    if data[6:8] != b"\x00\x00":
        raise MRKVError("bad_header", "reserved bytes not zero")

    records, blocks, pos = [], 0, 8
    while True:
        if pos + 4 > len(data):
            raise MRKVError("truncated", "missing block or footer")
        (stored_len,) = struct.unpack_from("<I", data, pos)
        if stored_len == 0:
            break
        if stored_len > MAX_BLOCK_SIZE:
            raise MRKVError("bad_block", f"block of {stored_len} bytes exceeds the limit")
        if pos + 12 + stored_len > len(data):
            raise MRKVError("truncated", "block runs past end of file")
        _, count, checksum = struct.unpack_from("<III", data, pos)
        payload = data[pos + 12 : pos + 12 + stored_len]
        if crc32c(payload) != checksum:
            raise MRKVError("checksum_mismatch", f"block at offset {pos}")
        if count == 0:
            raise MRKVError("bad_block", "record_count is 0")
        p = 0
        for _ in range(count):
            key_len, p = _read_uvarint(payload, p)
            key, p = payload[p : p + key_len], p + key_len
            value_len, p = _read_uvarint(payload, p)
            value, p = payload[p : p + value_len], p + value_len
            if p > len(payload):
                raise MRKVError("bad_block", "record runs past end of payload")
            records.append((bytes(key), bytes(value)))
        if p != len(payload):
            raise MRKVError("bad_block", "bytes left over after last record")
        blocks += 1
        pos += 12 + stored_len

    if pos + 24 > len(data):
        raise MRKVError("truncated", "incomplete footer")
    totals = data[pos + 4 : pos + 20]
    total_records, block_count = struct.unpack("<QQ", totals)
    (footer_checksum,) = struct.unpack_from("<I", data, pos + 20)
    if crc32c(totals) != footer_checksum:
        raise MRKVError("checksum_mismatch", "footer")
    if total_records != len(records) or block_count != blocks:
        raise MRKVError("count_mismatch")
    if pos + 24 != len(data):
        raise MRKVError("trailing_data")
    return records
