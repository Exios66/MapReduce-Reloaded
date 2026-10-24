"""The benchmark data generators in bench/ (#57)."""

import collections
import subprocess
import sys
from pathlib import Path

import pytest

BENCH = Path(__file__).resolve().parent.parent.parent / "bench"


def generate(name, *args):
    script = BENCH / f"gen-{name}.py"
    return subprocess.run(
        [sys.executable, script, *map(str, args)], capture_output=True, check=True
    ).stdout


@pytest.mark.parametrize("name", ["words", "grep", "sort", "zipf"])
def test_reproducible(name):
    assert generate(name, 10_000) == generate(name, 10_000)
    assert generate(name, 10_000) != generate(name, 10_000, 7)


@pytest.mark.parametrize("name", ["grep", "sort"])
def test_records(name):
    out = generate(name, 1_000_050)
    lines = out.split(b"\n")
    assert lines.pop() == b""
    assert len(lines) == 10_001  # rounded up to whole records
    assert {len(line) for line in lines} == {99}


# The pattern occurs only where the generator put it, at the paper's rate.
def test_grep_matches():
    lines = generate("grep", 100 * 300_000).splitlines()
    assert sum(b"XYZ" in line for line in lines) == 3  # 300,000 * 92,337 / 10^10
    assert sum(b"XYZ" in line for line in generate("grep", 100).splitlines()) == 1


# Keys spread over the first byte that distsort partitions on.
def test_sort_keys():
    lines = generate("sort", 100 * 20_000).splitlines()
    assert lines[1][10:46] == b"  %032X  " % 1
    per_partition = collections.Counter(line[0] * 4 // 256 for line in lines)
    assert all(4_500 < n < 5_500 for n in per_partition.values()), per_partition
    assert len({line[:10] for line in lines}) == len(lines)


def test_zipf_skew():
    words = collections.Counter(generate("zipf", 200_000).split())
    total = sum(words.values())
    assert 0.07 < words[b"w1"] / total < 0.10  # 1 / H(100,000) is about 0.083
    assert words[b"w1"] > 1.7 * words[b"w2"]
    flat = collections.Counter(generate("zipf", 200_000, 42, 0).split())
    assert flat[b"w1"] < 0.001 * total
