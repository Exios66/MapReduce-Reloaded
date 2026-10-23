"""Runs a user's combiner on sorted map output before it is spilled (§4.3).

Mirrors go/internal/combine.
"""

from collections.abc import Iterable, Iterator

from mapreduce_reloaded.api import Context, Reducer
from mapreduce_reloaded.extsort import CombineFunc, Pair, groups

# Counter names; their ratio shows how much the combiner shrank map output.
INPUT_RECORDS = "combine.input.records"
OUTPUT_RECORDS = "combine.output.records"


def func(ctx: Context, combiner: Reducer) -> CombineFunc:
    """Adapts combiner to a Sorter's combine hook.

    Each key's records are reduced by combiner, which may emit any number of
    records but only for that same key, so the output stays sorted.
    """

    def run(records: Iterable[Pair]) -> Iterator[Pair]:
        n_in = n_out = 0

        def counted() -> Iterator[Pair]:
            nonlocal n_in
            for record in records:
                n_in += 1
                yield record

        try:
            for key, values in groups(counted()):
                for k, v in combiner(ctx, key, values):
                    if k != key:
                        raise ValueError(f"combiner emitted key {k!r} while combining {key!r}")
                    n_out += 1
                    yield k, v
        finally:
            ctx.count(INPUT_RECORDS, n_in)
            ctx.count(OUTPUT_RECORDS, n_out)

    return run
