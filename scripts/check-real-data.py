"""Runs the paper's example programs over the real datasets of
scripts/fetch-data.sh, in Go and in Python local mode, and checks both
against an independent oracle (docs/data.md).

Usage: check-real-data.py [--mr BIN] [--python PY] [-m M] [-r R] [--no-python] [NAME ...]

NAME is warpeace (word count), nasa (urlfreq) or webgoogle (reverselinks);
the default is every dataset that data/input/ holds. Like
make-corpus-expected.py, the oracle is stdlib only and written from the
programs' documented behavior, without the Go or Python code it checks:
lines end at "\\n" (a "\\r" before it is dropped), words are maximal runs
of bytes that are not ASCII whitespace, and output is R files,
part-r-NNNNN, of "key<TAB>value\\n" in byte order, with each key in
partition fnv1a64(key) mod R. It prints one line per run and exits 1 if
any output differs.
"""

import argparse
import subprocess
import sys
import tempfile
import time
from collections import Counter, defaultdict
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
DATA = ROOT / "data" / "input"

# name: (program, input file under data/input)
DATASETS = {
    "warpeace": ("wordcount", "warpeace/warpeace.txt"),
    "nasa": ("urlfreq", "nasa/access.log"),
    "webgoogle": ("reverselinks", "webgoogle/links.tsv"),
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


def wordcount(data: bytes) -> dict[bytes, bytes]:
    return {w: b"%d" % n for w, n in Counter(data.split()).items()}


def urlfreq(data: bytes) -> dict[bytes, bytes]:
    """Counts the seventh space-separated field of each line with seven or
    more."""
    counts = Counter(f[6] for f in map(bytes.split, lines(data)) if len(f) >= 7)
    return {url: b"%d" % n for url, n in counts.items()}


def reverselinks(data: bytes) -> dict[bytes, bytes]:
    """Maps each target to its distinct sources, sorted and joined by
    commas; lines without a tab are skipped."""
    sources = defaultdict(set)
    for line in lines(data):
        source, tab, target = line.partition(b"\t")
        if tab:
            sources[target].add(source)
    return {t: b",".join(sorted(s)) for t, s in sources.items()}


ORACLES = {"wordcount": wordcount, "urlfreq": urlfreq, "reverselinks": reverselinks}


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
    data = path.read_bytes()
    start = time.monotonic()
    result = ORACLES[program](data)
    want = partitions(result, args.r)
    print(
        f"{name}: {program} over {len(data):,} bytes, {len(result):,} keys "
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
    names = args.names or [n for n, (_, rel) in DATASETS.items() if (DATA / rel).exists()]
    if not names:
        print("check-real-data: no dataset in data/input; run scripts/fetch-data.sh", file=sys.stderr)
        return 1
    with tempfile.TemporaryDirectory() as tmp:
        ok = all([check(n, args, Path(tmp)) for n in names])
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
