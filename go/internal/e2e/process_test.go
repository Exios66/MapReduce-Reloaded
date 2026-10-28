//go:build unix

// Package e2e runs the built coordinator, worker and mr commands as separate
// processes on localhost sockets (#151): the flags, the real network between
// processes, the shuffle between them, and what a killed or terminated
// process leaves behind. The cluster tests in internal/worker run the same
// code as goroutines in one process; make e2e runs it under Docker.
package e2e

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"math/rand/v2"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

const repo = "../../.."

var (
	buildOnce sync.Once
	binDir    string
	buildErr  error
)

// coverDir, from MR_E2E_GOCOVERDIR, is where the commands write their
// coverage counters if set (make go-test), so that what only a process
// runs, such as main, counts toward each package's coverage.
var coverDir = os.Getenv("MR_E2E_GOCOVERDIR")

// bin returns the path of a built command, building all three once, or
// skips the test if they do not build. With coverDir they are built to
// count coverage of every package in the module.
func bin(t *testing.T, name string) string {
	t.Helper()
	buildOnce.Do(func() {
		if binDir, buildErr = os.MkdirTemp("", "mr-e2e-bin-"); buildErr != nil {
			return
		}
		args := []string{"build", "-o", binDir}
		if coverDir != "" {
			args = append(args, "-cover", "-covermode=atomic", "-coverpkg=github.com/exios66/mapreduce-reloaded/go/...")
		}
		args = append(args, "../../cmd/coordinator", "../../cmd/worker", "../../cmd/mr")
		out, err := exec.Command("go", args...).CombinedOutput()
		if err != nil {
			buildErr = fmt.Errorf("%v\n%s", err, out)
		}
	})
	if buildErr != nil {
		t.Skipf("cannot build the commands: %v", buildErr)
	}
	return filepath.Join(binDir, name)
}

// command returns a command that runs the built command name, writing its
// coverage counters to coverDir if set.
func command(t *testing.T, name string, args ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(bin(t, name), args...)
	cmd.Env = os.Environ()
	if coverDir != "" {
		cmd.Env = append(cmd.Env, "GOCOVERDIR="+coverDir)
	}
	return cmd
}

func TestMain(m *testing.M) {
	code := m.Run()
	if binDir != "" {
		_ = os.RemoveAll(binDir)
	}
	os.Exit(code)
}

// proc is a running command whose output is kept for the test log.
type proc struct {
	name string
	cmd  *exec.Cmd
	out  bytes.Buffer
	done chan struct{}
	err  error
}

func start(t *testing.T, name string, env []string, args ...string) *proc {
	t.Helper()
	p := &proc{name: name, cmd: command(t, strings.Fields(name)[0], args...), done: make(chan struct{})}
	p.cmd.Env = append(p.cmd.Env, env...)
	p.cmd.Stdout, p.cmd.Stderr = &p.out, &p.out
	if err := p.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { p.err = p.cmd.Wait(); close(p.done) }()
	t.Cleanup(func() {
		_ = p.cmd.Process.Kill()
		<-p.done
		if t.Failed() {
			t.Logf("%s output:\n%s", p.name, p.out.String())
		}
	})
	return p
}

// stop sends sig and waits up to timeout for the process to exit.
func (p *proc) stop(t *testing.T, sig syscall.Signal, timeout time.Duration) error {
	t.Helper()
	_ = p.cmd.Process.Signal(sig)
	select {
	case <-p.done:
		return p.err
	case <-time.After(timeout):
		t.Fatalf("%s did not exit within %v of %v", p.name, timeout, sig)
		return nil
	}
}

// cluster is a coordinator and workers on free localhost ports.
type cluster struct {
	t       *testing.T
	addr    string
	dataDir string // the coordinator's -data-dir, or ""
	coord   *proc
	workers []*proc
	tmp     string // the workers' TMPDIR, where they make their work directories
}

func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().String()
}

func newCluster(t *testing.T, workers int, wal bool) *cluster {
	t.Helper()
	c := &cluster{t: t, addr: freePort(t), tmp: t.TempDir()}
	if wal {
		c.dataDir = t.TempDir()
	}
	c.startCoordinator()
	for i := range workers {
		port := freePort(t)
		c.workers = append(c.workers, start(t, fmt.Sprintf("worker %d", i), []string{"TMPDIR=" + c.tmp},
			"-coordinator", c.addr, "-listen", port, "-advertise", port, "-min-free-mib", "1"))
	}
	return c
}

