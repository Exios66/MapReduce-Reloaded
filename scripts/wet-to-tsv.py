"""Converts a Common Crawl WET file (gzipped WARC records of each page's
text) on stdin into termvector's input on stdout: one line per page, its
URL, a tab and its text with every run of ASCII whitespace made one space,
which leaves its words as word count splits them. Records other than
"conversion" (the file's warcinfo) are left out. It prints the number of
pages to stderr.

Usage: gzip -dc ... | python3 scripts/wet-to-tsv.py   (or the .gz on stdin)
"""

import gzip
import sys


def records(f):
    """Yields (headers, body) for each WARC record in f."""
    while line := f.readline():
        if not line.strip():
            continue
        if not line.startswith(b"WARC/"):
            raise ValueError(f"wet-to-tsv: expected a WARC record, got {line[:40]!r}")
        headers = {}
        while (line := f.readline().rstrip(b"\r\n")) != b"":
            name, _, value = line.partition(b":")
            headers[name.strip().lower()] = value.strip()
        yield headers, f.read(int(headers[b"content-length"]))


def main() -> int:
    raw = sys.stdin.buffer
    f = gzip.GzipFile(fileobj=raw) if raw.peek(2)[:2] == b"\x1f\x8b" else raw
    out, pages = sys.stdout.buffer, 0
    for headers, body in records(f):
        url = headers.get(b"warc-target-uri", b"")
        if headers.get(b"warc-type") != b"conversion" or not url or b"\t" in url:
            continue
        out.write(url + b"\t" + b" ".join(body.split()) + b"\n")
        pages += 1
    print(f"wet-to-tsv: {pages} pages", file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
