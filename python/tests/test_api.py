"""Tests for the programming API, including the paper's word count."""

from itertools import groupby
from operator import itemgetter

import pytest

from mapreduce_reloaded.api import (
    MAX_MAP_TASKS,
    MAX_REDUCE_TASKS,
    Context,
    JobSpec,
    Program,
    TaskID,
    mapper,
    reducer,
)
from mapreduce_reloaded.split import MAX_SPLITS


@mapper
def wc_map(ctx, offset, line):
    for word in line.split():
        yield word, b"1"


@reducer
def wc_reduce(ctx, word, counts):
    yield word, str(sum(int(c) for c in counts)).encode()


WORD_COUNT = Program(mapper=wc_map, reducer=wc_reduce, combiner=wc_reduce)
CTX = Context(TaskID("map", 0))


def run(program, lines):
    """Runs program on lines in memory: map, sort by key, group, reduce."""
    inter = [kv for i, line in enumerate(lines) for kv in program.mapper(CTX, b"%d" % i, line)]
    inter.sort(key=itemgetter(0))
    return [
        out
        for key, group in groupby(inter, key=itemgetter(0))
        for out in program.reducer(CTX, key, (v for _, v in group))
    ]


def test_word_count():
    out = run(WORD_COUNT, [b"the quick fox", b"the lazy dog", b"the end"])
    assert out == [
        (b"dog", b"1"),
        (b"end", b"1"),
        (b"fox", b"1"),
        (b"lazy", b"1"),
        (b"quick", b"1"),
        (b"the", b"3"),
    ]


def test_function_styles():
    @mapper
    def returns_list(ctx, k, v):
        return [(k, v)]

    @mapper
    def returns_none(ctx, k, v):
        return None

    assert list(returns_list(CTX, b"k", b"v")) == [(b"k", b"v")]
    assert list(returns_none(CTX, b"k", b"v")) == []
    assert returns_list.__name__ == "returns_list"


def test_non_bytes_output_rejected():
    @mapper
    def emits_str(ctx, k, v):
        yield "word", b"1"

    with pytest.raises(TypeError, match="emits_str output \\(str, bytes\\)"):
        list(emits_str(CTX, b"", b""))


def test_counters():
    counts = {}
    ctx = Context(TaskID("map", 3), on_count=lambda n, d: counts.update({n: counts.get(n, 0) + d}))
    ctx.count("words")
    ctx.count("words", 4)
    assert counts == {"words": 5}
    Context(TaskID("map", 0)).count("ignored")  # no hook: a no-op


def test_job_spec_validation():
    JobSpec(WORD_COUNT, inputs=("in",), output="out", r=1)
    with pytest.raises(ValueError) as err:
        JobSpec(Program(mapper=lambda *a: (), reducer=wc_reduce), inputs=(), output="", r=0)
    for problem in ["mapper must be a @mapper", "no inputs", "no output", "at least 1"]:
        assert problem in str(err.value)


def test_job_spec_limits_match_go():
    assert (MAX_REDUCE_TASKS, MAX_MAP_TASKS) == (10_000, MAX_SPLITS)
    JobSpec(WORD_COUNT, inputs=("in",), output="out", r=MAX_REDUCE_TASKS, m=MAX_MAP_TASKS)
    with pytest.raises(ValueError) as err:
        JobSpec(WORD_COUNT, inputs=("in",), output="out", r=10_001, m=100_001)
    assert str(err.value) == (
        "number of reduce tasks r is 10001; the most is 10000; "
        "number of map tasks m is 100001; the most is 100000"
    )
