"""The scenarios of go/internal/e2e/process_test.go, ported to pytest and run
against the Python coordinator (#36): python -m mapreduce_reloaded.coordinator
as a process, with the Go worker and mr commands, over localhost sockets.
The tests are skipped if the Go commands cannot be built, or fail if
MR_REQUIRE_GO is set, as make py-e2e sets it for CI's go job."""

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


def must_run():
    """Returns pytest.skip, or pytest.fail when MR_REQUIRE_GO is set, so a
    scenario that cannot run fails CI's go job instead of passing it."""
    return pytest.fail if os.environ.get("MR_REQUIRE_GO") else pytest.skip


@pytest.fixture(scope="module")
def bin_dir(tmp_path_factory):
    """Builds the Go worker and mr commands once, or skips, or fails with
    MR_REQUIRE_GO."""
    give_up = must_run()
    if shutil.which("go") is None:
        give_up("go is not installed")
    out = tmp_path_factory.mktemp("bin")
    build = subprocess.run(
        ["go", "build", "-o", str(out), "./cmd/worker", "./cmd/mr"],
        cwd=ROOT / "go",
        capture_output=True,
        text=True,
    )
    if build.returncode != 0:
        give_up(f"cannot build the Go commands: {build.stderr}")
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
        coordinator cannot be reached or does not answer within 5s, which
        leaves the deadline to wait."""
        try:
            r = subprocess.run(
                [str(self.bin / "mr"), "status", "-coordinator", self.addr, job],
                capture_output=True,
                text=True,
                timeout=5,
            )
        except subprocess.TimeoutExpired:
            return None
        if r.returncode != 0:
            return None
        st = dict(line.split("\t", 1) for line in r.stdout.splitlines() if "\t" in line)
        st["state"] = st.get(job, "")
        return st

    def signal_workers(self, sig):
        for w in self.workers:
            if w.p.poll() is None:
                w.p.send_signal(sig)

    def mid_map(self, tmp_path, input, tries=3):
        """Submits a word count over input and returns (job, output
        directory) with every worker stopped (SIGSTOP) once at least one map
        task is done and the job is not: the test interrupts it with nothing
        moving, then sends SIGCONT. A job can only finish between a poll and
        the SIGSTOP, 20ms; then it submits a new one, up to tries times."""
        for n in range(tries):
            out = tmp_path / f"out{n}"
            job = self.submit("-m", "12", "-r", "4", "wordcount", str(out), input)
            self.wait(job, 30, lambda st: not st.get("map", "0/").startswith("0/"))
            self.signal_workers(signal.SIGSTOP)
            st = self.status(job)
            if st is not None and st["state"] not in ("SUCCEEDED", "FAILED"):
                return job, out
            self.signal_workers(signal.SIGCONT)
        must_run()(f"{tries} jobs finished before they could be interrupted")

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
    try:
        r = subprocess.run([str(bin_dir / "mr"), *args], capture_output=True, text=True, timeout=60)
    except subprocess.TimeoutExpired:
        pytest.fail(f"mr {' '.join(args)} did not exit within 60s")
    if r.returncode != 0:
        pytest.fail(f"mr {' '.join(args)}: exit {r.returncode}\n{r.stdout}{r.stderr}")
    return r.stdout


@pytest.fixture
def cluster(bin_dir, tmp_path):
    """Returns a function that starts a cluster; every process is killed
    when the test ends. Each process's output is kept in the test's
    logs/ directory, which pytest keeps for its last three runs, and
    printed at teardown, which pytest shows with -rP or -s."""
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
    counters = dict(line.split("\t", 1) for line in report.splitlines() if "\t" in line)
    assert counters["map.output.records"] == "78658", counters
    assert counters["reduce.output.records"] == "16851", counters
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
    job, out = c.mid_map(tmp_path, input)
    c.workers[0].stop(signal.SIGKILL, 5)
    c.signal_workers(signal.SIGCONT)
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
    job, out = c.mid_map(tmp_path, input)
    c.coord.stop(signal.SIGKILL, 5)
    c.start_coordinator()
    c.signal_workers(signal.SIGCONT)
    st = c.wait(job, 45)
    assert st["state"] == "SUCCEEDED", st
    assert parts(out) == want


SLOW_JOB = """package job

import (
	"time"

	"mapreduce/api"
)

// Map sleeps in map task 0, so the coordinator sees a straggler.
func Map(ctx api.Context, key, value []byte, out api.Emitter) error {
	if ctx.Task().Index == 0 {
		time.Sleep(3 * time.Second)
	}
	for _, w := range api.Words(value) {
		if err := out.Emit(w, []byte("1")); err != nil {
			return err
		}
	}
	return nil
}

var Reduce = api.Sum
var Combine = api.Sum
"""

BAD_JOB = """package job

import "mapreduce/api"

// Map panics on the first record of map task 0, which the coordinator must
// skip after its retries (§4.6).
func Map(ctx api.Context, key, value []byte, out api.Emitter) error {
	if ctx.Task().Index == 0 && string(key) == "0" {
		panic("bad record")
	}
	for _, w := range api.Words(value) {
		if err := out.Emit(w, []byte("1")); err != nil {
			return err
		}
	}
	return nil
}

var Reduce = api.Sum
var Combine = api.Sum
"""


def test_backups_finish_a_straggler(cluster, tmp_path):
    """A map task that sleeps is finished by a backup attempt on another
    worker, and the backup wins (§3.6)."""
    job_file = tmp_path / "slow.go"
    job_file.write_text(SLOW_JOB)
    c = cluster(3)
    out = tmp_path / "out"
    report = c.mr(
        "submit", "-coordinator", c.addr, "-m", "4", "-r", "2", "-wait",
        str(job_file), str(out), str(ROOT / "testdata/corpus") + "/*.txt",
    )  # fmt: skip
    status = dict(line.split("\t", 1) for line in report.splitlines() if "\t" in line)
    assert "SUCCEEDED" in report
    assert int(status["backups"]) >= 1, status
    assert int(status["backups_won"]) >= 1, status
    assert parts(out) == parts(ROOT / "testdata/corpus/expected")


def test_bad_records_are_skipped(cluster, tmp_path):
    """A map task that panics on one record is retried, then that record is
    skipped and the job succeeds (§4.6)."""
    job_file = tmp_path / "bad.go"
    job_file.write_text(BAD_JOB)
    c = cluster(3)
    out = tmp_path / "out"
    report = c.mr(
        "submit", "-coordinator", c.addr, "-m", "4", "-r", "2", "-wait",
        str(job_file), str(out), str(ROOT / "testdata/corpus") + "/*.txt",
    )  # fmt: skip
    status = dict(line.split("\t", 1) for line in report.splitlines() if "\t" in line)
    assert "SUCCEEDED" in report
    assert status["skipped.records"] == "1", status
