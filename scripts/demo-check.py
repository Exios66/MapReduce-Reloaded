#!/usr/bin/env python3
"""Check the running Compose cluster against what docs/demo.md needs.

make demo-check runs it on the VM after make cluster-up mr and
make upload-example. One line per check: OK, WARN (the demo still runs) or
FAIL (fix it first, the detail says how). Exits 1 on any FAIL.
scripts/vm-setup.sh --check checks the machine; this checks the cluster.
Standard library only, so the system python3 runs it.
"""

import argparse
import http.client
import json
import shutil
import subprocess
import sys
import urllib.error
import urllib.request
from collections.abc import Callable
from pathlib import Path
from typing import NamedTuple

ROOT = Path(__file__).resolve().parents[1]
STATUS_URL = "http://127.0.0.1:18780/"
COMPOSE = ["compose", "-p", "mapreduce", "-f", "deploy/compose/compose.yaml"]

# deploy/compose/compose.yaml and docs/cluster-vm.md; a test keeps them equal.
WORKERS = 4
WORKER_NANO_CPUS = 1_000_000_000
WORKER_MEMORY = 768 * 1024 * 1024
COORDINATOR_MEMORY = 256 * 1024 * 1024
HEARTBEAT_LIMIT = 15  # seconds; the coordinator's timeout is 10
FREE_BYTES = 6_000_000_000  # docs/demo.md: at least 6 GB free
SORT_INPUT = "data/input/sort-1g.txt"
SORT_BYTES = 1_073_741_824
SORT_FILE_BYTES = -(-SORT_BYTES // 100) * 100  # gen-sort.py writes whole 100-byte records


class Check(NamedTuple):
    level: str  # OK, WARN or FAIL
    name: str
    detail: str


def docker(args: list[str]) -> str:
    try:
        out = subprocess.run(
            ["docker", *args], cwd=ROOT, capture_output=True, text=True, timeout=20
        )
    except (OSError, subprocess.TimeoutExpired) as e:
        raise RuntimeError(str(e)) from e
    if out.returncode != 0:
        # The cause comes first; Docker may add a usage hint after it.
        lines = [s for s in (out.stderr or out.stdout).splitlines() if s.strip()]
        raise RuntimeError(lines[0].strip() if lines else f"exit {out.returncode}")
    return out.stdout


def fetch(url: str) -> bytes:
    with urllib.request.urlopen(url, timeout=5) as r:
        return r.read()


def free_bytes(path: Path) -> int:
    return shutil.disk_usage(path).free


def _ps_rows(text: str) -> list[dict]:
    """docker compose ps --format json: one object per line, or one array."""
    text = text.strip()
    if text.startswith("["):
        return json.loads(text)
    return [json.loads(line) for line in text.splitlines() if line.strip()]


def _cluster(docker: Callable[[list[str]], str]) -> list[Check]:
    names = ["workers", "coordinator", "limits", "wal"]
    try:
        version = docker(["version", "--format", "{{.Server.Version}}"]).strip()
    except RuntimeError as e:
        first = Check("FAIL", "docker", f"docker unavailable: {e}")
        return [first] + [Check("FAIL", n, "needs docker") for n in names]
    out = [Check("OK", "docker", f"server {version}")]
    try:
        rows = _ps_rows(docker([*COMPOSE, "ps", "--all", "--format", "json"]))
    except (RuntimeError, ValueError) as e:
        detail = f"docker compose ps: {e}"
        return out + [Check("FAIL", n, detail) for n in names]
    running = [r for r in rows if r.get("State") == "running"]
    workers = sorted(r["Name"] for r in running if r.get("Service") == "worker")
    coordinators = [r["Name"] for r in running if r.get("Service") == "coordinator"]
    level = "OK" if len(workers) == WORKERS else "FAIL"
    out.append(Check(level, "workers", f"{len(workers)} of {WORKERS} running"))
    if not coordinators:
        out.append(Check("FAIL", "coordinator", "not running: make cluster-up"))
    else:
        out.append(Check("OK", "coordinator", coordinators[0]))
    if not running:
        return out + [
            Check("FAIL", "limits", "nothing running"),
            Check("FAIL", "wal", "nothing running"),
        ]
    try:
        inspected = json.loads(docker(["inspect", *coordinators, *workers]))
    except (RuntimeError, ValueError) as e:
        return out + [
            Check("FAIL", "limits", f"docker inspect: {e}"),
            Check("FAIL", "wal", f"docker inspect: {e}"),
        ]
    wrong, wal = [], None
    returned = {(c.get("Name") or "").lstrip("/") for c in inspected}
    wrong += [f"{n} not inspected" for n in [*coordinators, *workers] if n not in returned]
    for c in inspected:
        name, host = (c.get("Name") or "?").lstrip("/"), c.get("HostConfig") or {}
        if name in coordinators:
            wal = _data_dir((c.get("Config") or {}).get("Cmd") or [])
            if host.get("Memory") != COORDINATOR_MEMORY:
                wrong.append(f"{name} memory {host.get('Memory')}")
        elif host.get("NanoCpus") != WORKER_NANO_CPUS or host.get("Memory") != WORKER_MEMORY:
            wrong.append(f"{name} cpus {host.get('NanoCpus')} memory {host.get('Memory')}")
    if wrong:
        out.append(
            Check("FAIL", "limits", "; ".join(wrong) + " (want deploy/compose/compose.yaml's)")
        )
    else:
        out.append(Check("OK", "limits", "workers 1 CPU and 768 MiB, coordinator 256 MiB"))
    if not coordinators:
        out.append(Check("FAIL", "wal", "coordinator not running"))
    elif wal:
        out.append(Check("OK", "wal", "coordinator runs with -data-dir"))
    else:
        out.append(
            Check(
                "FAIL", "wal", "coordinator has no -data-dir: the restart step would lose the job"
            )
        )
    return out


def _data_dir(cmd: list[str]) -> str:
    """The coordinator's -data-dir value; empty means no write-ahead log."""
    for i, arg in enumerate(cmd):
        if arg in ("-data-dir", "--data-dir"):
            return cmd[i + 1] if i + 1 < len(cmd) else ""
        if arg.startswith(("-data-dir=", "--data-dir=")):
            return arg.split("=", 1)[1]
    return ""


def _status(fetch: Callable[[str], bytes], url: str) -> list[Check]:
    try:
        st = json.loads(fetch(url.rstrip("/") + "/status.json"))
    except urllib.error.HTTPError as e:
        detail = f"{url} answered HTTP {e.code}: docker logs mapreduce-coordinator-1 says why"
        return [Check("FAIL", "status", detail), Check("FAIL", "heartbeat", "no status page")]
    except (OSError, ValueError, http.client.HTTPException) as e:
        detail = f"{url} unreachable ({e}): make cluster-up, or start the SSH forward"
        return [Check("FAIL", "status", detail), Check("FAIL", "heartbeat", "no status page")]
    if not isinstance(st, dict) or not isinstance(st.get("workers"), list):
        detail = f"{url.rstrip('/')}/status.json is not the coordinator's status"
        return [Check("FAIL", "status", detail), Check("FAIL", "heartbeat", "no status page")]
    workers = st["workers"]
    out = [
        Check(
            "OK" if len(workers) == WORKERS else "FAIL",
            "status",
            f"{len(workers)} of {WORKERS} workers registered",
        )
    ]
    at = st.get("at")
    if isinstance(at, bool) or not isinstance(at, (int, float)):
        return out + [Check("FAIL", "heartbeat", "status.json gives no time to compare with")]
    stale = [w.get("id", "?") for w in workers if at - (w.get("last_seen") or 0) > HEARTBEAT_LIMIT]
    if stale:
        out.append(
            Check(
                "FAIL",
                "heartbeat",
                f"no heartbeat for over {HEARTBEAT_LIMIT} s: {', '.join(stale)}",
            )
        )
    elif not workers:
        out.append(Check("FAIL", "heartbeat", "no workers"))
    else:
        out.append(Check("OK", "heartbeat", f"every worker seen in the last {HEARTBEAT_LIMIT} s"))
    return out


def _disk(paths: list[Path], free_bytes: Callable[[Path], int], unmeasured: list[str]) -> Check:
    """Free space where the inputs live and where Docker keeps the job output."""
    seen, low, unmeasured = [], False, list(unmeasured)
    for p in paths:
        try:
            free = free_bytes(p)
        except OSError as e:
            unmeasured.append(f"could not measure {p}: {e}")
            continue
        low |= free < FREE_BYTES
        seen.append(f"{free / 1e9:.1f} GB free at {p}")
    need = f"(the demo needs {FREE_BYTES / 1e9:.0f} GB)"
    level = "FAIL" if low else "WARN" if unmeasured else "OK"
    return Check(level, "disk", "; ".join([*seen, *unmeasured]) + f" {need}")


def _host(
    root: Path, free_bytes: Callable[[Path], int], docker_root: Path | None, docker_known: bool
) -> list[Check]:
    paths = [root] if docker_root is None else [root, docker_root]
    out = [_disk(paths, free_bytes, [] if docker_known else ["Docker's data directory unknown"])]
    mr = root / "bin/mr"
    if mr.is_file() and mr.stat().st_mode & 0o111:
        out.append(Check("OK", "client", "bin/mr"))
    else:
        out.append(Check("FAIL", "client", "bin/mr missing: make mr"))
    missing = []
    if not list((root / "data/input/corpus").glob("*.txt")):
        missing.append("data/input/corpus/*.txt")
    if not (root / "data/input/weblog/access.log").is_file():
        missing.append("data/input/weblog/access.log")
    if missing:
        out.append(Check("FAIL", "inputs", f"missing {', '.join(missing)}: make upload-example"))
    else:
        out.append(Check("OK", "inputs", "corpus and web log uploaded"))
    sort = root / SORT_INPUT
    gen = f"python3 bench/gen-sort.py {SORT_BYTES} 42 > {SORT_INPUT}"
    if sort.is_file() and sort.stat().st_size == SORT_FILE_BYTES:
        out.append(Check("OK", "sort", SORT_INPUT))
    else:
        out.append(
            Check(
                "WARN",
                "sort",
                f"{SORT_INPUT} missing or wrong size; the demo spends 20 s on it, or run: {gen}",
            )
        )
    return out


def run_checks(
    docker: Callable[[list[str]], str],
    fetch: Callable[[str], bytes],
    root: Path,
    free_bytes: Callable[[Path], int],
    status_url: str,
) -> list[Check]:
    out = []

    def section(run, names):
        try:
            out.extend(run())
        except Exception as e:  # one surprise must not hide the other checks
            out.extend(Check("FAIL", n, f"check crashed: {e!r}") for n in names)

    section(lambda: _cluster(docker), ["docker", "workers", "coordinator", "limits", "wal"])
    section(lambda: _status(fetch, status_url), ["status", "heartbeat"])
    docker_root, known = None, True  # Docker down: its own check already fails
    if out and out[0].level == "OK":  # Docker answered: its volumes hold the job output
        try:
            found = docker(["info", "--format", "{{.DockerRootDir}}"]).strip()
        except RuntimeError:
            found = ""
        docker_root, known = (Path(found), True) if found else (None, False)
    section(
        lambda: _host(root, free_bytes, docker_root, known), ["disk", "client", "inputs", "sort"]
    )
    return out


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--status-url", default=STATUS_URL)
    args = parser.parse_args(argv)
    checks = run_checks(docker, fetch, ROOT, free_bytes, args.status_url)
    for c in checks:
        print(f"{c.level:<4} {c.name:<11} {c.detail}")
    return 1 if any(c.level == "FAIL" for c in checks) else 0


if __name__ == "__main__":
    sys.exit(main())
