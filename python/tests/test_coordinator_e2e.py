"""The scenarios of go/internal/e2e/process_test.go, ported to pytest and run
against the Python coordinator (#36): python -m mapreduce_reloaded.coordinator
as a process, with the Go worker and mr commands, over localhost sockets.
The tests are skipped if the Go commands cannot be built."""

import json
import os
import random
import shutil
import signal
import socket
import subprocess
import sys
import time
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]

pytestmark = pytest.mark.skipif(not hasattr(signal, "SIGKILL"), reason="needs Unix signals")


@pytest.fixture(scope="module")
def bin_dir(tmp_path_factory):
    """Builds the Go worker and mr commands once, or skips."""
    if shutil.which("go") is None:
        pytest.skip("go is not installed")
    out = tmp_path_factory.mktemp("bin")
    build = subprocess.run(
        ["go", "build", "-o", str(out), "./cmd/worker", "./cmd/mr"],
        cwd=ROOT / "go",
        capture_output=True,
        text=True,
    )
    if build.returncode != 0:
        pytest.skip(f"cannot build the Go commands: {build.stderr}")
    return out


class Proc:
    """A running command whose output is kept for the test log."""

    def __init__(self, name, args, env=None, log_dir=None):
        self.name = name
        self.log = open(log_dir / f"{name.replace(' ', '-')}.log", "w+b")
        self.p = subprocess.Popen(
            args, stdout=self.log, stderr=subprocess.STDOUT, env={**os.environ, **(env or {})}
        )

    def stop(self, sig, timeout):
        """Sends sig and waits up to timeout seconds; returns the exit code."""
        self.p.send_signal(sig)
        try:
            return self.p.wait(timeout)
        except subprocess.TimeoutExpired:
            pytest.fail(f"{self.name} did not exit within {timeout}s of {sig!r}")

    def output(self):
        self.log.seek(0)
        return self.log.read().decode(errors="replace")


def free_port():
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return f"127.0.0.1:{s.getsockname()[1]}"


class Cluster:
    """A Python coordinator and Go workers on free localhost ports."""

    def __init__(self, bin_dir, tmp, workers, wal, procs):
        self.bin, self.procs = bin_dir, procs
        self.addr, self.tmp = free_port(), tmp / "workers"
        self.tmp.mkdir()
        self.logs = tmp / "logs"
        self.logs.mkdir()
        self.data_dir = str(tmp / "wal") if wal else ""
        self.start_coordinator()
        self.workers = []
        for i in range(workers):
            port = free_port()
            args = [str(bin_dir / "worker"), "-coordinator", self.addr, "-listen", port]
            args += ["-advertise", port, "-min-free-mib", "1"]
            self.workers.append(self._start(f"worker {i}", args, {"TMPDIR": str(self.tmp)}))

    def _start(self, name, args, env=None):
        p = Proc(name, args, env, self.logs)
        self.procs.append(p)
        return p

    def start_coordinator(self):
        args = [sys.executable, "-m", "mapreduce_reloaded.coordinator", "-addr", self.addr]
        args += ["-http", ""]
        if self.data_dir:
            args += ["-data-dir", self.data_dir]
        self.coord = self._start("coordinator", args)
        host, port = self.addr.rsplit(":", 1)
        deadline = time.monotonic() + 15
        while True:
            try:
                socket.create_connection((host, int(port)), timeout=1).close()
                return
            except OSError as e:
                if time.monotonic() > deadline or self.coord.p.poll() is not None:
                    pytest.fail(f"the coordinator does not listen on {self.addr}: {e}")
                time.sleep(0.02)

    def mr(self, *args):
        return mr(self.bin, *args)

    def submit(self, *args):
        return self.mr("submit", "-coordinator", self.addr, *args).strip()

    def status(self, job):
        """Returns the job's status as name/value pairs, or None if the
        coordinator cannot be reached."""
        r = subprocess.run(
            [str(self.bin / "mr"), "status", "-coordinator", self.addr, job],
            capture_output=True,
            text=True,
        )
        if r.returncode != 0:
            return None
        st = dict(line.split("\t", 1) for line in r.stdout.splitlines() if "\t" in line)
        st["state"] = st.get(job, "")
        return st

    def wait(self, job, timeout, stop=None):
        """Polls the job until it ends or stop returns true, and returns its
        last status."""
        deadline = time.monotonic() + timeout
        while True:
            st = self.status(job)
            if st is not None and (
                st["state"] in ("SUCCEEDED", "FAILED") or (stop is not None and stop(st))
            ):
                return st
            if time.monotonic() > deadline:
                pytest.fail(f"{job} still {st} after {timeout}s")
            time.sleep(0.02)


def mr(bin_dir, *args):
    r = subprocess.run([str(bin_dir / "mr"), *args], capture_output=True, text=True)
    if r.returncode != 0:
        pytest.fail(f"mr {' '.join(args)}: exit {r.returncode}\n{r.stdout}{r.stderr}")
    return r.stdout


