"""Regenerates the partition-hash vectors in testdata/hash/vectors.json.

The hash is computed here by a loop written straight from the spec in
docs/file-format.md ("Partitioning"), independent of the Go and Python
implementations that are tested against it. Only needed after adding a vector.
"""

import base64
import json
from pathlib import Path

OUT = Path(__file__).resolve().parent.parent / "testdata" / "hash"

OFFSET = 0xCBF29CE484222325
PRIME = 0x100000001B3
MASK = (1 << 64) - 1

# Partition counts checked for every key: the trivial case, a small prime,
# a power of two, a typical job size and a large count.
REDUCERS = [1, 2, 7, 64, 1000, 4294967295]


def fnv1a64(key):
    h = OFFSET
    for b in key:
        h ^= b
        h = (h * PRIME) & MASK
    return h


# Published FNV-1a 64 values; a wrong constant fails here before writing.
assert fnv1a64(b"") == 0xCBF29CE484222325
assert fnv1a64(b"a") == 0xAF63DC4C8601EC8C
assert fnv1a64(b"foobar") == 0x85944171F73967E8

KEYS = {
    "empty": b"",
    "a": b"a",
    "foobar": b"foobar",
    "word": b"apple",
    "utf8_latin": "naïve".encode(),
    "utf8_cjk": "日本語".encode(),
    "utf8_emoji": "🗺️".encode(),
    "nul": b"\x00",
    "high_bytes": b"\x00\xff\x80",
    "all_bytes": bytes(range(256)),
    "long": b"mapreduce " * 100,
}

vectors = []
for name, key in KEYS.items():
    h = fnv1a64(key)
    vectors.append(
        {
            "name": name,
            "key_b64": base64.b64encode(key).decode("ascii"),
            # Hex string: JSON numbers above 2^53 lose precision in many parsers.
            "hash": f"{h:016x}",
            "partitions": {str(r): h % r for r in REDUCERS},
        }
    )

OUT.mkdir(parents=True, exist_ok=True)
manifest = {"algorithm": "fnv1a64", "vectors": vectors}
(OUT / "vectors.json").write_text(json.dumps(manifest, indent=2) + "\n")
print(f"wrote {len(vectors)} vectors to {OUT}")
