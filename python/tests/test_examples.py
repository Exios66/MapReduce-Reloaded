"""Tests for the example programs other than word count, mirroring the tests
in go/examples."""

from pathlib import Path

import pytest

from mapreduce_reloaded import local, split
from mapreduce_reloaded.api import JobSpec
from mapreduce_reloaded.examples import grep

CORPUS = Path(__file__).resolve().parents[2] / "testdata" / "corpus"
INPUTS = (str(CORPUS / "*.txt"),)


def run(tmp_path, program, m, params):
    out = tmp_path / "out"
    spec = JobSpec(
        program=program,
        inputs=INPUTS,
        output=str(out),
        r=1,
        split_size=split.size_for(INPUTS, m),
        params=params,
    )
    local.run(spec, str(tmp_path))
    return out


@pytest.mark.parametrize("m", [1, 3, 7])
def test_grep(tmp_path, m):
    lines = (
        line.removesuffix(b"\r")
        for f in CORPUS.glob("*.txt")
        for line in f.read_bytes().split(b"\n")
    )
    want = sorted(line + b"\t\n" for line in lines if b"Raven" in line)
    out = run(tmp_path, grep.program, m, {"pattern": "Raven"})
    assert (out / "part-r-00000").read_bytes() == b"".join(want)


def test_grep_no_pattern(tmp_path):
    with pytest.raises(ValueError, match="no pattern"):
        run(tmp_path, grep.program, 1, {})
