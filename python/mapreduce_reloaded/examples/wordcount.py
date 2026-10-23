"""The paper's word count (§2), mirroring go/examples/wordcount.

A word is a maximal run of bytes that are not ASCII whitespace. bytes.split()
splits on exactly those bytes, so the Go and Python versions agree.
"""

from mapreduce_reloaded.api import Program, mapper, reducer


@mapper
def wc_map(ctx, offset, line):
    for word in line.split():
        yield word, b"1"


@reducer
def wc_sum(ctx, word, counts):
    yield word, b"%d" % sum(int(c) for c in counts)


# The reducer doubles as the combiner.
program = Program(mapper=wc_map, reducer=wc_sum, combiner=wc_sum)
