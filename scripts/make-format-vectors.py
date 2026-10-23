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

OUT = Path(__file__).resolve().parent.parent / "testdata" / "format"


def b64(data):
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

one_bytes = mrkv.encode(ONE)
many_bytes = mrkv.encode(MANY, block_target=SMALL_TARGET)

# Corruptions, each derived from a valid file.
corrupt = bytearray(one_bytes)
corrupt[8 + 12] ^= 0x01  # flip a bit in the first payload byte
first_block_len = struct.unpack_from("<I", many_bytes, 8)[0]
second = 8 + 12 + first_block_len
second_len = struct.unpack_from("<I", many_bytes, second)[0]
missing_block = many_bytes[:second] + many_bytes[second + 12 + second_len :]

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
}

manifest = {}
for name, (data, expected) in VECTORS.items():
    (OUT / f"{name}.mrkv").write_bytes(data)
    entry = {"file": f"{name}.mrkv"}
    if isinstance(expected, str):
        entry["error"] = expected
    else:
        # Writers given this block target must reproduce the file exactly.
        entry["block_target"] = BLOCK_TARGETS.get(name, mrkv.DEFAULT_BLOCK_TARGET)
        entry["records"] = [[b64(k), b64(v)] for k, v in expected]
    manifest[name] = entry

(OUT / "vectors.json").write_text(json.dumps(manifest, indent=2) + "\n")
print(f"wrote {len(VECTORS)} vectors to {OUT}")
