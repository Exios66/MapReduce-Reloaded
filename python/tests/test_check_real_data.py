"""Tests for scripts/check-real-data.py, which checks the example programs
over the datasets of scripts/fetch-data.sh against its own oracle."""

import importlib.util
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "scripts/check-real-data.py"
_spec = importlib.util.spec_from_file_location("check_real_data", SCRIPT)
real = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(real)


def golden(d: Path) -> list[bytes]:
    return [p.read_bytes() for p in sorted(d.glob("part-r-*"))]


EX = ROOT / "testdata/examples"
CORPUS = ROOT / "testdata/corpus"
INDEX = EX / "invertedindex/expected/r4"


@pytest.mark.parametrize(
    "program, inputs, expected",
    [
        ("wordcount", sorted(CORPUS.glob("*.txt")), CORPUS / "expected"),
        ("urlfreq", [EX / "urlfreq/input/access.log"], EX / "urlfreq/expected/r4"),
        ("reverselinks", [EX / "reverselinks/input/links.tsv"], EX / "reverselinks/expected/r4"),
        ("invertedindex", [CORPUS / "raven.txt", EX / "edge.txt"], INDEX),
        ("termvector", [EX / "termvector/input/docs.tsv"], EX / "termvector/expected/r4"),
    ],
)  # fmt: skip
def test_oracle_gives_the_golden_output(program, inputs, expected):
    """The oracle agrees with the committed golden outputs, which both
    implementations are tested against, so a dataset that differs points at
    the programs, not at the oracle."""
    assert real.partitions(real.ORACLES[program](inputs), 4) == golden(expected)


def test_lines():
    assert list(real.lines(b"a\r\nb\n\nc")) == [b"a", b"b", b"", b"c"]
    assert list(real.lines(b"a\n")) == [b"a"]


def write(tmp_path, data, name="in.txt"):
    (tmp_path / name).write_bytes(data)
    return [tmp_path / name]


def test_urlfreq_skips_short_lines(tmp_path):
    log = b'h - - [d z] "GET /a HTTP/1.0" 200 1\nshort line\nh - - [d z] "GET /a HTTP/1.0" 200 1\n'
    assert real.urlfreq(write(tmp_path, log)) == {b"/a": b"2"}


def test_reverselinks_sorts_and_dedups_sources(tmp_path):
    assert real.reverselinks(write(tmp_path, b"b\tt\na\tt\nb\tt\r\nno tab\n")) == {b"t": b"a,b"}


def test_termvector_hosts_order_and_min(tmp_path):
    docs = b"http://h/x\tb a b c\nhttps://h?q\ta\nftp://g#f\tz\nno-tab\nnohost\tq\nhttp:///p\tq\n"
    assert real.termvector(write(tmp_path, docs)) == {b"h": b"a:2 b:2", b"g": b""}


def test_main_without_data(tmp_path, monkeypatch, capsys):
    monkeypatch.setattr(real, "DATA", tmp_path)
    assert real.main([]) == 1
    assert "run scripts/fetch-data.sh" in capsys.readouterr().err


def test_main_rejects_an_unknown_dataset():
    with pytest.raises(SystemExit):
        real.main(["nope"])
