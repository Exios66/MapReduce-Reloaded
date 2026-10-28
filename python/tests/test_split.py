"""Tests for split planning, mirroring go/internal/split."""

import os
import random
import tempfile

import pytest
from hypothesis import given
from hypothesis import strategies as st

from mapreduce_reloaded.formats import Text
from mapreduce_reloaded.split import (
    MAX_SPLITS,
    allocate,
    default_r,
    expand,
    plan,
    plan_m,
    size_for,
)


def write_files(dir, sizes) -> str:
    """Creates files of the given sizes and returns a glob matching them."""
    for i, n in enumerate(sizes):
        with open(os.path.join(dir, f"f{i}.txt"), "wb") as f:
            f.write(bytes(n))
    return os.path.join(dir, "*.txt")


@given(sizes=st.lists(st.integers(0, 300), min_size=1, max_size=4), size=st.integers(1, 100))
def test_plan_covers_input(tmp_path_factory, sizes, size):
    """Splits cover every file exactly, in order, with no gaps or overlaps,
    and none is larger than the split size."""
    splits = iter(plan(Text(), [write_files(tmp_path_factory.mktemp("in"), sizes)], size))
    for i, n in enumerate(sizes):
        end = 0
        while end < n:
            s = next(splits)
            assert os.path.basename(s.path) == f"f{i}.txt" and s.start == end
            assert 0 < s.end - s.start <= size
            end = s.end
    assert next(splits, None) is None


def test_plan_default_size(tmp_path):
    assert len(plan(Text(), [write_files(tmp_path, [10])])) == 1


@pytest.mark.parametrize("m", [1, 3, 7, 101, 500])
def test_size_for(tmp_path, m):
    glob = write_files(tmp_path, [100, 1])
    assert len(plan(Text(), [glob], size_for([glob], m))) <= m + 1
    with pytest.raises(ValueError):
        size_for([glob], 0)


def test_default_r():
    assert (default_r(0), default_r(4)) == (2, 8)


def test_expand_no_match(tmp_path):
    with pytest.raises(FileNotFoundError, match="no input matches"):
        expand([str(tmp_path / "*")])


@pytest.mark.parametrize(
    "sizes, m, want",
    [
        ([100], 8, [8]),
        ([100, 300], 4, [1, 3]),
        ([100, 300], 8, [2, 6]),
        ([1, 1, 1], 4, [2, 1, 1]),  # a tie goes to the first file
        ([5, 5, 5], 2, [1, 1, 1]),  # more files than m
        ([1, 1 << 40], 10, [1, 9]),  # a tiny file still gets one
        ([1 << 62, 1 << 62], 1 << 30, [1 << 29, 1 << 29]),
        ([], 64, []),  # every input file empty
    ],
)
def test_allocate(sizes, m, want):
    # The same table as go/internal/split TestAllocate, so both languages
    # give the same map tasks for an exact M.
    assert allocate(sizes, m) == want


def test_plan_m():
    # plan_m gives exactly m splits for files that can be cut that finely,
    # covering each file in order.
    rng = random.Random(3)
    for _ in range(200):
        sizes = [rng.randrange(1000, 6000) for _ in range(rng.randrange(1, 5))]
        m = rng.randrange(1, 21)
        with tempfile.TemporaryDirectory() as d:
            splits = plan_m(Text(), [write_files(d, sizes)], m)
            assert len(splits) == max(m, len(sizes))
            covered = {}
            for s in splits:
                assert s.start == covered.get(s.path, 0), "a gap"
                covered[s.path] = s.end
            assert [covered[os.path.join(d, f"f{i}.txt")] for i in range(len(sizes))] == sizes


def test_plan_m_skips_empty_files_and_rejects_zero(tmp_path):
    glob = write_files(tmp_path, [0, 100])
    splits = plan_m(Text(), [glob], 2)
    assert len(splits) == 2 and os.path.basename(splits[0].path) == "f1.txt"
    empty = tmp_path / "empty"
    empty.mkdir()
    assert plan_m(Text(), [write_files(empty, [0, 0])], 64) == []
    with pytest.raises(ValueError, match="at least 1"):
        plan_m(Text(), [glob], 0)


def test_plan_stops_at_max_splits(tmp_path):
    # Each file alone is under the limit; together they pass it, as in
    # go/internal/split TestPlanStopsAtMaxMapTasks.
    assert MAX_SPLITS == 100_000  # go/internal/formats.MaxSplits
    glob = write_files(tmp_path, [60_000, 60_000])
    with pytest.raises(ValueError, match="map tasks"):
        plan(Text(), [glob], 1)
    with pytest.raises(ValueError, match="map tasks"):
        plan_m(Text(), [glob], 120_000)
    assert len(plan(Text(), [glob], 2)) == 60_000
