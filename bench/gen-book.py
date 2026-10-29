"""Writes real text at any size to stdout, for word count over real English
(E6): at least SIZE bytes of whole lines of BOOK, War and Peace by default
(scripts/fetch-data.sh fetches it to data/input/warpeace/).

Each pass writes every non-blank line of the book once, in a new random
order, so word frequencies and line lengths are the book's own at any
size, unlike gen-zipf.py's drawn words. Two things differ from a real
collection that large: the vocabulary stops at the book's (41,631 words for
War and Peace), where a real one keeps growing, and each line repeats once
per pass, about every 3 MB. Word count's combiner folds repeats within a
map task either way, so its savings depend on the frequencies, which are
real. A fixed seed makes the output reproducible.

Usage: python3 bench/gen-book.py SIZE [SEED] [BOOK]
"""

import random
import sys
from pathlib import Path

BOOK = Path(__file__).resolve().parent.parent / "data/input/warpeace/warpeace.txt"

size = int(sys.argv[1])
rng = random.Random(int(sys.argv[2]) if len(sys.argv) > 2 else 42)
book = Path(sys.argv[3]) if len(sys.argv) > 3 else BOOK
if not book.exists():
    sys.exit(f"gen-book: no {book}; run scripts/fetch-data.sh warpeace")

lines = [line.rstrip(b"\r") + b"\n" for line in book.read_bytes().split(b"\n") if line.strip()]
if not lines:
    sys.exit(f"gen-book: {book} has no text")

out = sys.stdout.buffer
written, whole = 0, sum(map(len, lines))
while written < size:
    rng.shuffle(lines)
    if size - written >= whole:
        out.write(b"".join(lines))
        written += whole
        continue
    for line in lines:  # the last pass stops at the first line that reaches SIZE
        out.write(line)
        written += len(line)
        if written >= size:
            break
