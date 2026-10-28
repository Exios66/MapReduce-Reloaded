"""Restarts a coordinator from its write-ahead log (§3.3, ADR 0007),
mirroring go/internal/coordinator/recover.go."""

from dataclasses import replace

from mapreduce_reloaded.coordinator.registry import Registry
from mapreduce_reloaded.coordinator.server import Config, Server
from mapreduce_reloaded.gen.mapreduce.v1 import log_pb2
from mapreduce_reloaded.wal import Log


def recover(cfg: Config, path: str) -> tuple[Server, Log]:
    """Returns a server that resumes from the write-ahead log at path,
    creating it if needed, and the log. The caller closes the server, or the
    log if it never failed: a server whose log fails reopens it the same
    way, replacing it. It replays
    the decisions there, then logs a restart: the attempts that were running
    stop, and workers register again under IDs the old coordinator never
    gave out. The server logs to path from then on. Logged submission and
    finish times are kept; missing ones stay unknown. Raises the log's
    errors, a protobuf DecodeError or replay's ValueError for a bad record,
    and RpcError with Unavailable if recording the restart fails."""
    s = Server(replace(cfg, id_prefix=""))
    restarts = 0

    def replay(b: bytes):
        nonlocal restarts
        rec = log_pb2.LogRecord.FromString(b)
        if rec.HasField("restarted"):
            restarts += 1
        s._replay(rec)

    log = Log.open(path, replay)
    try:
        # Clean up the ended jobs only now, when the log says which job runs:
        # a later job may write to the same output, and needs its TempDir.
        for j in s._replay_ended:
            if s._active is None or s._active.spec.output != j.spec.output:
                j.cleanup()
        s._replay_ended = []
        s._cfg.log = log
        s._compact()  # replay may have forgotten jobs while the log was not set
        rec = log_pb2.LogRecord(restarted=log_pb2.Restarted())
        s._record(rec, s.now())
        s._replay(rec)
    except BaseException:
        log.close()
        raise
    s._workers = Registry(f"r{restarts + 1}-w")
    s._log_path = path
    return s, log
