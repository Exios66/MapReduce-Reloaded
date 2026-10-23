"""The paper's reverse web-link graph (§2.3), mirroring
go/examples/reverselinks.

Each input line is a source page, a tab and a target page. The output is
each target with the pages that link to it, sorted, without duplicates, and
joined by commas. Lines without a tab are skipped.
"""

from mapreduce_reloaded.api import Program, mapper, reducer


@mapper
def link_map(ctx, offset, line):
    source, tab, target = line.partition(b"\t")
    if tab:
        yield target, source


@reducer
def link_reduce(ctx, target, sources):
    yield target, b",".join(sorted(set(sources)))


program = Program(mapper=link_map, reducer=link_reduce)
