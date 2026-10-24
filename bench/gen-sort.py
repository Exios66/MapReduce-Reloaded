"""Writes the input for the sort benchmark (#57) to stdout: TeraSort-style
100-byte records with random 10-byte keys.

The paper sorted 10^10 100-byte records by their first 10 bytes (§5.3), the
TeraSort benchmark's format. This writes ceil(SIZE / 100) records of:

- a 10-byte key of random bytes, any byte but newline and carriage return,
  so that keys spread evenly over the first byte that distsort partitions
  on; printable keys would put every record in the lower half of the
  partitions;
- two spaces, the record's number as 32 hex digits and two spaces, as in
  the ASCII records of TeraSort's gensort;
- 53 random letters and a newline.

A fixed seed makes the output reproducible.

Usage: python3 bench/gen-sort.py SIZE [SEED]
"""

import random
import sys

KEY = 10  # bytes
RECORD = 100  # bytes, with the newline
CHUNK = 10_000  # records per write

# 256 entries over 32 characters, so each random byte picks one uniformly.
TABLE = bytes(b"abcdefghijklmnopqrstuvwxyz234567"[i % 32] for i in range(256))

size = int(sys.argv[1])
rng = random.Random(int(sys.argv[2]) if len(sys.argv) > 2 else 42)
records = -(-size // RECORD)

keys = b""  # random bytes not yet used, without newlines


def key():
    global keys
    while len(keys) < KEY:
        keys += rng.randbytes(64 * 1024).translate(None, b"\n\r")
    k, keys = keys[:KEY], keys[KEY:]
    return k


out = sys.stdout.buffer
filler = RECORD - KEY - 2 - 32 - 2 - 1
for start in range(0, records, CHUNK):
    chunk = []
    for i in range(start, min(start + CHUNK, records)):
        tail = rng.randbytes(filler).translate(TABLE)
        chunk.append(b"%s  %032X  %s\n" % (key(), i, tail))
    out.write(b"".join(chunk))
