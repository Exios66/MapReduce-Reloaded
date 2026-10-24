"""Writes SIZE bytes of text lines to stdout for the codec benchmark (#42).

Each line is 5 to 15 words drawn at random, by frequency, from the test
corpus. Lines are text-like but almost never repeat, unlike copies of the
corpus, which a sort would bring together and a codec would then shrink
far more than real data. A fixed seed makes the output reproducible.

Usage: python3 bench/gen-words.py SIZE [SEED]
"""

import random
import re
import sys
from pathlib import Path

CORPUS = Path(__file__).resolve().parent.parent / "testdata" / "corpus"

size = int(sys.argv[1])
rng = random.Random(int(sys.argv[2]) if len(sys.argv) > 2 else 42)
words = [w for f in sorted(CORPUS.glob("*.txt")) for w in re.findall(r"\S+", f.read_text())]

out = sys.stdout
written = 0
while written < size:
    line = " ".join(rng.choices(words, k=rng.randint(5, 15))) + "\n"
    out.write(line)
    written += len(line.encode())
