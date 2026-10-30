"""The large-text oracle must reject incomplete preparation and changed output."""

import importlib.util
import io
import json
import subprocess
import sys
from pathlib import Path

import pytest

SPEC = importlib.util.spec_from_file_location(
    "text_data", Path(__file__).resolve().parents[2] / "bench/text-data.py"
)
DATA = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(DATA)


def test_text_shards_and_input_derived_word_oracle(tmp_path):
    text = b"a b a\n\xc3\xa9\tword\r\na\vb\fc\n"
    directory = tmp_path / "input"
    manifest = DATA.prepare(io.BytesIO(text), directory, len(text), 2, 2, "test")
    assert b"".join(p.read_bytes() for p in sorted(directory.glob("part-m-*"))) == text
    assert manifest["records"] == 3
    assert manifest["physical_bytes"] == len(text)
    assert not (directory / "oracle.sqlite").exists()
    output = tmp_path / "output"
    output.mkdir()
    parts = [bytearray(), bytearray()]
    for key, count in sorted({b"a": 3, b"b": 2, b"c": 1, b"word": 1, b"\xc3\xa9": 1}.items()):
        parts[DATA.partition(key, 2)] += key + b"\t" + str(count).encode() + b"\n"
    for index, content in enumerate(parts):
        (output / f"part-r-{index:05d}").write_bytes(content)
    assert DATA.verify(manifest, output)["verified"]
    (output / "part-r-00000").write_bytes(b"wrong\t1\n")
    with pytest.raises(ValueError, match="differs"):
        DATA.verify(manifest, output)
    with pytest.raises(FileExistsError):
        DATA.prepare(io.BytesIO(text), directory, len(text), 2, 2, "test")


@pytest.mark.parametrize("text,target", [(b"a\n", 100), (b"no newline", 10)])
def test_failed_text_preparation_does_not_publish_manifest(tmp_path, text, target):
    directory = tmp_path / "input"
    with pytest.raises(ValueError):
        DATA.prepare(io.BytesIO(text), directory, target, 2, 2, "test")
    assert not (directory / "manifest.json").exists()


def test_text_prepare_cli(tmp_path):
    result = subprocess.run(
        [sys.executable, SPEC.origin, "prepare", "4", "2", "2", "test", str(tmp_path / "in")],
        input=b"a\nb\n",
        capture_output=True,
        check=True,
    )
    assert json.loads(result.stdout)["records"] == 2