func (c *cluster) startCoordinator() {
	args := []string{"-addr", c.addr, "-http", ""}
	if c.dataDir != "" {
		args = append(args, "-data-dir", c.dataDir)
	}
	c.coord = start(c.t, "coordinator", nil, args...)
	deadline := time.Now().Add(10 * time.Second)
	for {
		conn, err := net.Dial("tcp", c.addr)
		if err == nil {
			_ = conn.Close()
			return
		}
		if time.Now().After(deadline) {
			c.t.Fatalf("the coordinator does not listen on %s: %v", c.addr, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// mr runs the mr command and returns its standard output.
func mr(t *testing.T, args ...string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	cmd := command(t, "mr", args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("mr %s: %v\n%s%s", strings.Join(args, " "), err, stdout.String(), stderr.String())
	}
	return stdout.String()
}

// submit submits a job without waiting and returns its ID.
func (c *cluster) submit(args ...string) string {
	return strings.TrimSpace(mr(c.t, append([]string{"submit", "-coordinator", c.addr}, args...)...))
}

// status returns the job's status as name/value pairs, or nil if the
// coordinator cannot be reached.
func (c *cluster) status(job string) map[string]string {
	out, err := command(c.t, "mr", "status", "-coordinator", c.addr, job).Output()
	if err != nil {
		return nil
	}
	st := make(map[string]string)
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		if k, v, ok := strings.Cut(sc.Text(), "\t"); ok {
			st[k] = v
		}
	}
	st["state"] = st[job]
	return st
}

// wait polls the job until it ends or until stop returns true, and returns
// its last status.
func (c *cluster) wait(job string, timeout time.Duration, stop func(map[string]string) bool) map[string]string {
	deadline := time.Now().Add(timeout)
	for {
		st := c.status(job)
		if st != nil && (st["state"] == "SUCCEEDED" || st["state"] == "FAILED" || stop != nil && stop(st)) {
			return st
		}
		if time.Now().After(deadline) {
			c.t.Fatalf("%s still %v after %v", job, st, timeout)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func parts(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	files := make(map[string][]byte)
	for _, e := range entries {
		if files[e.Name()], err = os.ReadFile(filepath.Join(dir, e.Name())); err != nil {
			t.Fatal(err)
		}
	}
	return files
}

// sameParts fails unless got holds exactly want's part files, with the same
// bytes, and nothing else.
func sameParts(t *testing.T, got, want map[string][]byte) {
	t.Helper()
	if g, w := slices.Sorted(maps.Keys(got)), slices.Sorted(maps.Keys(want)); !slices.Equal(g, w) {
		t.Fatalf("output files %v, want %v", g, w)
	}
	for name, data := range want {
		if !bytes.Equal(got[name], data) {
			t.Errorf("%s differs from the expected output", name)
		}
	}
}

func abs(t *testing.T, path string) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join(repo, path))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// bigInput writes a text of words that keeps a 3-worker job busy for a
// second or two, and returns its path.
func bigInput(t *testing.T) string {
	t.Helper()
	rng := rand.New(rand.NewPCG(1, 2))
	var b bytes.Buffer
	for b.Len() < 48<<20 {
		for range 12 {
			fmt.Fprintf(&b, "w%d ", int(rng.ExpFloat64()*500))
		}
		b.WriteByte('\n')
	}
	path := filepath.Join(t.TempDir(), "words.txt")
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// reference returns the output of mr run -local over input.
func reference(t *testing.T, input string) map[string][]byte {
	t.Helper()
	out := filepath.Join(t.TempDir(), "out")
	mr(t, "run", "-local", "-m", "12", "-r", "4", "wordcount", out, input)
	return parts(t, out)
}

// Word count over the test corpus on a coordinator and three worker
// processes gives the golden output; then SIGTERM stops every process
// within a second, cleanly, and the workers remove their work directories.
func TestWordCount(t *testing.T) {
	t.Parallel()
	c := newCluster(t, 3, false)
	out := filepath.Join(t.TempDir(), "out")
	report := mr(t, "submit", "-coordinator", c.addr, "-m", "5", "-r", "4", "-wait", "wordcount", out, abs(t, "testdata/corpus")+"/*.txt")
	if !strings.Contains(report, "SUCCEEDED") {
		t.Fatalf("submit -wait printed %q", report)
	}
	sameParts(t, parts(t, out), parts(t, abs(t, "testdata/corpus/expected")))

	for _, p := range c.workers {
		if err := p.stop(t, syscall.SIGTERM, time.Second); err != nil {
			t.Errorf("%s exited with %v after SIGTERM", p.name, err)
		}
	}
	// The coordinator waits a second on purpose, for idle workers to hear
	// that they should exit, before it stops serving.
	if err := c.coord.stop(t, syscall.SIGTERM, 2*time.Second); err != nil {
		t.Errorf("the coordinator exited with %v after SIGTERM", err)
	}
	if left, _ := filepath.Glob(filepath.Join(c.tmp, "mr-worker-*")); len(left) > 0 {
		t.Errorf("work directories left after SIGTERM: %v", left)
	}
}

// Every example program gives the expected output of testdata/examples/.
func TestExamples(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(abs(t, "testdata/examples/jobs.json"))
	if os.IsNotExist(err) {
		t.Skip("no testdata/examples/jobs.json")
	}
	if err != nil {
		t.Fatal(err)
	}
	var jobs map[string]struct {
		Inputs []string
		Params map[string]string
	}
	if err := json.Unmarshal(data, &jobs); err != nil {
		t.Fatal(err)
	}
	c := newCluster(t, 3, false)
	for _, program := range slices.Sorted(maps.Keys(jobs)) {
		job := jobs[program]
		out := filepath.Join(t.TempDir(), program)
		args := []string{"-m", "3", "-r", "4"}
		for _, k := range slices.Sorted(maps.Keys(job.Params)) {
			args = append(args, "-param", k+"="+job.Params[k])
		}
		args = append(args, program, out)
		for _, in := range job.Inputs {
			args = append(args, abs(t, in))
		}
		if st := c.wait(c.submit(args...), 30*time.Second, nil); st["state"] != "SUCCEEDED" {
			t.Fatalf("%s: %v", program, st)
		}
		sameParts(t, parts(t, out), parts(t, abs(t, filepath.Join("testdata/examples", program, "expected/r4"))))
	}
}

// A worker killed with SIGKILL once the job has finished map tasks does not
// change the output: the coordinator reruns what it lost (§3.3).
func TestWorkerKilled(t *testing.T) {
	t.Parallel()
	input := bigInput(t)
	want := reference(t, input)
	c := newCluster(t, 3, false)
	out := filepath.Join(t.TempDir(), "out")
	job := c.submit("-m", "12", "-r", "4", "wordcount", out, input)
	st := c.wait(job, 30*time.Second, func(st map[string]string) bool { return !strings.HasPrefix(st["map"], "0/") })
	if st["state"] == "SUCCEEDED" {
		t.Skip("the job finished before a worker could be killed")
	}
	_ = c.workers[0].stop(t, syscall.SIGKILL, 5*time.Second)
	if st = c.wait(job, 45*time.Second, nil); st["state"] != "SUCCEEDED" {
		t.Fatalf("after the kill: %v", st)
	}
	sameParts(t, parts(t, out), want)
}

// A coordinator killed mid-job and restarted with the same -data-dir
// recovers the job from its log; the workers register again and it
// finishes with the right output (§3.3, ADR 0007).
func TestCoordinatorRestart(t *testing.T) {
	t.Parallel()
	input := bigInput(t)
	want := reference(t, input)
	c := newCluster(t, 3, true)
	out := filepath.Join(t.TempDir(), "out")
	job := c.submit("-m", "12", "-r", "4", "wordcount", out, input)
	st := c.wait(job, 30*time.Second, func(st map[string]string) bool { return !strings.HasPrefix(st["map"], "0/") })
	if st["state"] == "SUCCEEDED" {
		t.Skip("the job finished before the coordinator could be killed")
	}
	_ = c.coord.stop(t, syscall.SIGKILL, 5*time.Second)
	c.startCoordinator()
	if st = c.wait(job, 45*time.Second, nil); st["state"] != "SUCCEEDED" {
		t.Fatalf("after the restart: %v", st)
	}
	sameParts(t, parts(t, out), want)
}
