"""Serves a coordinator's status over HTTP (§4.8), mirroring
go/internal/statusweb: a page for people, refreshed every few seconds, and
Prometheus metrics at /metrics, with the same metric names and labels as
the Go coordinator, so one dashboard reads either."""

import contextlib
import html
import socket
import threading
import time
from collections.abc import Callable
from decimal import Decimal
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlsplit

from mapreduce_reloaded.coordinator.server import Status
from mapreduce_reloaded.gen.mapreduce.v1 import coordinator_pb2

# The Content-Type of a /metrics response: the text format, version 0.0.4.
CONTENT_TYPE = "text/plain; version=0.0.4; charset=utf-8"

# How long a client may take to send its request, in seconds, as the Go
# server's ReadHeaderTimeout: a stalled one would hold a thread forever.
READ_TIMEOUT = 5.0


def label(v: str) -> str:
    """Quotes v as a label value, as go/internal/promtext.Label."""
    return '"' + v.replace("\\", "\\\\").replace('"', '\\"').replace("\n", "\\n") + '"'


def duration(j: coordinator_pb2.GetJobStatusResponse, now: float) -> int | None:
    """Returns how long j has run in nanoseconds, as of now (Unix seconds) if
    it has not finished, or None if its submission time is unknown. It is 0
    if the clock was set back since: the times are wall-clock times."""
    if not j.HasField("submitted_at"):
        return None
    end = j.finished_at.ToNanoseconds() if j.HasField("finished_at") else round(now * 1e9)
    return max(0, end - j.submitted_at.ToNanoseconds())


def go_float(x: float) -> str:
    """Formats finite x as Go's %g: the shortest digits that read back as x,
    in exponent form for nonzero magnitudes below 1e-4 or from 1e6 on."""
    if x == 0:
        return "0"
    t = Decimal(repr(x)).normalize().as_tuple()
    digits = "".join(map(str, t.digits))
    exp = len(digits) + t.exponent - 1
    sign = "-" if t.sign else ""
    if exp < -4 or exp >= 6:
        mant = digits[0] + ("." + digits[1:] if len(digits) > 1 else "")
        return f"{sign}{mant}e{'-' if exp < 0 else '+'}{abs(exp):02d}"
    if exp < 0:
        return f"{sign}0.{'0' * (-exp - 1)}{digits}"
    whole, frac = digits[: exp + 1].ljust(exp + 1, "0"), digits[exp + 1 :]
    return sign + whole + ("." + frac if frac else "")


def go_duration(ns: int) -> str:
    """Formats ns, which is not negative, as Go's Duration.Round(100ms).String():
    "0s", "300ms", "2.5s", "1m30s" or "1h0m0.1s"."""
    tenths = (ns + 50_000_000) // 100_000_000
    if tenths == 0:
        return "0s"
    if tenths < 10:
        return f"{tenths * 100}ms"
    h, rest = divmod(tenths, 36_000)
    m, rest = divmod(rest, 600)
    s, tenth = divmod(rest, 10)
    out = f"{h}h" if h else ""
    if h or m:
        out += f"{m}m"
    return out + f"{s}" + (f".{tenth}" if tenth else "") + "s"


def metrics(st: Status) -> bytes:
    """Renders st in the Prometheus text format, version 0.0.4, as the Go
    coordinator does. Job durations are in seconds, ending at the finish
    time or st.at for running jobs; jobs without a submission time have no
    duration sample."""
    lines: list[str] = []

    def family(name: str, help: str):
        """Adds HELP and gauge TYPE declarations for a metric family."""
        lines.extend([f"# HELP {name} {help}", f"# TYPE {name} gauge"])

    family("mr_workers", "Live workers.")
    lines.append(f"mr_workers {len(st.workers)}")
    family("mr_job_tasks", "Tasks of each job, by kind and state.")
    for j in st.jobs:
        for kind, state, n in (
            ("map", "total", j.map_tasks_total),
            ("map", "completed", j.map_tasks_completed),
            ("reduce", "total", j.reduce_tasks_total),
            ("reduce", "completed", j.reduce_tasks_completed),
        ):
            lines.append(f'mr_job_tasks{{job={label(j.job_id)},kind="{kind}",state="{state}"}} {n}')
    family("mr_job_duration_seconds", "Time since each job was submitted, until it finished.")
    for j in st.jobs:
        d = duration(j, st.at)
        if d is not None:
            lines.append(f"mr_job_duration_seconds{{job={label(j.job_id)}}} {go_float(d / 1e9)}")
    family(
        "mr_job_backups",
        "Backup attempts of each job's last tasks (§3.6): started, and won by finishing first.",
    )
    for j in st.jobs:
        lines.append(f'mr_job_backups{{job={label(j.job_id)},state="started"}} {j.backup_attempts}')
        lines.append(f'mr_job_backups{{job={label(j.job_id)},state="won"}} {j.backup_wins}')
    family(
        "mr_job_map_tasks_local",
        "Completed map tasks that ran on the host or in the zone that held their split (§3.4).",
    )
    for j in st.jobs:
        lines.append(
            f'mr_job_map_tasks_local{{job={label(j.job_id)},level="host"}} {j.map_tasks_host_local}'
        )
        lines.append(
            f'mr_job_map_tasks_local{{job={label(j.job_id)},level="zone"}} {j.map_tasks_zone_local}'
        )
    family("mr_job_counter", "Counters of each job's accepted attempts (§4.9).")
    for j in st.jobs:
        counts = j.counters.values
        for name in sorted(counts):
            sample = f"mr_job_counter{{job={label(j.job_id)},name={label(name)}}}"
            lines.append(f"{sample} {counts[name]}")
    return ("\n".join(lines) + "\n").encode()


