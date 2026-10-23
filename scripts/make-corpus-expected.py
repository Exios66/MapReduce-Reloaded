"""Regenerates testdata/corpus/expected/: word count over testdata/corpus/.

An oracle independent of both implementations: stdlib only, and the hash is
written out from the spec. Words are maximal runs of bytes that are not
ASCII whitespace (bytes.split()). Output is R=4 files, part-r-NNNNN, each
holding the words of that partition as "word<TAB>count\\n" in byte order.
"""

from collections import Counter
from pathlib import Path

CORPUS = Path(__file__).resolve().parent.parent / "testdata" / "corpus"
R = 4


def fnv1a64(key):
    h = 0xCBF29CE484222325
    for b in key:
        h = ((h ^ b) * 0x100000001B3) & 0xFFFFFFFFFFFFFFFF
    return h


counts = Counter(w for path in sorted(CORPUS.glob("*.txt")) for w in path.read_bytes().split())
parts = [bytearray() for _ in range(R)]
for word in sorted(counts):
    parts[fnv1a64(word) % R] += b"%s\t%d\n" % (word, counts[word])

out = CORPUS / "expected"
out.mkdir(exist_ok=True)
for r, data in enumerate(parts):
    (out / f"part-r-{r:05d}").write_bytes(data)
print(f"{len(counts)} distinct words, {counts.total()} in total")
