"""bench/scrape-metrics.py and bench/plot-rates.py (#57)."""

import csv
import http.server
import io
import subprocess
import sys
import threading
import time
from pathlib import Path

import pytest

BENCH = Path(__file__).resolve().parent.parent.parent / "bench"


def run(script, *args, **kwargs):
    return subprocess.run(
        [sys.executable, BENCH / script, *map(str, args)],
        capture_output=True,
        check=True,
        text=True,
        **kwargs,
    ).stdout


def test_scrape():
    served = 0

    class Handler(http.server.BaseHTTPRequestHandler):
        def do_GET(self):
            nonlocal served
            served += 1
            body = (
                "# TYPE mr_worker_shuffle_fetched_bytes_total counter\n"
                f"mr_worker_shuffle_fetched_bytes_total {served * 1000}\n"
                'mr_worker_tasks_total{kind="map",result="ok"} 3\n'
                "mr_worker_registrations_total 1\n"
            ).encode()
            self.send_response(200)
            self.end_headers()
            self.wfile.write(body)

        def log_message(self, *args):
            pass

    server = http.server.HTTPServer(("127.0.0.1", 0), Handler)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    try:
        url = f"http://127.0.0.1:{server.server_port}/metrics"
        down = "http://127.0.0.1:1/metrics"  # nothing listens; its rounds are skipped
        rows = list(csv.DictReader(io.StringIO(run("scrape-metrics.py", 0.2, 0.5, url, down))))
    finally:
        server.shutdown()
    # Rounds at 0, 0.2 and 0.4 s, one row each: only byte counters, only from url.
    assert [r["metric"] for r in rows] == ["mr_worker_shuffle_fetched_bytes_total"] * 3
    assert {r["url"] for r in rows} == {url}
    assert [int(r["value"]) for r in rows] == [1000, 2000, 3000]
    assert [float(r["seconds"]) for r in rows] == pytest.approx([0.0, 0.2, 0.4], abs=0.05)


def test_plot_without_samples(tmp_path):
    # A run whose scraper saw no worker has only the header; plotting must
    # not fail, or bench/run.sh would stop before it wrote result.json.
    samples = tmp_path / "samples.csv"
    samples.write_text("seconds,url,metric,value\n")
    run("plot-rates.py", samples, tmp_path / "rates.png")
    assert not (tmp_path / "rates.png").exists()
    assert run("plot-rates.py", samples) == "seconds,input,shuffle,output\n"


def test_rates(tmp_path):
    samples = tmp_path / "samples.csv"
    samples.write_text(
        "seconds,url,metric,value\n"
        "0,a,mr_worker_map_input_bytes_total,0\n"
        "0,a,mr_worker_shuffle_fetched_bytes_total,0\n"
        "0,a,mr_worker_reduce_output_bytes_total,0\n"
        "0,b,mr_worker_map_input_bytes_total,0\n"
        "1,a,mr_worker_map_input_bytes_total,2000000\n"
        "1,b,mr_worker_map_input_bytes_total,4000000\n"
        # b misses this round, so it counts with its last value.
        "3,a,mr_worker_map_input_bytes_total,4000000\n"
        "3,a,mr_worker_shuffle_fetched_bytes_total,6000000\n"
        "3,a,mr_worker_reduce_output_bytes_total,1000000\n"
        # c is first seen here: what it counted before adds nothing.
        "3,c,mr_worker_map_input_bytes_total,5000000\n"
        # a restarted: its counter counts from zero again.
        "4,a,mr_worker_map_input_bytes_total,1000000\n"
        "4,c,mr_worker_map_input_bytes_total,6000000\n"
    )
    rows = list(csv.reader(io.StringIO(run("plot-rates.py", samples))))
    assert rows == [
        ["seconds", "input", "shuffle", "output"],
        ["1.000", "6.000", "0.000", "0.000"],
        ["3.000", "1.000", "3.000", "0.500"],
        ["4.000", "2.000", "0.000", "0.000"],
    ]


def test_unplotted_metrics_cost_nothing(tmp_path):
    # A sample file that introduces a new series at every timestamp must not
    # make the work grow with the square of its length: only the three
    # plotted metrics are kept.
    samples = tmp_path / "samples.csv"
    with samples.open("w") as f:
        f.write("seconds,url,metric,value\n")
        f.write("0,a,mr_worker_map_input_bytes_total,0\n")
        for i in range(1, 30_000):
            f.write(f"{i},a,other_metric_{i},1\n")
        f.write("30000,a,mr_worker_map_input_bytes_total,30000000000\n")
    start = time.monotonic()
    rows = list(csv.reader(io.StringIO(run("plot-rates.py", samples))))
    assert time.monotonic() - start < 10
    # Only the two rounds with a plotted metric remain: 30 GB over 30000 s.
    assert rows == [
        ["seconds", "input", "shuffle", "output"],
        ["30000.000", "1.000", "0.000", "0.000"],
    ]
