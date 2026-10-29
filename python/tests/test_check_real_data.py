"""Tests for scripts/check-real-data.py, which checks the example programs
over the datasets of scripts/fetch-data.sh against its own oracle."""

import gzip
import importlib.util
import subprocess
import sys
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


def warc(kind, body, url=b"http://h.example/p"):
    head = b"WARC/1.0\r\nWARC-Type: %s\r\n" % kind
    if url:
        head += b"WARC-Target-URI: %s\r\n" % url
    return head + b"Content-Length: %d\r\n\r\n%s\r\n\r\n" % (len(body), body)


@pytest.mark.parametrize("compress", [False, True])
def test_wet_to_tsv(compress):
    """Each conversion record becomes "URL<TAB>text" with whitespace runs
    made one space; warcinfo and records without a URL are left out."""
    wet = (
        warc(b"warcinfo", b"software: x\r\n", url=b"")
        + warc(b"conversion", "Grüße  aus\n\tBerlin\r\n".encode())
        + warc(b"conversion", b"no url", url=b"")
        + warc(b"conversion", b"", url=b"https://g.example/")
    )
    if compress:
        wet = gzip.compress(wet[: len(wet) // 2]) + gzip.compress(wet[len(wet) // 2 :])
    r = subprocess.run(
        [sys.executable, ROOT / "scripts/wet-to-tsv.py"], input=wet, capture_output=True, check=True
    )
    assert r.stdout == "http://h.example/p\tGrüße aus Berlin\nhttps://g.example/\t\n".encode()
    assert b"2 pages" in r.stderr


@pytest.mark.parametrize("argv", [["nope"], ["warpeace"], ["-r", "0"], ["-m", "0"]])
def test_main_rejects_bad_arguments(tmp_path, monkeypatch, argv):
    """An unknown or unfetched dataset, or no tasks, stops with a usage error."""
    monkeypatch.setattr(real, "DATA", tmp_path)
    with pytest.raises(SystemExit) as e:
        real.main(argv)
    assert e.value.code == 2


def test_split_book(tmp_path):
    """split-book.py writes each chapter after the contents list as a file,
    replacing an earlier split, and leaves no temporary directory."""
    book = tmp_path / "book.txt"
    book.write_bytes(
        b"BOOK ONE: x\nCHAPTER I\n"  # the contents list
        b"BOOK ONE: x\ntitle\nCHAPTER I\none\nCHAPTER II\ntwo\nFIRST EPILOGUE\nCHAPTER I\nthree\n"
    )
    (tmp_path / "warpeace-chapters").mkdir()
    (tmp_path / "warpeace-chapters/stale.txt").write_text("old\n")
    script = ROOT / "scripts/split-book.py"
    subprocess.run([sys.executable, script, book, tmp_path], capture_output=True, check=True)
    files = sorted(p.name for p in (tmp_path / "warpeace-chapters").iterdir())
    assert files == ["b01-c001.txt", "b01-c002.txt", "b02-c001.txt"]
    assert (tmp_path / "warpeace-chapters/b01-c002.txt").read_bytes() == b"CHAPTER II\ntwo\n"
    assert not (tmp_path / "warpeace-chapters.part").exists()
