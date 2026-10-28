"""Checks the partition function against the shared vectors in testdata/hash/.

Go is tested against the same file, so passing both means the two languages
send every key to the same reducer.
"""

import base64
import json
from pathlib import Path

import pytest

from mapreduce_reloaded.partition import fnv1a64, partition

REPO = next(p for p in Path(__file__).resolve().parents if (p / "testdata").is_dir())
MANIFEST = json.loads((REPO / "testdata" / "hash" / "vectors.json").read_text())
VECTORS = MANIFEST["vectors"]


def test_manifest():
    assert MANIFEST["algorithm"] == "fnv1a64"
    assert VECTORS


@pytest.mark.parametrize("v", VECTORS, ids=[v["name"] for v in VECTORS])
def test_vector(v):
    key = base64.b64decode(v["key_b64"])
    assert f"{fnv1a64(key):016x}" == v["hash"]
    for r, want in v["partitions"].items():
        assert partition(key, int(r)) == want


def test_bytes_like_keys_agree():
    key = b"foobar"
    assert fnv1a64(bytearray(key)) == fnv1a64(memoryview(key)) == fnv1a64(key)


def test_str_key_rejected():
    with pytest.raises(TypeError):
        fnv1a64("foobar")


@pytest.mark.parametrize("r", [0, -1])
def test_bad_reducer_count(r):
    with pytest.raises(ValueError):
        partition(b"a", r)


def test_result_in_range():
    for i in range(1000):
        assert 0 <= partition(str(i).encode(), 7) < 7
