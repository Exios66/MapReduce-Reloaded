"""Tests for the combiner, mirroring go/internal/combine."""

import random

import pytest

from mapreduce_reloaded import combine
from mapreduce_reloaded.api import Context, TaskID, reducer
from mapreduce_reloaded.extsort import Sorter, groups


@reducer
def total(ctx, key, values):
    yield key, b"%d" % sum(int(v) for v in values)


def words(n):
    rng = random.Random(3)
    return [b"w%d" % rng.randrange(40) for _ in range(n)]


def counting_context():
    counts = {}
    ctx = Context(TaskID("map", 0), on_count=lambda n, d: counts.update({n: counts.get(n, 0) + d}))
    return ctx, counts


def map_side(tmp_path, ws, combiner=None, ctx=None):
    ctx = ctx or Context(TaskID("map", 0))
    fn = combine.func(ctx, combiner) if combiner else None
    sorter = Sorter(dir=str(tmp_path), budget=2048, combine=fn)
    for w in ws:
        sorter.add(w, b"1")
    return list(sorter.sorted()), sorter


def reduce(records):
    ctx = Context(TaskID("reduce", 0))
    return [out for key, values in groups(records) for out in total(ctx, key, values)]


def test_combiner_preserves_output(tmp_path):
    ws = words(2000)
    ctx, counts = counting_context()
    plain, plain_sorter = map_side(tmp_path, ws)
    combined, combined_sorter = map_side(tmp_path, ws, total, ctx)
    assert reduce(plain) == reduce(combined)
    assert len(combined) < len(plain)
    assert combined_sorter.spilled_bytes < plain_sorter.spilled_bytes
    assert counts == {combine.INPUT_RECORDS: len(ws), combine.OUTPUT_RECORDS: len(combined)}


def test_combiner_applied_any_number_of_times(tmp_path):
    records, _ = map_side(tmp_path, words(500))
    want = reduce(records)
    apply = combine.func(Context(TaskID("map", 0)), total)
    for _ in range(4):
        assert reduce(records) == want
        records = list(apply(iter(records)))


def test_combiner_must_keep_key():
    @reducer
    def rename(ctx, key, values):
        yield b"x" + key, b""

    with pytest.raises(ValueError, match="emitted key b'xa'"):
        list(combine.func(Context(TaskID("map", 0)), rename)(iter([(b"a", b"1")])))
