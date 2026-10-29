"""Runs the paper's example programs over the real datasets of
scripts/fetch-data.sh, in Go and in Python local mode, and checks both
against an independent oracle (docs/data.md).

Usage: check-real-data.py [--mr BIN] [--python PY] [-m M] [-r R] [--no-python] [NAME ...]

NAME is warpeace (word count), nasa (urlfreq), webgoogle (reverselinks),
chapters (invertedindex over War and Peace's chapters as files) or
commoncrawl (termvector over a Common Crawl WET file's pages); the default
is every dataset that data/input/ holds. Like
make-corpus-expected.py, the oracle is stdlib only and written from the
programs' documented behavior, without the Go or Python code it checks:
lines end at "\\n" (a "\\r" before it is dropped), words are maximal runs
of bytes that are not ASCII whitespace, and output is R files,
part-r-NNNNN, of "key<TAB>value\\n" in byte order, with each key in
partition fnv1a64(key) mod R. It prints one line per run and exits 1 if
any output differs.
"""

import argparse
import re
import subprocess
import sys
import tempfile
import time
from collections import Counter, defaultdict
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
DATA = ROOT / "data" / "input"

# name: (program, input file or glob under data/input)
DATASETS = {
    "warpeace": ("wordcount", "warpeace/warpeace.txt"),
    "nasa": ("urlfreq", "nasa/access.log"),
    "webgoogle": ("reverselinks", "webgoogle/links.tsv"),
    "chapters": ("invertedindex", "warpeace-chapters/*.txt"),
    "commoncrawl": ("termvector", "commoncrawl/pages.tsv"),
}


def fnv1a64(key: bytes) -> int:
    h = 0xCBF29CE484222325
    for b in key:
        h = ((h ^ b) * 0x100000001B3) & 0xFFFFFFFFFFFFFFFF
    return h


def lines(data: bytes):
    """Yields each line without its "\\n" or "\\r\\n"; a last line without
    a newline counts too."""
    parts = data.split(b"\n")
    if parts[-1] == b"":
        parts.pop()
    for line in parts:
        yield line[:-1] if line.endswith(b"\r") else line


def wordcount(files: list[Path]) -> dict[bytes, bytes]:
    counts = Counter(w for f in files for w in f.read_bytes().split())
    return {w: b"%d" % n for w, n in counts.items()}


def urlfreq(files: list[Path]) -> dict[bytes, bytes]:
    """Counts the seventh space-separated field of each line with seven or
    more."""
    fields = (line.split() for f in files for line in lines(f.read_bytes()))
    counts = Counter(f[6] for f in fields if len(f) >= 7)
    return {url: b"%d" % n for url, n in counts.items()}


def reverselinks(files: list[Path]) -> dict[bytes, bytes]:
    """Maps each target to its distinct sources, sorted and joined by
    commas; lines without a tab are skipped."""
    sources = defaultdict(set)
    for line in (line for f in files for line in lines(f.read_bytes())):
        source, tab, target = line.partition(b"\t")
        if tab:
            sources[target].add(source)
    return {t: b",".join(sorted(s)) for t, s in sources.items()}


def invertedindex(files: list[Path]) -> dict[bytes, bytes]:
    """Maps each word to the base names of the files it occurs in, sorted,
    without duplicates, and joined by commas."""
    where = defaultdict(set)
    for f in files:
        for w in set(f.read_bytes().split()):
            where[w].add(f.name.encode())
    return {w: b",".join(sorted(names)) for w, names in where.items()}


