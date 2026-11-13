"""testdata/corpus/README.md states the corpus's and its expected output's
numbers; each one is recomputed here from the files, so the page cannot
drift from the data it describes."""

import re
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
CORPUS = ROOT / "testdata" / "corpus"
README = (CORPUS / "README.md").read_text()


def number(text):
    return int(text.replace(",", ""))


def rows(first_column):
    """The table rows whose first cell matches first_column, as cells."""
    found = []
    for line in README.splitlines():
        cells = [cell.strip() for cell in line.strip().strip("|").split("|")]
        if line.startswith("|") and re.fullmatch(first_column, cells[0]):
            found.append(cells)
    return found


def test_each_books_size_and_words():
    total = [0, 0, set()]
    for cells in rows(r"`\w+\.txt`"):
        data = (CORPUS / cells[0].strip("`")).read_bytes()
        words = data.split()
        assert [number(c) for c in cells[4:7]] == [len(data), len(words), len(set(words))], cells[0]
        total[0] += len(data)
        total[1] += len(words)
        total[2] |= set(words)
    books = sorted(path.name for path in CORPUS.glob("*.txt"))
    assert [cells[0].strip("`") for cells in rows(r"`\w+\.txt`")] == books
    (all_four,) = [cells for cells in rows("All four") if len(cells) == 7]
    assert [number(c) for c in all_four[4:7]] == [total[0], total[1], len(total[2])]


def test_the_multibyte_words():
    words = set((CORPUS / "verwandlung.txt").read_bytes().split())
    multibyte = [word for word in words if any(byte > 127 for byte in word)]
    assert f"({len(multibyte):,} of its {len(words):,} distinct words)" in README


def test_each_parts_words_occurrences_and_bytes():
    parts = sorted((CORPUS / "expected").glob("part-r-*"))
    table = rows(r"`part-r-\d+`")
    assert [cells[0].strip("`") for cells in table] == [part.name for part in parts]
    totals = [0, 0, 0]
    counts = {}
    for cells, part in zip(table, parts, strict=True):
        data = part.read_bytes()
        lines = [line.rsplit(b"\t", 1) for line in data.splitlines()]
        counts.update((word, int(count)) for word, count in lines)
        found = [len(lines), sum(int(count) for _, count in lines), len(data)]
        assert [number(c) for c in cells[1:4]] == found, part.name
        totals = [a + b for a, b in zip(totals, found, strict=True)]
    (all_four,) = [cells for cells in rows("All four") if len(cells) == 4]
    assert [number(c) for c in all_four[1:4]] == totals
    assert f"`the` ({counts[b'the']:,})" in README
    for word in (b"the", b"The", b"THE", b"said", b"said,"):
        assert word in counts, word
    part0 = {line.rsplit(b"\t", 1)[0] for line in parts[0].read_bytes().splitlines()}
    assert {b"the", b"of", b"to", b"a"} <= part0


def test_the_tests_it_names_exist():
    section = README.split("## What is checked against it", 1)[1]
    names = re.findall(r"`((?:go|python|scripts)/[^`]+)`", section)
    assert names
    for name in names:
        assert (ROOT / name).is_file(), name
