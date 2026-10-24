"""Regenerates the MRKV golden vectors in testdata/format/.

Writes one .mrkv file per vector, plus vectors.json describing what a correct
reader must return for each: the records (base64-encoded) or an error kind.
Only needed after changing the spec or adding a vector.
"""

import base64
import json
import struct
from pathlib import Path

import mrkv_reference as mrkv
import zstandard

OUT = Path(__file__).resolve().parent.parent / "testdata" / "format"


def b64(data):
    """Returns the bytes as an ASCII base64 string for the JSON manifest."""
    return base64.b64encode(data).decode("ascii")


ONE = [(b"hello", b"1")]
MANY = [(f"key-{i:03d}".encode(), f"value-{i}".encode()) for i in range(50)]
BINARY = [
    (b"", b"empty key"),
    (b"\x00\x01", b""),
    ("日本".encode(), b"\xff\xfe"),
    (b"z" * 200, b"key longer than the block target"),
]

# A small block target forces several blocks out of a few records.
SMALL_TARGET = 64
BLOCK_TARGETS = {"many_records": SMALL_TARGET, "binary_keys": SMALL_TARGET}

# Compressed files are not byte-reproducible, so these have no block target.
CODECS = {"zstd_records": mrkv.CODEC_ZSTD, "snappy_records": mrkv.CODEC_SNAPPY}

one_bytes = mrkv.encode(ONE)
many_bytes = mrkv.encode(MANY, block_target=SMALL_TARGET)

# Corruptions, each derived from a valid file.
corrupt = bytearray(one_bytes)
corrupt[8 + 12] ^= 0x01  # flip a bit in the first payload byte
first_block_len = struct.unpack_from("<I", many_bytes, 8)[0]
second = 8 + 12 + first_block_len
second_len = struct.unpack_from("<I", many_bytes, second)[0]
missing_block = many_bytes[:second] + many_bytes[second + 12 + second_len :]


def raw_file(codec, payload, count=1):
    """A one-block file with the given stored payload and a valid checksum and footer."""
    totals = struct.pack("<QQ", count, 1)
    return (
        b"MRKV"
        + bytes([1, codec, 0, 0])
        + struct.pack("<III", len(payload), count, mrkv.crc32c(payload))
        + payload
        + struct.pack("<I", 0)
        + totals
        + struct.pack("<I", mrkv.crc32c(totals))
    )


# zstd payloads that are not exactly one frame with its content size.
hello = b"\x05hello\x011"  # the record ("hello", "1")
two_frames = zstandard.ZstdCompressor().compress(hello) * 2
no_size = zstandard.ZstdCompressor(write_content_size=False).compress(hello)
empty_frame = zstandard.ZstdCompressor().compress(b"")  # decodes to nothing

# A snappy block that declares 64 MiB + 1 decoded bytes.
too_large = mrkv._uvarint(mrkv.MAX_BLOCK_SIZE + 1) + b"junk"

VECTORS = {
    "empty": (mrkv.encode([]), []),
    "one_record": (one_bytes, ONE),
    "many_records": (many_bytes, MANY),
    "binary_keys": (mrkv.encode(BINARY, block_target=SMALL_TARGET), BINARY),
    "corrupt_block": (bytes(corrupt), "checksum_mismatch"),
    "truncated": (many_bytes[:-10], "truncated"),
    "missing_block": (missing_block, "count_mismatch"),
    "bad_magic": (b"MRKX" + one_bytes[4:], "bad_magic"),
    "trailing_data": (one_bytes + b"\x00", "trailing_data"),
    "zstd_records": (
        mrkv.encode(MANY, block_target=SMALL_TARGET, codec=mrkv.CODEC_ZSTD),
        MANY,
    ),
    "snappy_records": (
        mrkv.encode(BINARY, block_target=SMALL_TARGET, codec=mrkv.CODEC_SNAPPY),
        BINARY,
    ),
    "zstd_bad_payload": (raw_file(mrkv.CODEC_ZSTD, b"not zstd"), "bad_block"),
    "zstd_two_frames": (raw_file(mrkv.CODEC_ZSTD, two_frames, count=2), "bad_block"),
    "zstd_trailing_empty_frame": (
        raw_file(mrkv.CODEC_ZSTD, zstandard.ZstdCompressor().compress(hello) + empty_frame),
        "bad_block",
    ),
    "zstd_no_content_size": (raw_file(mrkv.CODEC_ZSTD, no_size), "bad_block"),
    "snappy_too_large": (raw_file(mrkv.CODEC_SNAPPY, too_large), "bad_block"),
    "unknown_codec": (one_bytes[:5] + b"\x03" + one_bytes[6:], "unsupported_codec"),
}

manifest = {}
for name, (data, expected) in VECTORS.items():
    (OUT / f"{name}.mrkv").write_bytes(data)
    entry = {"file": f"{name}.mrkv"}
    if isinstance(expected, str):
        entry["error"] = expected
    else:
        if name in CODECS:
            entry["codec"] = CODECS[name]
        else:
            # Writers given this block target must reproduce the file exactly.
            entry["block_target"] = BLOCK_TARGETS.get(name, mrkv.DEFAULT_BLOCK_TARGET)
        entry["records"] = [[b64(k), b64(v)] for k, v in expected]
    manifest[name] = entry

(OUT / "vectors.json").write_text(json.dumps(manifest, indent=2) + "\n")
print(f"wrote {len(VECTORS)} vectors to {OUT}")
