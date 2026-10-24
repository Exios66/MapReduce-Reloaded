"""Writes the input for the grep benchmark (#57) to stdout: 100-byte records
in which a rare three-character pattern appears.

The paper's grep scanned 10^10 100-byte records for a rare three-character
pattern, which 92,337 of them contained (§5.2). This writes ceil(SIZE / 100)
records, each 99 random characters and a newline, and puts the pattern XYZ
at a random place in round(records * 92,337 / 10^10) of them, at least one.
The other characters are lowercase letters and digits, so the pattern never
appears by chance and the number of matches is exact: grep's output must
have that many lines. A fixed seed makes the output reproducible.

Usage: python3 bench/gen-grep.py SIZE [SEED]
"""

import random
import sys

PATTERN = b"XYZ"
RATE = 92_337 / 10**10  # matches per record in the paper's run
RECORD = 100  # bytes, with the newline
CHUNK = 10_000  # records per write

# 256 entries over 32 characters, so each random byte picks one uniformly.
TABLE = bytes(b"abcdefghijklmnopqrstuvwxyz234567"[i % 32] for i in range(256))

size = int(sys.argv[1])
rng = random.Random(int(sys.argv[2]) if len(sys.argv) > 2 else 42)
records = -(-size // RECORD)
matches = set(rng.sample(range(records), min(records, max(1, round(records * RATE)))))

out = sys.stdout.buffer
for start in range(0, records, CHUNK):
    chunk = []
    for i in range(start, min(start + CHUNK, records)):
        line = rng.randbytes(RECORD - 1).translate(TABLE)
        if i in matches:
            at = rng.randrange(RECORD - len(PATTERN))
            line = line[:at] + PATTERN + line[at + len(PATTERN) :]
        chunk.append(line)
    out.write(b"\n".join(chunk) + b"\n")
