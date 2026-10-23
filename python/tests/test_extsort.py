"""Tests for the external sorter, mirroring go/internal/extsort."""

import os
import random
from itertools import islice

import pytest

from mapreduce_reloaded.extsort import DEFAULT_BUDGET, Sorter, groups, merge


def random_records(n):
    """n records with many duplicate keys; each value is its position."""
    rng = random.Random(1)
    return [(b"k%d" % rng.randrange(50), b"%d" % i) for i in range(n)]


def sort_all(records, tmp_path, budget):
    sorter = Sorter(dir=str(tmp_path), budget=budget)
    for key, value in records:
        sorter.add(key, value)
    return list(sorter.sorted()), sorter


@pytest.mark.parametrize("budget", [1, 2048, DEFAULT_BUDGET])
def test_sort(tmp_path, budget):
    records = random_records(1000)
    got, sorter = sort_all(records, tmp_path, budget)
    assert got == sorted(records, key=lambda r: r[0])  # stable
    if budget == 2048:
        assert sorter.runs >= 3
    if budget == DEFAULT_BUDGET:
        assert sorter.runs == 0 and os.listdir(tmp_path) == []
    sorter.close()


def test_edge_keys(tmp_path):
    records = [(b"ab", b"1"), (b"", b""), (b"a", b"2"), (b"b\x00", b"3"), (b"b", b"4")]
    got, _ = sort_all(records, tmp_path, 1)
    assert [k for k, _ in got] == [b"", b"a", b"ab", b"b", b"b\x00"]


def test_close_removes_runs(tmp_path):
    with Sorter(dir=str(tmp_path), budget=1) as sorter:
        sorter.add(b"k", b"v")
        assert os.listdir(tmp_path)
    assert os.listdir(tmp_path) == []


@pytest.mark.parametrize("limit", [None, 0, 1])
def test_groups(tmp_path, limit):
    records, _ = sort_all(random_records(500), tmp_path, DEFAULT_BUDGET)
    keys, total = [], 0
    for key, values in groups(records):
        keys.append(key)
        total += len(list(islice(values, limit)))  # None reads every value
    assert keys == sorted(set(keys)) and len(keys) == 50
    if limit is None:
        assert total == 500


def test_merge_is_stable():
    assert list(merge([(b"a", b"1"), (b"b", b"1")], [(b"a", b"2")])) == [
        (b"a", b"1"),
        (b"a", b"2"),
        (b"b", b"1"),
    ]