def termvector(files: list[Path], min_count: int = 2) -> dict[bytes, bytes]:
    """Maps each host to the words of its documents, as word:count pairs,
    most frequent first and then by word, without those that occur fewer
    than min_count times. A document is a line: a URL, a tab and its text;
    the host is the URL's part between "://" and the next "/", "?" or "#".
    Lines without a tab or a host are skipped."""
    vectors: dict[bytes, Counter] = {}
    for line in (line for f in files for line in lines(f.read_bytes())):
        url, tab, text = line.partition(b"\t")
        host = re.split(rb"[/?#]", url.partition(b"://")[2], maxsplit=1)[0]
        if tab and host:
            vectors.setdefault(host, Counter()).update(text.split())

    def encode(v: Counter) -> bytes:
        terms = sorted(v.items(), key=lambda tn: (-tn[1], tn[0]))
        return b" ".join(b"%s:%d" % (t, n) for t, n in terms if n >= min_count)

    return {host: encode(v) for host, v in vectors.items()}


ORACLES = {
    "wordcount": wordcount,
    "urlfreq": urlfreq,
    "reverselinks": reverselinks,
    "invertedindex": invertedindex,
    "termvector": termvector,
}


def partitions(result: dict[bytes, bytes], r: int) -> list[bytes]:
    parts = [bytearray() for _ in range(r)]
    for key in sorted(result):
        parts[fnv1a64(key) % r] += key + b"\t" + result[key] + b"\n"
    return [bytes(p) for p in parts]


def read_output(d: Path, r: int) -> list[bytes]:
    return [(d / f"part-r-{i:05d}").read_bytes() for i in range(r)]


def run(label: str, cmd: list[str], out: Path, want: list[bytes], r: int) -> bool:
    start = time.monotonic()
    p = subprocess.run(cmd, capture_output=True, text=True)
    took = time.monotonic() - start
    if p.returncode != 0:
        print(f"{label}: exit {p.returncode}\n{p.stdout}{p.stderr}")
        return False
    ok = read_output(out, r) == want
    print(f"{label}: {'same as the oracle' if ok else 'DIFFERS from the oracle'} ({took:.1f}s)")
    return ok


def check(name: str, args, tmp: Path) -> bool:
    program, rel = DATASETS[name]
    path = DATA / rel
    files = sorted(DATA.glob(rel))
    start = time.monotonic()
    result = ORACLES[program](files)
    want = partitions(result, args.r)
    size = sum(f.stat().st_size for f in files)
    print(
        f"{name}: {program} over {len(files):,} files, {size:,} bytes, {len(result):,} keys "
        f"(oracle {time.monotonic() - start:.1f}s)"
    )
    job = ["-m", str(args.m), "-r", str(args.r), program]
    ok = run(f"{name}: go", [args.mr, "run", "-local", *job, str(tmp / f"{name}-go"), str(path)],
             tmp / f"{name}-go", want, args.r)  # fmt: skip
    if not args.no_python:
        py = [args.python, "-m", "mapreduce_reloaded", "run", "--local"]
        ok &= run(f"{name}: python", [*py, *job, str(tmp / f"{name}-py"), str(path)],
                  tmp / f"{name}-py", want, args.r)  # fmt: skip
    return ok


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(prog="check-real-data.py")
    parser.add_argument("--mr", default=str(ROOT / "bin" / "mr"), help="the Go mr command")
    parser.add_argument("--python", default=sys.executable, help="a Python with mapreduce_reloaded")
    parser.add_argument("-m", type=int, default=16, help="map tasks (default 16)")
    parser.add_argument("-r", type=int, default=4, help="reduce tasks (default 4)")
    parser.add_argument("--no-python", action="store_true", help="run only the Go program")
    parser.add_argument("names", nargs="*", metavar="NAME")
    args = parser.parse_args(argv)
    if unknown := [n for n in args.names if n not in DATASETS]:
        parser.error(f"unknown dataset {', '.join(unknown)}; want {', '.join(DATASETS)}")
    names = args.names or [n for n, (_, rel) in DATASETS.items() if any(DATA.glob(rel))]
    if not names:
        print(
            "check-real-data: no data in data/input; run scripts/fetch-data.sh",
            file=sys.stderr,
        )
        return 1
    with tempfile.TemporaryDirectory() as tmp:
        ok = all([check(n, args, Path(tmp)) for n in names])
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
