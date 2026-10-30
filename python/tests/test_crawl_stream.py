"""Large-corpus generation does not repeat pages or silently accept short text."""

import importlib.util
import io
from pathlib import Path

import pytest

SPEC = importlib.util.spec_from_file_location(
    "crawl_stream", Path(__file__).resolve().parents[2] / "bench/gen-crawl-stream.py"
)
CRAWL = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(CRAWL)


def test_crawl_emit_filters_language_and_finishes_source(monkeypatch):
    visited = []

    def records(_):
        for language, body in ((b"deu", b"ignore"), (b"eng", b"a\n b"), (b"eng", b"more")):
            visited.append(body)
            yield {b"warc-type": b"conversion", b"warc-identified-content-language": language}, body

    monkeypatch.setattr(CRAWL.WET, "records", records)
    output = io.BytesIO()
    assert CRAWL.emit(None, output, 2) == (4, 1)
    assert output.getvalue() == b"a b\n"
    assert visited == [b"ignore", b"a\n b", b"more"]


@pytest.mark.parametrize(
    "payload",
    [
        b"WARC/1.0\nContent-Length: -1\n\n",
        b"WARC/1.0\nContent-Length: 16777217\n\n",
        b"WARC/1.0\nContent-Length: 2\n\nx",
        b"WARC/1.0\n" + b"x" * 65537,
    ],
)
def test_crawl_rejects_unbounded_or_truncated_records(payload):
    with pytest.raises(ValueError):
        list(CRAWL.WET.records(io.BytesIO(payload)))
