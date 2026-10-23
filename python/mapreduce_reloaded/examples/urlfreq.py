"""The paper's count of URL access frequency (§2.3), mirroring
go/examples/urlfreq.

The input is a web server log in the Common Log Format, where the URL is the
seventh space-separated field. Lines with fewer fields are skipped.
"""

from mapreduce_reloaded.api import Program, mapper
from mapreduce_reloaded.examples.wordcount import wc_sum


@mapper
def url_map(ctx, offset, line):
    fields = line.split()
    if len(fields) >= 7:
        yield fields[6], b"1"


# The reducer doubles as the combiner.
program = Program(mapper=url_map, reducer=wc_sum, combiner=wc_sum)
