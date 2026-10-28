"""Serves the coordinator ("master", §3.2) of a cluster, mirroring
go/cmd/coordinator, with the same flags:

    python -m mapreduce_reloaded.coordinator [-addr :7070] [-http :8080]
        [-data-dir dir] [-backups=false] [-keep-jobs n] [-max-task-failures n]
        [-log-format text|json]

It runs until interrupted or terminated, then tells workers to exit. With
-data-dir it logs every decision there, and a restarted coordinator resumes
its job from that log (§3.3). With -http it serves a status page and
Prometheus metrics (§4.8). It keeps the -keep-jobs most recent ended jobs
for mr status and forgets older ones. Go and Python workers and the mr
command work with either coordinator. It exits with status 2 for bad flags
and 1 for any other error.
"""

import argparse
import fcntl
import json
import logging
import os
import signal
import sys
import threading
import time
from collections.abc import Callable
from dataclasses import dataclass
from datetime import datetime

from mapreduce_reloaded.coordinator import statusweb
from mapreduce_reloaded.coordinator.recover import recover
from mapreduce_reloaded.coordinator.server import Config, Server, serve, start
from mapreduce_reloaded.coordinator.tasktable import SKIP_AFTER

log = logging.getLogger("mapreduce_reloaded.coordinator")

# How long idle workers get to hear that they should exit, as Go's one
# retry interval, before the coordinator stops serving.
EXIT_GRACE = 1.0


@dataclass
class Options:
    """The coordinator's flags."""

    addr: str = ":7070"
    http: str = ":8080"
    data_dir: str = ""
    backups: bool = True
    keep_jobs: int = 100
    max_task_failures: int = 8
    log_format: str = "text"


def _bool(v: str) -> bool:
    """Parses a boolean flag value as Go's strconv.ParseBool does.
    Raises argparse.ArgumentTypeError for an unrecognized value."""
    if v in ("1", "t", "T", "true", "TRUE", "True"):
        return True
    if v in ("0", "f", "F", "false", "FALSE", "False"):
        return False
    raise argparse.ArgumentTypeError(f"invalid boolean value {v!r}")


def parse(args: list[str]) -> Options:
    """Parses the coordinator's flags, which take one dash or two, as Go's
    flag package. Raises SystemExit with status 2 for bad flags, and 0 for
    -h, after printing to stderr or stdout."""
    p = argparse.ArgumentParser(prog="coordinator", allow_abbrev=False)
    d = Options()
    p.add_argument("-addr", "--addr", default=d.addr, help="address to listen on")
    p.add_argument(
        "-http",
        "--http",
        default=d.http,
        help="address of the status page and /metrics (empty: none)",
    )
    p.add_argument(
        "-data-dir",
        "--data-dir",
        default=d.data_dir,
        help="directory for the write-ahead log (default: none, so a restart loses the job)",
    )
    p.add_argument(
        "-backups",
        "--backups",
        type=_bool,
        nargs="?",
        const=True,
        default=d.backups,
        help="run backup attempts of the last tasks of each phase",
    )
    p.add_argument(
        "-keep-jobs",
        "--keep-jobs",
        type=int,
        default=d.keep_jobs,
        help="ended jobs to keep for mr status; older ones are forgotten (0: all)",
    )
    p.add_argument(
        "-max-task-failures",
        "--max-task-failures",
        type=int,
        default=d.max_task_failures,
        help="fail the job once one task has failed this many times (0: retry for ever)",
    )
    p.add_argument(
        "-log-format", "--log-format", default=d.log_format, help="log format: text or json"
    )
    a = p.parse_args(args)
    o = Options(
        a.addr, a.http, a.data_dir, a.backups, a.keep_jobs, a.max_task_failures, a.log_format
    )
    if o.keep_jobs < 0:
        p.error(f"-keep-jobs is {o.keep_jobs}; it must be 0 (all) or more")
    if o.max_task_failures != 0 and o.max_task_failures <= SKIP_AFTER:
        # Skipping a bad record takes SKIP_AFTER crashes, then one more attempt.
        p.error(
            f"-max-task-failures is {o.max_task_failures}; "
            f"it must be 0 (no limit) or more than {SKIP_AFTER}"
        )
    if o.log_format not in ("text", "json"):
        p.error(f"log format {o.log_format!r} is not text or json")
    return o


def config(o: Options) -> Config:
    """Returns the coordinator's configuration for o, with Go's timings."""
    return Config(
        heartbeat_interval=3.0,
        worker_timeout=10.0,  # three missed heartbeats
        retry_after=1.0,
        backups=o.backups,
        keep_jobs=o.keep_jobs,
        max_task_failures=o.max_task_failures,
    )


def grpc_address(addr: str) -> str:
    """Turns a Go listen address such as ":7070", every interface, into
    gRPC's "[::]:7070"; other addresses are unchanged."""
    return "[::]" + addr if addr.startswith(":") else addr


def http_address(addr: str) -> tuple[str, int]:
    """Splits a Go listen address such as ":8080" or "127.0.0.1:0" into a
    host and a port; an empty host means every interface. Removes brackets
    around an IPv6 host. Raises ValueError if the port is missing or cannot
    be parsed as an integer; port ranges are not checked here."""
    host, _, port = addr.rpartition(":")
    return host.strip("[]"), int(port)


# slog's names of the levels Python has.
_LEVELS = {"WARNING": "WARN", "CRITICAL": "ERROR"}


