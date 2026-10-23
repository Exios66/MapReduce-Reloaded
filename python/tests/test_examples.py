"""Tests for the example programs other than word count, mirroring the tests
in go/examples."""

from pathlib import Path

import pytest

from mapreduce_reloaded import local, split
from mapreduce_reloaded.api import JobSpec
from mapreduce_reloaded.examples import grep, reverselinks, urlfreq

CORPUS = Path(__file__).resolve().parents[2] / "testdata" / "corpus"
INPUTS = (str(CORPUS / "*.txt"),)


def run(tmp_path, program, m, params, inputs=INPUTS):
    out = tmp_path / "out"
    spec = JobSpec(
        program=program,
        inputs=inputs,
        output=str(out),
        r=1,
        split_size=split.size_for(inputs, m),
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


def write(tmp_path, data):
    path = tmp_path / "input"
    path.write_bytes(data)
    return (str(path),)


def test_urlfreq(tmp_path):
    log = (
        b'10.0.0.1 - - [10/Oct/2000:13:55:36 -0700] "GET /b HTTP/1.0" 200 10\n'
        b'10.0.0.2 - - [10/Oct/2000:13:55:37 -0700] "GET /a HTTP/1.0" 200 10\n'
        b"a short line\n"
        b'10.0.0.1 - - [10/Oct/2000:13:55:38 -0700] "GET /b HTTP/1.0" 404 0\n'
        b'10.0.0.3 - - [10/Oct/2000:13:55:39 -0700] "POST /b HTTP/1.0" 200 5\n'
    )
    out = run(tmp_path, urlfreq.program, 2, {}, write(tmp_path, log))
    assert (out / "part-r-00000").read_bytes() == b"/a\t1\n/b\t3\n"


def test_reverselinks(tmp_path):
    links = b"c\tx\na\tx\nno tab\nb\ty\nc\tx\nb\tx\n"
    out = run(tmp_path, reverselinks.program, 2, {}, write(tmp_path, links))
    assert (out / "part-r-00000").read_bytes() == b"x\ta,b,c\ny\tb\n"
