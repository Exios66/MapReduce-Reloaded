"""Tests for scripts/check-real-data.py, which checks the example programs
over the datasets of scripts/fetch-data.sh against its own oracle."""

import importlib.util
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]
_spec = importlib.util.spec_from_file_location("check_real_data", ROOT / "scripts/check-real-data.py")
real = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(real)


def golden(d: Path) -> list[bytes]:
    return [p.read_bytes() for p in sorted(d.glob("part-r-*"))]


@pytest.mark.parametrize(
    "program, inputs, expected",
    [
        ("wordcount", sorted((ROOT / "testdata/corpus").glob("*.txt")), "testdata/corpus/expected"),
        ("urlfreq", [ROOT / "testdata/examples/urlfreq/input/access.log"], "testdata/examples/urlfreq/expected/r4"),
        ("reverselinks", [ROOT / "testdata/examples/reverselinks/input/links.tsv"],
         "testdata/examples/reverselinks/expected/r4"),
    ],
)  # fmt: skip
def test_oracle_gives_the_golden_output(program, inputs, expected):
    """The oracle agrees with the committed golden outputs, which both
    implementations are tested against, so a dataset that differs points at
    the programs, not at the oracle."""
    data = b"".join(p.read_bytes() for p in inputs)
    assert real.partitions(real.ORACLES[program](data), 4) == golden(ROOT / expected)


def test_lines():
    assert list(real.lines(b"a\r\nb\n\nc")) == [b"a", b"b", b"", b"c"]
    assert list(real.lines(b"a\n")) == [b"a"]


def test_urlfreq_skips_short_lines():
    log = b'h - - [d z] "GET /a HTTP/1.0" 200 1\nshort line\nh - - [d z] "GET /a HTTP/1.0" 200 1\n'
    assert real.urlfreq(log) == {b"/a": b"2"}


def test_reverselinks_sorts_and_dedups_sources():
    assert real.reverselinks(b"b\tt\na\tt\nb\tt\r\nno tab\n") == {b"t": b"a,b"}


def test_main_without_data(tmp_path, monkeypatch, capsys):
    monkeypatch.setattr(real, "DATA", tmp_path)
    assert real.main([]) == 1
    assert "run scripts/fetch-data.sh" in capsys.readouterr().err


def test_main_rejects_an_unknown_dataset():
    with pytest.raises(SystemExit):
        real.main(["nope"])