def _slog(record: logging.LogRecord) -> dict[str, str]:
    """Returns record's time, level and message as slog writes them: the
    time in RFC 3339 with milliseconds and the zone's offset, and the
    level with WARNING mapped to WARN and CRITICAL to ERROR. Other level
    names are unchanged."""
    at = datetime.fromtimestamp(record.created).astimezone().isoformat(timespec="milliseconds")
    level = _LEVELS.get(record.levelname, record.levelname)
    return {"time": at.replace("+00:00", "Z"), "level": level, "msg": record.getMessage()}


def _logfmt(v: str) -> str:
    """Quotes v as slog's text handler does, if it is empty or has a space,
    '=', '"' or a character that does not print."""
    if v and not any(c in '="' or c.isspace() or not c.isprintable() for c in v):
        return v
    return json.dumps(v, ensure_ascii=False)


class _Text(logging.Formatter):
    """Writes each record as one line of key=value pairs, as slog's text handler."""

    def format(self, record: logging.LogRecord) -> str:
        """Returns the record's time, level and message as key=value text."""
        return " ".join(f"{k}={_logfmt(v)}" for k, v in _slog(record).items())


class _JSON(logging.Formatter):
    """Writes each record as one JSON object, as slog's JSON handler."""

    def format(self, record: logging.LogRecord) -> str:
        """Returns the record's time, level and message as a JSON object."""
        return json.dumps(_slog(record))


def setup_logs(fmt: str, stream=None):
    """Replaces the package logger's handlers and sets its level to INFO.
    Sends logs to stream (stderr by default), using JSON when fmt is "json"
    and text otherwise."""
    h = logging.StreamHandler(stream or sys.stderr)
    h.setFormatter(_JSON() if fmt == "json" else _Text())
    root = logging.getLogger("mapreduce_reloaded")
    root.handlers[:] = [h]
    root.setLevel(logging.INFO)


def lock(data_dir: str):
    """Takes the lock of data_dir, which its coordinator holds while it
    runs, and returns the open lock file; closing it releases the lock. A
    second coordinator on the directory, whatever its -addr, would truncate
    the log under the first and remove its compaction. Creates or overwrites
    coordinator.lock with this process's PID; data_dir must already exist.
    Raises OSError if another process holds it or file or lock operations
    fail."""
    f = open(os.path.join(data_dir, "coordinator.lock"), "a+")
    try:
        fcntl.flock(f, fcntl.LOCK_EX | fcntl.LOCK_NB)
    except BlockingIOError:
        f.seek(0)
        holder = f.read().strip() or "unknown"
        f.close()
        raise OSError(
            f"-data-dir {data_dir} is in use by another coordinator (pid {holder})"
        ) from None
    f.truncate(0)
    f.write(f"{os.getpid()}\n")
    f.flush()
    return f


def run(
    o: Options,
    stop: threading.Event,
    listening: Callable[[int, int | None], None] | None = None,
    grace: float = EXIT_GRACE,
):
    """Serves coordinator RPCs at o.addr until stop is set, then tells
    workers to exit. A nonempty o.data_dir enables log recovery and
    persistence; a nonempty o.http enables the status page and metrics.
    If listening is not None, run calls it with the RPC and HTTP ports (None
    without -http) once the enabled servers listen. grace is the delay in
    seconds after telling workers to exit and before stopping RPC serving.
    Propagates recovery, startup, listening callback and log close errors,
    including an error if another coordinator holds o.data_dir. The log is
    closed and the directory unlocked on every return, error or not."""
    cfg = config(o)
    coordinator: Server | None = None
    held = None
    if o.data_dir:
        os.makedirs(o.data_dir, exist_ok=True)
        held = lock(o.data_dir)
        try:
            coordinator, _ = recover(cfg, os.path.join(o.data_dir, "coordinator.log"))
        except BaseException:
            held.close()
            raise
    try:
        if coordinator is None:
            server, coordinator, port, monitor = serve(grpc_address(o.addr), cfg)
        else:
            server, port, monitor = start(coordinator, grpc_address(o.addr))
        status = None
        try:
            if o.http:
                status = statusweb.serve(http_address(o.http), coordinator.status)
            log.info("coordinator listening: port %d", port)
            if listening is not None:
                listening(port, status.server_address[1] if status else None)
            stop.wait()
        finally:
            if status is not None:
                status.shutdown()
                status.server_close()
            coordinator.shutdown()
            time.sleep(grace)  # for idle workers to hear that they should exit
            monitor.set()
            server.stop(grace=5).wait()
    finally:
        if coordinator is not None:  # None if serve could not listen
            coordinator.close()
        if held is not None:
            held.close()


def main(argv: list[str] | None = None) -> int:
    """Runs the coordinator until SIGINT or SIGTERM, using sys.argv[1:]
    when argv is None. Returns 0 on normal shutdown or 1 after printing an
    exception from run to stderr. Flag parsing instead raises SystemExit
    with status 2 for bad flags or 0 for help. Raises ValueError outside the
    main thread. Leaves logging and signal handlers installed."""
    o = parse(sys.argv[1:] if argv is None else argv)
    setup_logs(o.log_format)
    stop = threading.Event()
    for sig in (signal.SIGINT, signal.SIGTERM):
        signal.signal(sig, lambda *_: stop.set())
    try:
        run(o, stop)
    except Exception as e:
        print(f"coordinator: {e}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
