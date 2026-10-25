"""Writes web server logs for the URL-frequency example (#33) to stdout: at
least SIZE bytes of requests in the Common Log Format, one per line:

    10.0.3.17 - - [10/Oct/2000:13:55:36 -0700] "GET /page42.html HTTP/1.0" 200 2326

The URL is the seventh space-separated field, where `urlfreq` reads it. It
is /pageN.html for N in 1 to 10,000, drawn with probability proportional to
1 / N, so a few pages get most of the requests, as on a real site. The time
moves forward one second every few requests, from 10 Oct 2000, and into later days
in a large log. A fixed seed makes the output
reproducible.

Usage: python3 bench/gen-weblog.py SIZE [SEED]
"""

import datetime
import itertools
import random
import sys

PAGES = 10_000
CHUNK = 10_000  # lines per write

size = int(sys.argv[1])
rng = random.Random(int(sys.argv[2]) if len(sys.argv) > 2 else 42)

urls = [b"/page%d.html" % n for n in range(1, PAGES + 1)]
weights = list(itertools.accumulate(1 / n for n in range(1, PAGES + 1)))

START = datetime.datetime(2000, 10, 10)


def stamp(second):
    """Returns the time, in the log's format, second seconds after the start."""
    return (START + datetime.timedelta(seconds=second)).strftime("%d/%b/%Y:%H:%M:%S").encode()


out = sys.stdout.buffer
written = 0
second = 0
while written < size:
    chunk = []
    for page in rng.choices(urls, cum_weights=weights, k=CHUNK):
        second += rng.random() < 0.3
        chunk.append(
            b'10.0.%d.%d - - [%s -0700] "GET %s HTTP/1.0" 200 %d'
            % (
                rng.randrange(256),
                rng.randrange(1, 255),
                stamp(second),
                page,
                rng.randrange(200, 20_000),
            )
        )
    data = b"\n".join(chunk) + b"\n"
    out.write(data)
    written += len(data)
