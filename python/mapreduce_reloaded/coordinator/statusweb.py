"""Serves a coordinator's status over HTTP (§4.8), mirroring
go/internal/statusweb: a page for people, refreshed every few seconds, and
Prometheus metrics at /metrics, with the same metric names and labels as
the Go coordinator, so one dashboard reads either."""

import contextlib
import html
import json
import socket
import threading
import time
from collections.abc import Callable
from decimal import Decimal
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlsplit

from mapreduce_reloaded.coordinator.server import Status
from mapreduce_reloaded.gen.mapreduce.v1 import coordinator_pb2, types_pb2

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
<noscript><meta http-equiv="refresh" content="2"></noscript>
<meta name="color-scheme" content="light dark">
<title>MapReduce status</title>
<style>
:root {
  color-scheme: light dark;
  --bg: #f4f5f7; --panel: #fff; --text: #1c2128; --muted: #59636e;
  --line: #d0d7de; --track: #e8ebef;
  --blue: #0969da; --green: #1a7f37; --amber: #9a6700; --red: #cf222e; --purple: #8250df;
}
@media (prefers-color-scheme: dark) {
  :root {
    --bg: #0d1117; --panel: #161b22; --text: #e6edf3; --muted: #9198a1;
    --line: #30363d; --track: #21262d;
    --blue: #4493f8; --green: #3fb950; --amber: #d29922; --red: #f85149; --purple: #ab7df8;
  }
}
body.dark {
  color-scheme: dark;
  --bg: #0d1117; --panel: #161b22; --text: #e6edf3; --muted: #9198a1;
  --line: #30363d; --track: #21262d;
  --blue: #4493f8; --green: #3fb950; --amber: #d29922; --red: #f85149; --purple: #ab7df8;
}
* { box-sizing: border-box; }
body {
  max-width: 1080px; margin: 0 auto; padding: 18px 20px 24px;
  background: var(--bg); color: var(--text);
  font: 13px/1.45 ui-sans-serif, system-ui, -apple-system, "Segoe UI", sans-serif;
}
header {
  display: flex; align-items: baseline; flex-wrap: wrap; gap: 6px 14px;
  margin-bottom: 16px;
}
h1 { margin: 0; font-size: 18px; letter-spacing: -0.01em; }
h2 {
  margin: 20px 0 8px; font-size: 12px; font-weight: 600; letter-spacing: 0.08em;
  text-transform: uppercase; color: var(--muted);
}
.summary { margin: 0; color: var(--muted); }
.summary b { color: var(--text); font-weight: 600; font-variant-numeric: tabular-nums; }
.jobs { display: grid; gap: 8px; }
.job {
  padding: 10px 12px; background: var(--panel);
  border: 1px solid var(--line); border-radius: 8px;
}
.job-head { display: flex; align-items: center; flex-wrap: wrap; gap: 8px; }
.job-id { font: 600 13px/1.4 ui-monospace, SFMono-Regular, Menlo, monospace; }
.spacer { flex: 1; }
.duration { color: var(--muted); font-variant-numeric: tabular-nums; }
.badge {
  padding: 1px 8px; border: 1px solid currentColor; border-radius: 999px;
  font-size: 11px; font-weight: 600; letter-spacing: 0.04em; text-transform: uppercase;
}
.state-mapping { color: var(--blue); }
.state-reducing { color: var(--purple); }
.state-succeeded { color: var(--green); }
.state-failed { color: var(--red); }
.state-pending, .state-unspecified { color: var(--muted); }
.phases {
  display: grid; grid-template-columns: 3.2em 1fr auto; gap: 4px 10px;
  align-items: center; margin-top: 8px;
}
.phase-label { color: var(--muted); }
progress {
  width: 100%; height: 8px; border: 0; border-radius: 4px;
  background: var(--track); overflow: hidden;
}
progress::-webkit-progress-bar { background: var(--track); border-radius: 4px; }
progress::-webkit-progress-value { background: var(--bar, var(--blue)); border-radius: 4px; }
progress::-moz-progress-bar { background: var(--bar, var(--blue)); border-radius: 4px; }
progress.map { --bar: var(--blue); }
progress.reduce { --bar: var(--purple); }
.count { color: var(--muted); font-variant-numeric: tabular-nums; white-space: nowrap; }
.meta {
  display: flex; flex-wrap: wrap; gap: 2px 16px; margin-top: 8px;
  color: var(--muted); font-size: 12px;
}
.meta b { color: var(--text); font-weight: 600; font-variant-numeric: tabular-nums; }
.error { color: var(--red); overflow-wrap: anywhere; }
details { margin-top: 8px; font-size: 12px; color: var(--muted); }
summary { cursor: pointer; }
.counters { margin-top: 6px; font-variant-numeric: tabular-nums; }
table {
  width: 100%; border-collapse: collapse; font-size: 12px;
  background: var(--panel); border: 1px solid var(--line);
  border-radius: 8px; overflow: hidden;
}
th {
  padding: 5px 10px; border-bottom: 1px solid var(--line);
  color: var(--muted); font-weight: 600; text-align: left;
}
td { padding: 5px 10px; border-bottom: 1px solid var(--line); vertical-align: top; }
tbody tr:last-child td { border-bottom: 0; }
.mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
.nowrap { white-space: nowrap; }
.empty {
  margin: 0; padding: 8px 12px; color: var(--muted);
  background: var(--panel); border: 1px solid var(--line); border-radius: 8px;
}
.job:target { border-color: var(--blue); }
.job-id a { color: inherit; text-decoration: none; }
.job-id a:hover { text-decoration: underline; }
.badge::before { content: "\\25CF\\00a0"; }
.state-succeeded::before { content: "\\2713\\00a0"; }
.state-failed::before { content: "\\2715\\00a0"; }
.state-pending::before, .state-unspecified::before { content: "\\25CB\\00a0"; }
.age { color: var(--muted); }
.table-wrap {
  overflow-x: auto; border: 1px solid var(--line); border-radius: 8px;
  background: var(--panel);
}
.table-wrap table { border: 0; border-radius: 0; min-width: 480px; }
a:focus-visible, summary:focus-visible { outline: 2px solid var(--blue); outline-offset: 2px; }
@media (max-width: 640px) {
  body { padding: 12px; }
  .phases { grid-template-columns: 3em 1fr; }
  .phases .count { grid-column: 2; }
}
.bar { display: flex; align-items: center; gap: 10px; margin-bottom: 8px; }
.bar[hidden], .stale[hidden] { display: none; }
.bar button {
  padding: 2px 10px; font: inherit; color: var(--text); background: var(--panel);
  border: 1px solid var(--line); border-radius: 6px; cursor: pointer;
}
.bar button:focus-visible { outline: 2px solid var(--blue); outline-offset: 2px; }
.stale { color: var(--red); font-weight: 600; }
.timeline { margin-top: 8px; font-size: 12px; color: var(--muted); }
.timeline .row { display: grid; grid-template-columns: 11em 1fr 6em; gap: 6px; align-items: center;
  padding-top: 3px; }
