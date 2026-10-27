"""Regenerates testdata/examples/: inputs and expected outputs for the example
programs other than word count, whose golden output is testdata/corpus/expected/.

An oracle independent of both implementations, like make-corpus-expected.py:
stdlib only, written from the programs' descriptions in their doc comments,
not from either implementation. jobs.json names each program's inputs,
relative to the repository, and parameters, for the tests. For each program it writes the expected
output with R = 1 and R = 4, as expected/r1/ and expected/r4/, each holding
part-r-NNNNN files of "key<TAB>value\\n" records in byte order of key.

Inputs, each in a program's input/ unless it reads the corpus:
- grep, invertedindex and distsort read testdata/corpus/raven.txt and edge.txt;
- urlfreq and reverselinks read the first lines of bench/gen-weblog.py and
  bench/gen-links.py, plus a few malformed lines;
- termvector reads documents made from raven.txt's lines, on a few hosts.

edge.txt holds what the implementations could disagree on: non-ASCII
spaces, which do not split words; ASCII vertical tab and form feed, which
do; empty lines; tabs inside lines; and bytes that are not UTF-8.
"""

import json
import subprocess
import sys
from collections import Counter, defaultdict
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
CORPUS = [ROOT / "testdata" / "corpus" / "raven.txt"]  # the smallest text keeps the files small
OUT = ROOT / "testdata" / "examples"
GREP_PATTERN = b"said"
GENERATED_LINES = 2000

EDGE = (
    b"plain words here\r\n"
    b"\r\n"
    b"\n"
    b"no\xc2\xa0break space and\xe2\x80\x83em space\n"
    b"vertical\x0btab and form\x0cfeed split\n"
    b"tab\tinside\tthe line, said the tester\n"
    b"not utf-8: \xff\xfe said \x80\n"
    b"  leading and trailing spaces  \n"
    b"said\n"
    b"said\n"
    b"last line without a newline"
)


def fnv1a64(key):
    h = 0xCBF29CE484222325
    for b in key:
        h = ((h ^ b) * 0x100000001B3) & 0xFFFFFFFFFFFFFFFF
    return h


def hash_partition(key, r):
    return fnv1a64(key) % r


def range_partition(key, r):
    return key[0] * r // 256 if key else 0


def lines(path):
    """The text format's records: lines without "\\n" or "\\r\\n"."""
    *ended, last = Path(path).read_bytes().split(b"\n")
    out = [p.removesuffix(b"\r") for p in ended]
    return out + [last] if last else out  # a last line without "\n" keeps any "\r"


def words(line):
    return line.split()  # maximal runs of bytes that are not ASCII whitespace


def write(program, records, partition=hash_partition):
    """Writes records, (key, value) pairs in output order, for R = 1 and 4."""
    records = sorted(records, key=lambda kv: kv[0])  # stable: equal keys keep their order
    for r in (1, 4):
        parts = [bytearray() for _ in range(r)]
        for key, value in records:
            parts[partition(key, r)] += key + b"\t" + value + b"\n"
        d = OUT / program / "expected" / f"r{r}"
        d.mkdir(parents=True, exist_ok=True)
        for old in d.glob("part-r-*"):
            old.unlink()
        for i, data in enumerate(parts):
            (d / f"part-r-{i:05d}").write_bytes(data)
    print(f"{program}: {len(records)} records")


def text_inputs():
    return [ROOT / p for p in TEXT]


def grep():
    hits = [line for p in text_inputs() for line in lines(p) if GREP_PATTERN in line]
    write("grep", [(line, b"") for line in hits])


def invertedindex():
    files = defaultdict(set)
    for p in text_inputs():
        for line in lines(p):
            for w in words(line):
                files[w].add(p.name.encode())
    write("invertedindex", [(w, b",".join(sorted(fs))) for w, fs in files.items()])


def distsort():
    write("distsort", [(line, b"") for p in text_inputs() for line in lines(p)], range_partition)


