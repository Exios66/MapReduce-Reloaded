"""Property tests for the example programs that parse their input lines
(#152): the Python twins of go/examples/internal/run's FuzzExampleParsers."""

from collections import defaultdict

import pytest
from hypothesis import example, given
from hypothesis import strategies as st

from mapreduce_reloaded.api import Context, TaskID
from mapreduce_reloaded.examples import reverselinks, termvector, urlfreq

CTX = Context(TaskID("map", 0))

# Lines built from the bytes the parsers look at, so the generator reaches
# their branches, plus any bytes at all.
parts = st.sampled_from(
    [
        b"http://",
        b"https://",
        b"://",
        b"\t",
        b"/",
        b"?",
        b"#",
        b" ",
        b"\r",
        b"\xc2\xa0",
        b"\xc2\x85",
        b"a.com",
        b"w",
    ]
)
lines = st.lists(parts).map(b"".join) | st.binary()


@pytest.mark.parametrize(
    "program",
    [urlfreq.program, reverselinks.program, termvector.program],
    ids=["urlfreq", "reverselinks", "termvector"],
)
@given(line=lines)
@example(line=b"://0\t" + b"0" * 78 + b"\xc2\x85")  # broke Go's termvector
@example(line=b"http://h/\ta\xc2\xa0b a\xc2\xa0b c")
def test_reduce_accepts_its_own_map_output(program, line):
    groups = defaultdict(list)
    for k, v in program.mapper(CTX, b"0", line):
        groups[k].append(v)
    for stage in (program.combiner, program.reducer):
        if stage is not None:
            for k in sorted(groups):
                list(stage(CTX, k, iter(groups[k])))
