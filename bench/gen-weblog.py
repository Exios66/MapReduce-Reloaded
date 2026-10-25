"""Writes web server logs for the URL-frequency example (#33) to stdout: at
least SIZE bytes of requests in the Common Log Format, one per line:

    10.0.3.17 - - [10/Oct/2000:13:55:36 -0700] "GET /page42.html HTTP/1.0" 200 2326

The URL is the seventh space-separated field, where `urlfreq` reads it. It
is /pageN.html for N in 1 to 10,000, drawn with probability proportional to
1 / N, so a few pages get most of the requests, as on a real site. The time
moves forward one second every few requests. A fixed seed makes the output
reproducible.

Usage: python3 bench/gen-weblog.py SIZE [SEED]
"""

import itertools
import random
import sys

PAGES = 10_000
CHUNK = 10_000  # lines per write

size = int(sys.argv[1])
rng = random.Random(int(sys.argv[2]) if len(sys.argv) > 2 else 42)

urls = [b"/page%d.html" % n for n in range(1, PAGES + 1)]
weights = list(itertools.accumulate(1 / n for n in range(1, PAGES + 1)))

out = sys.stdout.buffer
written = 0
second = 0
while written < size:
    chunk = []
    for page in rng.choices(urls, cum_weights=weights, k=CHUNK):
        second += rng.random() < 0.3
        h, m, s = second // 3600 % 24, second // 60 % 60, second % 60
        chunk.append(
            b'10.0.%d.%d - - [10/Oct/2000:%02d:%02d:%02d -0700] "GET %s HTTP/1.0" 200 %d'
            % (rng.randrange(256), rng.randrange(1, 255), h, m, s, page, rng.randrange(200, 20_000))
        )
    data = b"\n".join(chunk) + b"\n"
    out.write(data)
    written += len(data)