def generated(script, malformed):
    out = subprocess.run(
        [sys.executable, str(ROOT / "bench" / script), "1"], check=True, capture_output=True
    ).stdout
    return b"".join(out.splitlines(keepends=True)[:GENERATED_LINES]) + malformed


def urlfreq():
    path = OUT / "urlfreq" / "input" / "access.log"
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(
        generated(
            "gen-weblog.py",
            b"too few fields here\n"
            b"\n"
            b'1.2.3.4 - - [10/Oct/2000:13:55:36 -0700] "GET /page1.html HTTP/1.0" 200 1\r\n'
            b"a\x0bb c d e f /vt.html\n"
            b"a b c d e f /nbsp\xc2\xa0page.html\n",
        )
    )
    counts = Counter()
    for line in lines(path):
        fields = words(line)
        if len(fields) >= 7:
            counts[fields[6]] += 1
    write("urlfreq", [(url, b"%d" % n) for url, n in counts.items()])


def reverselinks():
    path = OUT / "reverselinks" / "input" / "links.tsv"
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(
        generated(
            "gen-links.py",
            b"no tab on this line\n"
            b"\n"
            b"http://example.com/p2\thttp://example.com/p1\r\n"
            b"http://example.com/p2\thttp://example.com/p1\n"
            b"\tempty source\n"
            b"empty target\t\n"
            b"a\tb\tc\n",
        )
    )
    sources = defaultdict(set)
    for line in lines(path):
        source, tab, target = line.partition(b"\t")
        if tab:
            sources[target].add(source)
    write("reverselinks", [(t, b",".join(sorted(s))) for t, s in sources.items()])


def termvector():
    hosts = [b"http://a.example", b"https://b.example:8080", b"ftp://c.example"]
    paths = [b"/doc%d", b"?q=%d", b"#s%d", b""]
    docs = bytearray()
    n = 0
    for p in CORPUS:
        for line in lines(p):
            if line.strip():
                path = paths[n % 4] % n if n % 4 < 3 else b""
                docs += hosts[n % 3] + path + b"\t" + line + b"\n"
                n += 1
    docs += (
        b"no tab, so skipped\n"
        b"http://\tno host, so skipped\n"
        b"no-scheme.example/x\tno scheme, so no host\n"
        b"http://d.example/\x0bvertical\x0btab\xc2\xa0nbsp\xc2\xa0nbsp\t the the a\x0ba\xc2\xa0b\n"
        b"http://d.example/2\tthe the a\xc2\xa0b\n"
    )
    path = OUT / "termvector" / "input" / "docs.tsv"
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(docs)
    vectors = defaultdict(Counter)
    for line in lines(path):
        url, tab, text = line.partition(b"\t")
        _, scheme, rest = url.partition(b"://")
        host = rest
        for sep in b"/?#":
            host = host.split(bytes([sep]), 1)[0]
        if tab and scheme and host:
            vectors[host].update(words(text))
    out = []
    for host, counts in vectors.items():
        terms = sorted((t for t in counts if counts[t] >= 2), key=lambda t: (-counts[t], t))
        out.append((host, b" ".join(b"%s:%d" % (t, counts[t]) for t in terms)))
    write("termvector", out)


TEXT = ["testdata/corpus/raven.txt", "testdata/examples/edge.txt"]
JOBS = {
    "grep": {"inputs": TEXT, "params": {"pattern": GREP_PATTERN.decode()}},
    "invertedindex": {"inputs": TEXT, "params": {}},
    "distsort": {"inputs": TEXT, "params": {}},
    "urlfreq": {"inputs": ["testdata/examples/urlfreq/input/*"], "params": {}},
    "reverselinks": {"inputs": ["testdata/examples/reverselinks/input/*"], "params": {}},
    "termvector": {"inputs": ["testdata/examples/termvector/input/*"], "params": {}},
}

OUT.mkdir(exist_ok=True)
(OUT / "edge.txt").write_bytes(EDGE)
(OUT / "jobs.json").write_text(json.dumps(JOBS, indent=2) + "\n")
grep()
invertedindex()
distsort()
urlfreq()
reverselinks()
termvector()
