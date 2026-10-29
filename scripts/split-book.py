"""Splits War and Peace (scripts/fetch-data.sh) into documents for the
inverted index and term vectors (docs/data.md).

Usage: split-book.py BOOK OUT_DIR

The book has 15 books and two epilogues, each in chapters headed
"CHAPTER I" and so on. Its contents list comes first and repeats every
heading, so the text starts at the second "BOOK ONE" line. This writes:

- OUT_DIR/warpeace-chapters/bNN-cMMM.txt: each chapter as a file, from its
  heading to the next, for invertedindex, which indexes words by file name;
- OUT_DIR/warpeace-docs/docs.tsv: each chapter as one line,
  "http://book-NN.warpeace/chapter-MMM", a tab, and its text with line
  breaks and tabs made spaces, for termvector, whose host is then one of
  the 17 parts.

The epilogues are parts 16 and 17. Text before a part's first chapter (its
title) belongs to no chapter and is left out.
"""

import re
import sys
from pathlib import Path

PART = re.compile(rb"^(BOOK [A-Z]+:|FIRST EPILOGUE|SECOND EPILOGUE)")
CHAPTER = re.compile(rb"^CHAPTER [IVXLC]+$")


def chapters(text: bytes) -> list[tuple[int, int, list[bytes]]]:
    """Returns (part, chapter, lines) for each chapter."""
    lines = text.split(b"\n")
    starts = [i for i, line in enumerate(lines) if line.startswith(b"BOOK ONE:")]
    if len(starts) < 2:
        sys.exit("split-book: no second BOOK ONE heading; is this War and Peace?")
    out, part, current = [], 0, None
    for line in lines[starts[1] :]:
        if PART.match(line):
            part += 1
            current = None
        elif CHAPTER.match(line.rstrip(b"\r")):
            current = []
            out.append((part, sum(1 for p, _, _ in out if p == part) + 1, current))
        if current is not None:
            current.append(line)
    return out


def main(argv: list[str]) -> int:
    book, out = Path(argv[0]), Path(argv[1])
    found = chapters(book.read_bytes())
    files, docs = out / "warpeace-chapters", out / "warpeace-docs"
    files.mkdir(parents=True, exist_ok=True)
    docs.mkdir(parents=True, exist_ok=True)
    tsv = bytearray()
    for part, chapter, lines in found:
        (files / f"b{part:02d}-c{chapter:03d}.txt").write_bytes(b"\n".join(lines) + b"\n")
        text = b" ".join(line.strip() for line in lines).replace(b"\t", b" ")
        tsv += b"http://book-%02d.warpeace/chapter-%03d\t%s\n" % (part, chapter, text)
    (docs / "docs.tsv").write_bytes(tsv)
    print(f"split-book: {len(found)} chapters in {max(p for p, _, _ in found)} parts")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
