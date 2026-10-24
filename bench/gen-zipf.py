"""Writes skewed input for the benchmarks (#57) to stdout: at least SIZE
bytes of lines of words whose frequencies follow Zipf's law.

Each line is 5 to 15 words drawn from a vocabulary of 100,000, w1 to
w100000, where word wN is drawn with probability proportional to 1 / N^S.
With S = 1 (the default), w1 is about 8% of the words, so a word count's
reduce task for w1 gets far more values than the others: a skewed key,
unlike the even spread of gen-words.py. A fixed seed makes the output
reproducible.

Usage: python3 bench/gen-zipf.py SIZE [SEED] [S]
"""

import itertools
import random
import sys

VOCABULARY = 100_000
CHUNK = 10_000  # lines per write

size = int(sys.argv[1])
rng = random.Random(int(sys.argv[2]) if len(sys.argv) > 2 else 42)
s = float(sys.argv[3]) if len(sys.argv) > 3 else 1.0

words = [b"w%d" % n for n in range(1, VOCABULARY + 1)]
weights = list(itertools.accumulate(1 / n**s for n in range(1, VOCABULARY + 1)))

out = sys.stdout.buffer
written = 0
while written < size:
    chunk = []
    for _ in range(CHUNK):
        chunk.append(b" ".join(rng.choices(words, cum_weights=weights, k=rng.randint(5, 15))))
    data = b"\n".join(chunk) + b"\n"
    out.write(data)
    written += len(data)