.timeline .label { font-variant-numeric: tabular-nums; white-space: nowrap; overflow: hidden;
  text-overflow: ellipsis; }
.timeline .backup .label::after { content: " \\00b7 backup"; color: var(--amber); }
.timeline .lane { position: relative; height: 12px; background: var(--track); border-radius: 3px; }
.timeline .trace { position: absolute; top: 3px; height: 6px; border-radius: 3px; min-width: 2px; }
.timeline .trace.map { background: var(--blue); }
.timeline .trace.reduce { background: var(--purple); }
.timeline .trace.failed { background: var(--track); border: 1px solid var(--red); }
.timeline .trace.open { background: var(--track); border: 1px solid var(--text); }
</style>
</head>
"""


_BAR = """<div class="bar" id="bar" hidden>
<button type="button" id="pause" aria-pressed="false">Pause</button>
<span class="stale" id="stale" role="status" hidden>
No answer from the coordinator. Showing the last update.
</span>
</div>
<div id="live">
"""

_SCRIPT = """<script>
(function () {
  var live = document.getElementById("live"), bar = document.getElementById("bar");
  var btn = document.getElementById("pause"), stale = document.getElementById("stale");
  var paused = false;
  if (!window.fetch || !window.DOMParser) return;
  bar.hidden = false;
  function refresh() {
    if (paused) return;
    fetch(location.pathname + location.search, { cache: "no-store" }).then(function (r) {
      if (!r.ok) throw new Error(r.status);
      return r.text();
    }).then(function (text) {
      var next = new DOMParser().parseFromString(text, "text/html").getElementById("live");
      if (!next) throw new Error("not a status page");
      var open = [].map.call(live.querySelectorAll("details[open]"), function (d) {
        return d.closest("article").id;
      });
      live.innerHTML = next.innerHTML;
      open.forEach(function (id) {
        var a = document.getElementById(id), d = a && a.querySelector("details");
        if (d) d.open = true;
      });
      stale.hidden = true;
    }).catch(function () { stale.hidden = false; });
  }
  btn.addEventListener("click", function () {
    paused = !paused;
    btn.textContent = paused ? "Resume" : "Pause";
    btn.setAttribute("aria-pressed", String(paused));
    if (!paused) refresh();
  });
  setInterval(refresh, 2000);
})();
</script>
"""


def _clock(t: float) -> str:
    """Formats Unix time t as the server's local time of day, as Go's
    Format("15:04:05")."""
    return time.strftime("%H:%M:%S", time.localtime(t))


def _state(s: int) -> str:
    """The job state without its enum prefix, for the badge: "mapping"."""
    return coordinator_pb2.JobState.Name(s).removeprefix("JOB_STATE_").lower()


def _kind(k: int) -> str:
    """The task kind without its enum prefix, for the timeline: "map"."""
    return types_pb2.TaskKind.Name(k).removeprefix("TASK_KIND_").lower()


def _percent(done: int, total: int) -> str:
    """done as a rounded percentage of total, "0%" if there is none."""
    return "0%" if total == 0 else f"{(done * 100 + total // 2) // total}%"


def _heartbeat(at: float, last_seen: float) -> str:
    """How long before at the worker was last seen, "3s ago", or "" if it never was."""
    if last_seen <= 0:
        return ""
    return f"{go_duration(max(0, round(at - last_seen)) * 1_000_000_000)} ago"


def _counter(j: coordinator_pb2.GetJobStatusResponse, name: str) -> int:
    """One job counter, or 0 if the job has none by that name."""
    return j.counters.values.get(name, 0)


def _timeline(j: coordinator_pb2.GetJobStatusResponse, now: float, attempts) -> list[dict]:
    """Lays the job's attempts out over the job's run, as
    go/internal/statusweb.timeline: from its submission (or the first
    attempt's start) to its finish, or to now. Bars keep their proportions,
    so the drawn timeline is to scale. One dict per bar: label, left and
    width in percent, backup, its trace classes and the seconds text."""
    if not attempts:
        return []

    def stamp(ts):
        return ts.ToNanoseconds() / 1e9 if ts is not None else None

    window_start = stamp(j.submitted_at) if j.HasField("submitted_at") else None
    if window_start is None or attempts[0].started < window_start:
        window_start = attempts[0].started
    window_stop = stamp(j.finished_at) if j.HasField("finished_at") else None
    if window_stop is None or not window_stop > window_start:
        window_stop = now
    if window_stop is None or not window_stop > window_start:
        window_stop = window_start + 1.0  # no window at all
    span = window_stop - window_start
    bars = []
    for a in attempts:
        ended, open = a.ended, a.ended is None
        if open:
            ended = window_stop
        left, width = (a.started - window_start) / span * 100, (ended - a.started) / span * 100
        kind = _kind(a.kind)
        trace = kind
        if open:
            trace += " open"
        elif not a.ok:
            trace += " failed"
        bars.append(
            {
                "label": f"{kind} {a.index} #{a.attempt} {a.worker}",
                "left": max(0.0, min(left, 100.0)),
                "width": max(0.0, min(width, 100.0)),
                "backup": a.backup,
                "trace": trace,
                "seconds": f"{ended - a.started:.1f} s",
            }
        )
    return bars


def page(st: Status, dark: bool = False) -> str:
    """Renders st as the status page of go/internal/statusweb/status.html,
    escaping every value; dark adds the explicit dark-mode class."""
    e = html.escape
    body_class = ' class="dark"' if dark else ""
    out = [_HEAD, f"<body{body_class}>\n", _BAR, "<header>\n"]
    out.append("<h1>MapReduce status</h1>\n")
    out.append(
        f'<p class="summary"><b>{len(st.jobs)}</b> jobs &middot; <b>{len(st.workers)}</b> workers'
        f" &middot; <b>{len(st.lost)}</b> lost &middot; {_clock(st.at)}</p>\n</header>\n\n"
    )
    out.append('<h2>Jobs</h2>\n<section class="jobs">\n')
    for j in st.jobs:
        d = duration(j, st.at)
        state = _state(j.state)
        counters = "".join(f"{e(k)}: {v}<br>" for k, v in sorted(j.counters.values.items()))
        out.append(f'<article class="job" id="job-{e(j.job_id)}">\n<div class="job-head">\n')
        out.append(f'<span class="job-id"><a href="#job-{e(j.job_id)}">{e(j.job_id)}</a></span>\n')
        out.append(f'<span class="badge state-{state}">{state}</span>\n')
        out.append('<span class="spacer"></span>\n')
        shown = "&mdash;" if d is None else e(go_duration(d))
        out.append(f'<span class="duration">{shown}</span>\n')
        out.append('</div>\n<div class="phases">\n<span class="phase-label">map</span>\n')
        out.append(
            f'<progress class="map" max="{j.map_tasks_total}" value="{j.map_tasks_completed}"'
            f' aria-label="map tasks"></progress>\n'
            f'<span class="count">{j.map_tasks_completed}/{j.map_tasks_total} &middot;'
            f" {_percent(j.map_tasks_completed, j.map_tasks_total)}</span>\n"
        )
        out.append('<span class="phase-label">reduce</span>\n')
        out.append(
            f'<progress class="reduce" max="{j.reduce_tasks_total}"'
            f' value="{j.reduce_tasks_completed}" aria-label="reduce tasks"></progress>\n'
            f'<span class="count">{j.reduce_tasks_completed}/{j.reduce_tasks_total} &middot;'
            f" {_percent(j.reduce_tasks_completed, j.reduce_tasks_total)}</span>\n"
        )
        out.append('</div>\n<div class="meta">\n')
        out.append(f"<span>backups: {j.backup_attempts} started, {j.backup_wins} won</span>\n")
        out.append(
            f"<span>local maps: host <b>{j.map_tasks_host_local}</b>,"
            f" zone <b>{j.map_tasks_zone_local}</b></span>\n"
        )
        out.append(f"<span>map in <b>{_counter(j, 'map.input.records')}</b></span>\n")
        out.append(f"<span>map out <b>{_counter(j, 'map.output.records')}</b></span>\n")
        out.append(f"<span>reduce out <b>{_counter(j, 'reduce.output.records')}</b></span>\n")
        if j.error:
            out.append(f'<span class="error">{e(j.error)}</span>\n')
        out.append("</div>\n<details>\n<summary>all counters</summary>\n")
        out.append(f'<div class="counters">{counters}</div>\n</details>\n')
        bars = _timeline(j, st.at, st.attempts.get(j.job_id, ()))
        if bars:
            out.append('<details class="timeline">\n<summary>task timeline</summary>\n')
            for b in bars:
                extra = " backup" if b["backup"] else ""
                out.append(
                    f'<div class="row"><span class="label{extra}">{e(b["label"])}</span>'
                    f'<span class="lane"><i class="trace {e(b["trace"])}"'
                    f' style="left:{b["left"]:.2f}%;width:{b["width"]:.2f}%"></i></span>'
                    f'<span class="secs">{e(b["seconds"])}</span></div>\n'
                )
            out.append("</details>\n")
        out.append("</article>\n")
    if not st.jobs:
        out.append('<p class="empty">No jobs yet.</p>\n')
    out.append('</section>\n\n<h2>Workers</h2>\n<div class="table-wrap">\n<table>\n<thead><tr>')
    out.append(
        '<th scope="col">Worker</th><th scope="col">Host</th><th scope="col">Zone</th>'
        '<th scope="col">Slots</th><th scope="col">Last seen</th>'
        '<th scope="col">Running</th></tr></thead>\n<tbody>\n'
    )
    for w in st.workers:
        running = "".join(
            f'<span class="mono">{e(str(t))}</span> {int(w.progress.get(t, 0.0) * 100 + 0.5)}%<br>'
            for t in w.running
        )
        out.append(
            f'<tr><td class="mono">{e(w.worker.id)}</td><td>{e(w.worker.info.host)}</td>'
            f"<td>{e(w.worker.info.zone) or '&mdash;'}</td>"
            f'<td class="nowrap">{len(w.running)}/{w.worker.info.slots}</td>'
            f'<td class="nowrap">{_clock(w.worker.last_seen)}'
            f' <span class="age">{_heartbeat(st.at, w.worker.last_seen)}</span></td>'
            f"<td>{running}</td></tr>\n"
        )
    if not st.workers:
        out.append('<tr><td colspan="6">No workers.</td></tr>\n')
    out.append(
        '</tbody>\n</table>\n</div>\n\n<h2>Lost workers</h2>\n<div class="table-wrap">\n'
        "<table>\n<thead><tr>"
    )
    out.append(
        '<th scope="col">Worker</th><th scope="col">Lost at</th>'
        '<th scope="col">Tasks run again</th></tr></thead>\n<tbody>\n'
    )
    for lw in st.lost:
        tasks = "".join(f'<span class="mono">{e(str(t))}</span><br>' for t in lw.tasks)
        out.append(
            f'<tr><td class="mono">{e(lw.id)}</td><td class="nowrap">{_clock(lw.at)}</td>'
            f"<td>{tasks}</td></tr>\n"
        )
    if not st.lost:
        out.append('<tr><td colspan="3">None.</td></tr>\n')
    out.append("</tbody>\n</table>\n</div>\n</div>\n" + _SCRIPT + "</body>\n</html>\n")
    return "".join(out)


def status_json(st: Status) -> bytes:
    """Renders st as /status.json, with the keys of go/internal/statusweb/json.go."""
    jobs = []
    for j in st.jobs:
        d = duration(j, st.at)
        jobs.append(
            {
                "id": j.job_id,
                "state": _state(j.state),
                "error": j.error,
                "map_completed": j.map_tasks_completed,
                "map_total": j.map_tasks_total,
                "reduce_completed": j.reduce_tasks_completed,
                "reduce_total": j.reduce_tasks_total,
                "backup_attempts": j.backup_attempts,
                "backup_wins": j.backup_wins,
                "map_host_local": j.map_tasks_host_local,
                "map_zone_local": j.map_tasks_zone_local,
                "counters": dict(j.counters.values),
                "submitted_at": j.submitted_at.ToNanoseconds() / 1e9
                if j.HasField("submitted_at")
                else None,
                "finished_at": j.finished_at.ToNanoseconds() / 1e9
                if j.HasField("finished_at")
                else None,
                "duration_seconds": None if d is None else d / 1e9,
                "attempts": [
                    {
                        "kind": _kind(a.kind),
                        "index": a.index,
                        "attempt": a.attempt,
                        "worker": a.worker,
                        "started": a.started,
                        "ended": a.ended,
                        "backup": a.backup,
                        "ok": a.ok,
                    }
                    for a in st.attempts.get(j.job_id, ())
                ],
            }
        )
    workers = [
        {
            "id": w.worker.id,
            "host": w.worker.info.host,
            "zone": w.worker.info.zone,
            "slots": w.worker.info.slots,
            "last_seen": w.worker.last_seen or None,
            "running": [{"task": str(t), "progress": w.progress.get(t, 0.0)} for t in w.running],
        }
        for w in st.workers
    ]
    lost = [{"id": lw.id, "at": lw.at, "tasks": [str(t) for t in lw.tasks]} for lw in st.lost]
    doc = {"at": st.at, "jobs": jobs, "workers": workers, "lost": lost}
    return (json.dumps(doc) + "\n").encode()


def handler(source: Callable[[], Status]) -> type[BaseHTTPRequestHandler]:
    """Returns a request handler that serves the status page at / and the
    metrics at /metrics, taking a fresh status from source for each."""

    class Handler(BaseHTTPRequestHandler):
        timeout = READ_TIMEOUT

        def do_GET(self):
            """Serves a fresh status page or metrics; ?theme=dark asks for the
            page in dark mode. Returns HTTP 404 for other paths; source and
            rendering exceptions propagate from this method."""
            parts = urlsplit(self.path)
            if parts.path == "/":
                dark = parse_qs(parts.query).get("theme") == ["dark"]
                body, ctype = page(source(), dark).encode(), "text/html; charset=utf-8"
            elif parts.path == "/status.json":
                body, ctype = status_json(source()), "application/json"
            elif parts.path == "/metrics":
                body, ctype = metrics(source()), CONTENT_TYPE
            else:
                self.send_error(404)
                return
            self.send_response(200)
            self.send_header("Content-Type", ctype)
            self.send_header("Content-Length", str(len(body)))
            self.send_header("Cache-Control", "no-store")
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