@pytest.fixture
def cluster(bin_dir, tmp_path):
    """Returns a function that starts a cluster; every process is killed
    when the test ends. Its output is printed, which pytest shows only for
    a failed test."""
    procs = []
    yield lambda workers, wal=False: Cluster(bin_dir, tmp_path, workers, wal, procs)
    for p in procs:
        if p.p.poll() is None:
            p.p.kill()
            p.p.wait()
        print(f"--- {p.name} output:\n{p.output()}")
        p.log.close()


def parts(d):
    return {p.name: p.read_bytes() for p in Path(d).iterdir()}


def big_input(tmp_path):
    """Writes a text of words that keeps a 3-worker job busy for a second or
    two, and returns its path."""
    rng = random.Random(12)
    lines, size = [], 0
    while size < 48 << 20:
        line = " ".join(f"w{int(rng.expovariate(1) * 500)}" for _ in range(12)) + " \n"
        lines.append(line)
        size += len(line)
    path = tmp_path / "words.txt"
    path.write_text("".join(lines))
    return str(path)


def reference(bin_dir, tmp_path, input):
    out = tmp_path / "reference"
    mr(bin_dir, "run", "-local", "-m", "12", "-r", "4", "wordcount", str(out), input)
    return parts(out)


def test_word_count(cluster, tmp_path):
    """Word count over the test corpus on the Python coordinator and three
    Go worker processes gives the golden output; then SIGTERM stops every
    process, cleanly, and the workers remove their work directories."""
    c = cluster(3)
    out = tmp_path / "out"
    report = c.mr(
        "submit", "-coordinator", c.addr, "-m", "5", "-r", "4", "-wait", "wordcount", str(out),
        str(ROOT / "testdata/corpus") + "/*.txt",
    )  # fmt: skip
    assert "SUCCEEDED" in report
    assert parts(out) == parts(ROOT / "testdata/corpus/expected")
    for w in c.workers:
        assert w.stop(signal.SIGTERM, 1) == 0, w.name
    # The coordinator waits a second on purpose, for idle workers to hear
    # that they should exit, before it stops serving.
    assert c.coord.stop(signal.SIGTERM, 3) == 0
    assert list(c.tmp.glob("mr-worker-*")) == []


def test_examples(cluster, tmp_path):
    """Every example program gives the expected output of testdata/examples/."""
    jobs_file = ROOT / "testdata/examples/jobs.json"
    if not jobs_file.exists():
        pytest.skip("no testdata/examples/jobs.json")
    jobs = json.loads(jobs_file.read_text())
    c = cluster(3)
    for program in sorted(jobs):
        job = jobs[program]
        out = tmp_path / program
        args = ["-m", "3", "-r", "4"]
        params = job.get("params") or {}
        for k in sorted(params):
            args += ["-param", f"{k}={params[k]}"]
        args += [program, str(out), *(str(ROOT / i) for i in job["inputs"])]
        st = c.wait(c.submit(*args), 30)
        assert st["state"] == "SUCCEEDED", (program, st)
        assert parts(out) == parts(ROOT / "testdata/examples" / program / "expected/r4"), program


def test_worker_killed(cluster, bin_dir, tmp_path):
    """A worker killed with SIGKILL once the job has finished map tasks does
    not change the output: the coordinator reruns what it lost (§3.3)."""
    input = big_input(tmp_path)
    want = reference(bin_dir, tmp_path, input)
    c = cluster(3)
    out = tmp_path / "out"
    job = c.submit("-m", "12", "-r", "4", "wordcount", str(out), input)
    st = c.wait(job, 30, lambda st: not st.get("map", "0/").startswith("0/"))
    if st["state"] == "SUCCEEDED":
        pytest.skip("the job finished before a worker could be killed")
    c.workers[0].stop(signal.SIGKILL, 5)
    st = c.wait(job, 45)
    assert st["state"] == "SUCCEEDED", st
    assert parts(out) == want


def test_coordinator_restart(cluster, bin_dir, tmp_path):
    """A coordinator killed mid-job and restarted with the same -data-dir
    recovers the job from its log; the workers register again and it
    finishes with the right output (§3.3, ADR 0007)."""
    input = big_input(tmp_path)
    want = reference(bin_dir, tmp_path, input)
    c = cluster(3, wal=True)
    out = tmp_path / "out"
    job = c.submit("-m", "12", "-r", "4", "wordcount", str(out), input)
    st = c.wait(job, 30, lambda st: not st.get("map", "0/").startswith("0/"))
    if st["state"] == "SUCCEEDED":
        pytest.skip("the job finished before the coordinator could be killed")
    c.coord.stop(signal.SIGKILL, 5)
    c.start_coordinator()
    st = c.wait(job, 45)
    assert st["state"] == "SUCCEEDED", st
    assert parts(out) == want
