"""Writes crawled web text to stdout, for word count over real text at
scale (E6): the text of Common Crawl's pages, one page a line, from the
WET files scripts/fetch-data.sh crawl keeps in data/input/crawl/, in file
and page order, until the first page that reaches SIZE bytes. No page is
repeated, so the vocabulary keeps growing with SIZE, unlike gen-book.py's.

LANG keeps only the pages whose first identified language, Common Crawl's
WARC-Identified-Content-Language, is that ISO 639-3 code: eng for English
pages. "all" keeps every page, in every language: Chinese, Japanese or Thai
text has no spaces, so word count reads a whole run of it as one word, and
a mix of languages has more distinct words than one has. The identification
is Common Crawl's own and is not perfect: some English pages quote other
languages.

Usage: python3 bench/gen-crawl.py SIZE LANG [DIR]
"""

import importlib.util
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
_spec = importlib.util.spec_from_file_location("wet_to_tsv", ROOT / "scripts/wet-to-tsv.py")
wet = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(wet)

if len(sys.argv) not in (3, 4):
    sys.exit("usage: gen-crawl.py SIZE LANG [DIR]")
size, lang = int(sys.argv[1]), sys.argv[2].encode()
crawl = Path(sys.argv[3]) if len(sys.argv) > 3 else ROOT / "data/input/crawl"
files = sorted(crawl.glob("*.warc.wet.gz"))
if not files:
    sys.exit(f"gen-crawl: no WET files in {crawl}; run scripts/fetch-data.sh crawl")

out, written = sys.stdout.buffer, 0
for path in files:
    with wet.gzip.open(path, "rb") as f:
        for headers, body in wet.records(f):
            if headers.get(b"warc-type") != b"conversion":
                continue
            first = headers.get(b"warc-identified-content-language", b"").split(b",")[0]
            text = b" ".join(body.split())
            if not text or (lang != b"all" and first != lang):
                continue
            out.write(text + b"\n")
            written += len(text) + 1
            if written >= size:
                sys.exit(0)
sys.exit(f"gen-crawl: {crawl} has {written} bytes of {lang.decode()} text, under {size}")
