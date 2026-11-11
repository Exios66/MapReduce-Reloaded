"""Stream at least SIZE bytes of English crawl text without repeating source files.

Usage: gen-crawl-stream.py SIZE SOURCE_MANIFEST
Downloads successive WET files from one fixed public CC-MAIN-2024-10 segment,
one at a time, and records their observed SHA-256 identities (not publisher
checksum verification). Temporary compressed copies are removed after reading;
the prepared text shards and source manifest are the retained dataset.
"""

import gzip
import hashlib
import importlib.util
import json
import shutil
import sys
import tempfile
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location("wet", ROOT / "scripts/wet-to-tsv.py")
WET = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(WET)
BASE = "https://data.commoncrawl.org/crawl-data/CC-MAIN-2024-10/segments/1707947473347.0/wet"


def emit(stream, output, remaining):
    written = pages = 0
    for headers, body in WET.records(stream):
        if written >= remaining:
            continue  # Finish the gzip stream to check its CRC before accepting it.
        language = headers.get(b"warc-identified-content-language", b"").split(b",")[0]
        if headers.get(b"warc-type") != b"conversion" or language != b"eng":
            continue
        text = b" ".join(body.split())
        if text:
            output.write(text + b"\n")
            written += len(text) + 1
            pages += 1
    return written, pages


def main():
    if len(sys.argv) != 3 or int(sys.argv[1]) <= 0:
        raise SystemExit(__doc__)
    target = int(sys.argv[1])
    manifest = Path(sys.argv[2])
    if manifest.exists():
        raise FileExistsError(manifest)
    sources = []
    written = pages = 0
    with tempfile.TemporaryDirectory(prefix="crawl-download-", dir=ROOT / "data") as temporary:
        for index in range(200):
            if shutil.disk_usage(temporary).free < 3 << 30:
                raise ValueError("crawl-stream: under 3 GiB free; retaining partial input")
            url = f"{BASE}/CC-MAIN-20240220211055-20240221001055-{index:05d}.warc.wet.gz"
            path = Path(temporary) / "source.gz"
            digest = hashlib.sha256()
            stored = 0
            with urllib.request.urlopen(url, timeout=120) as response, path.open("wb") as output:
                if not response.geturl().startswith(BASE + "/"):
                    raise ValueError("unexpected source redirect")
                while block := response.read(1 << 20):
                    stored += len(block)
                    if stored > 512 << 20:
                        raise ValueError("crawl-stream: compressed source exceeds 512 MiB")
                    output.write(block)
                    digest.update(block)
            with gzip.open(path, "rb") as stream:
                count, documents = emit(stream, sys.stdout.buffer, target - written)
            written += count
            pages += documents
            sources.append(
                {
                    "url": url,
                    "sha256": digest.hexdigest(),
                    "compressed_bytes": path.stat().st_size,
                    "emitted_bytes": count,
                    "emitted_pages": documents,
                }
            )
            print(
                f"crawl-stream: sources={index + 1} bytes={written} pages={pages}",
                file=sys.stderr,
                flush=True,
            )
            if written >= target:
                manifest.write_text(
                    json.dumps(
                        dict(
                            target_bytes=target,
                            logical_bytes=written,
                            pages=pages,
                            language="eng",
                            repeated_source_files=False,
                            checksum_provenance="observed through HTTPS, not publisher-pinned",
                            sources=sources,
                        ),
                        indent=2,
                    )
                    + "\n"
                )
                return
    raise ValueError("fixed source segment exhausted before target; no repetition or shrinkage")


if __name__ == "__main__":
    main()