_HEAD = """<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta http-equiv="refresh" content="2">
<title>MapReduce status</title>
<style>
body { font: 14px system-ui, sans-serif; margin: 2em; }
table { border-collapse: collapse; margin-bottom: 2em; }
th, td { border: 1px solid #ccc; padding: 4px 8px; text-align: left; }
progress { width: 8em; }
</style>
</head>
<body>
<h1>MapReduce status</h1>
"""


def _clock(t: float) -> str:
    """Formats Unix time t as the server's local time of day, as Go's
    Format("15:04:05")."""
    return time.strftime("%H:%M:%S", time.localtime(t))


def page(st: Status) -> str:
    """Renders st as the status page of go/internal/statusweb/status.html,
    escaping every value."""
    e = html.escape
    out = [_HEAD, "<h2>Jobs</h2>\n<table>\n<tr><th>Job</th><th>State</th><th>Duration</th>"]
    out.append("<th>Map</th><th>Reduce</th><th>Backups</th><th>Counters</th><th>Error</th></tr>\n")
    for j in st.jobs:
        d = duration(j, st.at)
        counters = "".join(f"{e(k)}: {v}<br>" for k, v in sorted(j.counters.values.items()))
        out.append(
            f"<tr>\n<td>{e(j.job_id)}</td>\n"
            f"<td>{coordinator_pb2.JobState.Name(j.state)}</td>\n"
            f"<td>{'' if d is None else go_duration(d)}</td>\n"
            f'<td><progress max="{j.map_tasks_total}" value="{j.map_tasks_completed}"></progress>'
            f" {j.map_tasks_completed}/{j.map_tasks_total}</td>\n"
            f'<td><progress max="{j.reduce_tasks_total}" value="{j.reduce_tasks_completed}">'
            f"</progress> {j.reduce_tasks_completed}/{j.reduce_tasks_total}</td>\n"
            f"<td>{j.backup_attempts} started, {j.backup_wins} won</td>\n"
            f"<td>{counters}</td>\n<td>{e(j.error)}</td>\n</tr>\n"
        )
    if not st.jobs:
        out.append('<tr><td colspan="8">No jobs yet.</td></tr>\n')
    out.append("</table>\n\n<h2>Workers</h2>\n<table>\n")
    out.append("<tr><th>Worker</th><th>Host</th><th>Last seen</th><th>Running</th></tr>\n")
    for w in st.workers:
        running = "".join(f"{e(str(t))}<br>" for t in w.running)
        out.append(
            f"<tr><td>{e(w.worker.id)}</td><td>{e(w.worker.info.host)}</td>"
            f"<td>{_clock(w.worker.last_seen)}</td><td>{running}</td></tr>\n"
        )
    if not st.workers:
        out.append('<tr><td colspan="4">No workers.</td></tr>\n')
    out.append("</table>\n\n<h2>Lost workers</h2>\n<table>\n")
    out.append("<tr><th>Worker</th><th>Lost at</th><th>Tasks run again</th></tr>\n")
    for lw in st.lost:
        tasks = "".join(f"{e(str(t))}<br>" for t in lw.tasks)
        out.append(f"<tr><td>{e(lw.id)}</td><td>{_clock(lw.at)}</td><td>{tasks}</td></tr>\n")
    if not st.lost:
        out.append('<tr><td colspan="3">None.</td></tr>\n')
    out.append("</table>\n</body>\n</html>\n")
    return "".join(out)


def handler(source: Callable[[], Status]) -> type[BaseHTTPRequestHandler]:
    """Returns a request handler that serves the status page at / and the
    metrics at /metrics, taking a fresh status from source for each."""

    class Handler(BaseHTTPRequestHandler):
        timeout = READ_TIMEOUT

        def do_GET(self):
            """Serves a fresh status page or metrics, ignoring query strings.
            Returns HTTP 404 for other paths; source and rendering
            exceptions propagate from this method."""
            route = urlsplit(self.path).path
            if route == "/":
                body, ctype = page(source()).encode(), "text/html; charset=utf-8"
            elif route == "/metrics":
                body, ctype = metrics(source()), CONTENT_TYPE
            else:
                self.send_error(404)
                return
            self.send_response(200)
            self.send_header("Content-Type", ctype)
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def log_message(self, format, *args):
            """Suppresses HTTP request and error log messages."""
            pass  # as the Go server, which logs no requests

    return Handler


class _Server(ThreadingHTTPServer):
    daemon_threads = True


class _Server6(_Server):
    """Listens on IPv6 and, for "::", on IPv4 too, as Go's net.Listen."""

    address_family = socket.AF_INET6

    def server_bind(self):
        """Binds IPv6, attempting to accept IPv4 on the same socket.
        Ignores OSError when enabling IPv4; bind errors propagate."""
        with contextlib.suppress(OSError):
            self.socket.setsockopt(socket.IPPROTO_IPV6, socket.IPV6_V6ONLY, 0)
        super().server_bind()


def serve(address: tuple[str, int], source: Callable[[], Status]) -> ThreadingHTTPServer:
    """Serves the status page and metrics at address, such as ("", 8080), on
    a daemon thread, and returns the server; port 0 picks a free one. An
    empty host means every interface, over IPv6 and IPv4, or IPv4 alone
    where the host has no IPv6; a host such as "::1" is IPv6. Stop it with
    shutdown and server_close. Raises OSError if it cannot listen."""
    host, port = address
    if ":" in host:
        srv = _Server6(address, handler(source))
    elif host:
        srv = _Server(address, handler(source))
    else:
        try:
            srv = _Server6(("::", port), handler(source))
        except OSError:
            srv = _Server(address, handler(source))
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    return srv
