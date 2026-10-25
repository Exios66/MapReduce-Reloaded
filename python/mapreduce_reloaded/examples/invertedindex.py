"""The paper's inverted index (§2.3), mirroring go/examples/invertedindex.

The output is each word, split as word count splits them, with the input
files it occurs in: their base names, sorted, without duplicates, and joined
by commas.
"""

import os

from mapreduce_reloaded.api import Program, combiner, mapper, reducer


@mapper
def index_map(ctx, offset, line):
    file = os.fsencode(os.path.basename(ctx.input))  # not .encode(): a name need not be UTF-8
    for word in line.split():
        yield word, file


# Drops a map task's repeats of a (word, file) pair.
@combiner
def index_distinct(ctx, word, files):
    for file in sorted(set(files)):
        yield word, file


@reducer
def index_reduce(ctx, word, files):
    yield word, b",".join(sorted(set(files)))


program = Program(mapper=index_map, reducer=index_reduce, combiner=index_distinct)
