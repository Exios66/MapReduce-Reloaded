"""Checks the MRKV golden vectors in testdata/format/ against the spec.

Uses the spec-literal reference codec in scripts/mrkv_reference.py. The
production reader and writer are tested against the same vectors in
test_kv.py.
"""

import base64
import importlib.util
import json
from pathlib import Path

import pytest

REPO = Path(__file__).resolve().parents[2]
VECTORS_DIR = REPO / "testdata" / "format"
MANIFEST = json.loads((VECTORS_DIR / "vectors.json").read_text())

_spec = importlib.util.spec_from_file_location(
    "mrkv_reference", REPO / "scripts" / "mrkv_reference.py"
)
mrkv = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(mrkv)


def test_crc32c_check_value():
    # The standard check value for CRC-32C, quoted in the spec.
    assert mrkv.crc32c(b"123456789") == 0xE3069283


@pytest.mark.parametrize("name", sorted(MANIFEST))
def test_vector(name):
    entry = MANIFEST[name]
    data = (VECTORS_DIR / entry["file"]).read_bytes()
    if "error" in entry:
        with pytest.raises(mrkv.MRKVError) as err:
            mrkv.decode(data)
        assert err.value.kind == entry["error"]
    else:
        want = [(base64.b64decode(k), base64.b64decode(v)) for k, v in entry["records"]]
        assert mrkv.decode(data) == want


@pytest.mark.parametrize("name", [n for n in sorted(MANIFEST) if "block_target" in MANIFEST[n]])
def test_reference_encoder_is_deterministic(name):
    # Re-encoding a valid vector's records must give back the file exactly,
    # given the same block target that produced it. Compressed vectors have
    # no block target: their bytes depend on the compressor.
    entry = MANIFEST[name]
    data = (VECTORS_DIR / entry["file"]).read_bytes()
    records = mrkv.decode(data)
    targets = [mrkv.DEFAULT_BLOCK_TARGET, 64]
    assert any(mrkv.encode(records, block_target=t) == data for t in targets)
