"""Tests for split planning, mirroring go/internal/split."""

import os

import pytest
from hypothesis import given
from hypothesis import strategies as st

from mapreduce_reloaded.formats import Text
from mapreduce_reloaded.split import default_r, expand, plan, size_for


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
