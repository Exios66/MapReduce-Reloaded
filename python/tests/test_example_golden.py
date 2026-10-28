"""Checks every example program but word count against the expected output in
testdata/examples/, which scripts/make-examples-expected.py computes from the
programs' descriptions (#153). go/cmd/mr checks the Go programs against the
same files, and against this implementation byte for byte."""

import json
from pathlib import Path

import pytest

from mapreduce_reloaded import local, split
from mapreduce_reloaded.__main__ import load
from mapreduce_reloaded.api import JobSpec

ROOT = Path(__file__).resolve().parents[2]
GOLDEN = ROOT / "testdata" / "examples"
JOBS = json.loads((GOLDEN / "jobs.json").read_text())


@pytest.mark.parametrize("r", [1, 4])
@pytest.mark.parametrize("m", [1, 3, 7])
@pytest.mark.parametrize("program", sorted(JOBS))
def test_matches_expected(tmp_path, program, m, r):
    inputs = tuple(str(ROOT / p) for p in JOBS[program]["inputs"])
    out = tmp_path / "out"
    spec = JobSpec(
        program=load(program),
        inputs=inputs,
        output=str(out),
        r=r,
        split_size=split.size_for(inputs, m),
        params=JOBS[program]["params"],
    )
    local.run(spec, str(tmp_path))
    want = GOLDEN / program / "expected" / f"r{r}"
    names = sorted(p.name for p in want.iterdir())
    assert sorted(p.name for p in out.glob("part-r-*")) == names
    for name in names:
        assert (out / name).read_bytes() == (want / name).read_bytes(), name
