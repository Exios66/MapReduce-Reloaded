"""The benchmark data generators in bench/ (#57)."""

import collections
import datetime
import re
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


@pytest.mark.parametrize("name", ["words", "grep", "sort", "zipf", "weblog", "links"])
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


# Each request is a Common Log Format line whose seventh field is the URL, as
# urlfreq reads it, and the pages follow a skewed distribution.
def test_weblog():
    lines = generate("weblog", 2_000_000).splitlines()
    assert len(lines) >= 20_000
    pattern = re.compile(
        rb"^10\.0\.\d+\.\d+ - - \[10/Oct/2000:\d\d:\d\d:\d\d -0700\] "
        rb'"GET (/page\d+\.html) HTTP/1\.0" 200 \d+$'
    )
    urls = collections.Counter(pattern.match(line).group(1) for line in lines)
    assert all(line.split(b" ")[6] in urls for line in lines[:100])
    top, count = urls.most_common(1)[0]
    assert top == b"/page1.html" and count > len(lines) // 20, urls.most_common(3)
    assert len(urls) > 1_000


# Time never goes back, even after a day of requests.
def test_weblog_time_moves_forward():
    lines = generate("weblog", 30_000_000).splitlines()
    stamps = [
        datetime.datetime.strptime(line.split(b" ")[3][1:].decode(), "%d/%b/%Y:%H:%M:%S")
        for line in lines
    ]
    assert stamps == sorted(stamps)
    assert stamps[-1].date() > stamps[0].date()


# Each link is a source, a tab and a target, and a few targets get most links.
def test_links():
    lines = generate("links", 2_000_000).splitlines()
    assert len(lines) >= 20_000
    pairs = [line.split(b"\t") for line in lines]
    assert all(len(p) == 2 and p[0].startswith(b"http://example.com/p") for p in pairs)
    targets = collections.Counter(p[1] for p in pairs)
    assert targets.most_common(1)[0][0] == b"http://example.com/p1"
    assert targets.most_common(1)[0][1] > len(lines) // 20
    assert len({p[0] for p in pairs}) > 10_000  # sources are not skewed


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
