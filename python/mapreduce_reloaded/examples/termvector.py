"""The paper's term vector per host (§2.3), mirroring go/examples/termvector.

Each input line is a document: a URL, a tab and the document's text. The
host is the part of the URL between "://" and the next "/", "?" or "#"; lines
without a tab or a host are skipped.

The output is each host with its term vector: the words of its documents,
split as word count splits them, as word:count pairs, most frequent first,
then by word. Terms that occur fewer than the job's "min" parameter times
(default 2) are dropped.
"""

import re
from collections import Counter

from mapreduce_reloaded.api import Program, combiner, mapper, reducer


@mapper
def tv_map(ctx, offset, line):
    url, tab, text = line.partition(b"\t")
    host = re.split(rb"[/?#]", url.partition(b"://")[2], maxsplit=1)[0]
    if tab and host:
        yield host, encode(Counter(text.split()))


# Adds vectors and drops nothing, since a term rare in one map task may not
# be rare in the job.
@combiner
def tv_sum(ctx, host, vectors):
    yield host, encode(total(vectors))


@reducer
def tv_reduce(ctx, host, vectors):
    min_count = ctx.params.get("min", "2")
    if not (min_count.isdigit() and int(min_count) >= 1):
        raise ValueError(f"termvector: min={min_count!r} is not a positive integer")
    yield host, encode(total(vectors), int(min_count))


def total(vectors):
    counts = Counter()
    for vector in vectors:
        for pair in vector.split():
            term, colon, n = pair.rpartition(b":")
            if not (colon and n.isdigit()):
                raise ValueError(f"termvector: bad term {pair!r}")
            counts[term] += int(n)
    return counts


def encode(counts, min_count=1):
    terms = sorted((t for t in counts if counts[t] >= min_count), key=lambda t: (-counts[t], t))
    return b" ".join(b"%s:%d" % (t, counts[t]) for t in terms)


program = Program(mapper=tv_map, reducer=tv_reduce, combiner=tv_sum)
