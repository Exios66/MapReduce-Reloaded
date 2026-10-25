"""Writes a web-link graph for the reverse-links example (#33) to stdout: at
least SIZE bytes of links, one per line, as the source page, a tab and the
target page.

There are 100,000 pages, http://example.com/pN for N in 1 to 100,000. The
source of each link is any page, and its target is drawn with probability
proportional to 1 / N, so a few pages are linked from very many others: the
reverse graph's value lists are skewed, as on the web. A fixed seed makes
the output reproducible.

Usage: python3 bench/gen-links.py SIZE [SEED]
"""

import itertools
import random
import sys

PAGES = 100_000
CHUNK = 10_000  # lines per write

size = int(sys.argv[1])
rng = random.Random(int(sys.argv[2]) if len(sys.argv) > 2 else 42)

pages = [b"http://example.com/p%d" % n for n in range(1, PAGES + 1)]
weights = list(itertools.accumulate(1 / n for n in range(1, PAGES + 1)))

out = sys.stdout.buffer
written = 0
while written < size:
    sources = rng.choices(pages, k=CHUNK)
    targets = rng.choices(pages, cum_weights=weights, k=CHUNK)
    data = b"\n".join(s + b"\t" + t for s, t in zip(sources, targets, strict=True)) + b"\n"
    out.write(data)
    written += len(data)
