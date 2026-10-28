"""Tests for the text and kv formats, mirroring go/internal/formats."""

import pytest
from hypothesis import given
from hypothesis import strategies as st

from mapreduce_reloaded.formats import KV, Text, input_format, output_format


def read_splits(fmt, path, split_size):
    return [rec for split in fmt.splits(str(path), split_size) for rec in fmt.read(split)]


def lines(content):
    """The obvious reading of content as text records: split after each "\\n"."""
    out, off = [], 0
    while off < len(content):
        end = content.find(b"\n", off) + 1 or len(content)
        out.append((str(off).encode(), content[off:end].removesuffix(b"\n").removesuffix(b"\r")))
        off = end
    return out


@given(st.binary(max_size=300), st.integers(min_value=1, max_value=64))
def test_text_splits_read_every_line_once(tmp_path_factory, content, split_size):
    path = tmp_path_factory.mktemp("in") / "input"
    path.write_bytes(content)
    assert read_splits(Text(), path, split_size) == lines(content)


def test_text_long_line(tmp_path):
    path = tmp_path / "input"
    path.write_bytes(b"x" * 10_000 + b"\nshort\n")
    assert read_splits(Text(), path, 3000) == [(b"0", b"x" * 10_000), (b"10001", b"short")]


def test_splits(tmp_path):
    empty, data = tmp_path / "empty", tmp_path / "data"
    empty.write_bytes(b"")
    data.write_bytes(bytes(25))
    assert Text().splits(str(empty), 10) == []
    assert [(s.start, s.end) for s in Text().splits(str(data), 10)] == [(0, 10), (10, 20), (20, 25)]
    assert len(KV().splits(str(data), 10)) == 1
    with pytest.raises(ValueError):
        Text().splits(str(data), 0)


def test_lookup():
    assert isinstance(input_format(), Text)
    with pytest.raises(ValueError, match="parquet"):
        output_format("parquet")


@pytest.mark.parametrize(
    "name, want",
    [
        ("text", [(b"0", b"apple\t3"), (b"8", b"pear\t1")]),  # read back as lines
        ("kv", [(b"apple", b"3"), (b"pear", b"1")]),
    ],
)
def test_output_round_trip(tmp_path, name, want):
    path = tmp_path / "part-r-00000"
    with output_format(name).create(str(path)) as w:
        w.write(b"apple", b"3")
        w.write(b"pear", b"1")
    assert read_splits(input_format(name), path, 4) == want


def test_text_alignment_stays_in_split(tmp_path, monkeypatch):
    # A split inside a very long line looks for the next line only up to its
    # own end, so the splits of one long line do not each read the rest of it.
    import builtins

    from mapreduce_reloaded.formats import text

    path = tmp_path / "input"
    path.write_bytes(b"x" * 1_000_000 + b"\nshort\n")
    sizes = []

    class Spy:
        def __init__(self, f):
            self.f = f

        def __enter__(self):
            return self

        def __exit__(self, *exc):
            self.f.close()

        def seek(self, pos):
            return self.f.seek(pos)

        def readline(self, limit=-1):
            line = self.f.readline(limit)
            sizes.append(len(line))
            return line

    monkeypatch.setattr(text, "open", lambda *a: Spy(builtins.open(*a)), raising=False)
    inside = text.Split(str(path), 5000, 5100)
    assert list(Text().read(inside)) == []
    assert sizes == [100]  # the alignment read, and nothing past the split
    assert list(Text().read(text.Split(str(path), 1_000_000, 1_000_007))) == [
        (b"1000001", b"short")
    ]
