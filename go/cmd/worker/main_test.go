package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"

	pb "github.com/exios66/mapreduce-reloaded/go/gen/mapreduce/v1"
	"github.com/exios66/mapreduce-reloaded/go/internal/coordinator"
)

func TestParseDefaults(t *testing.T) {
	c, err := parse(nil, io.Discard)
	want := config{coordinator: "localhost:7070", listen: ":7071", logFormat: "text", minFreeMiB: 1024}
	if err != nil || c != want {
		t.Fatalf("parse() = %+v, %v; want %+v", c, err, want)
	}
}

// Bad flags exit with status 2 and say why; a failure to run exits with 1.
func TestStartErrors(t *testing.T) {
	for _, tc := range []struct {
		args []string
		code int
		msg  string
	}{
		{[]string{"-nope"}, 2, "flag provided but not defined: -nope"},
		{[]string{"extra"}, 2, `unexpected arguments ["extra"]`},
		{[]string{"-listen", ""}, 2, `-listen "" needs a shared -work-dir`},
		{[]string{"-min-free-mib", "18446744073709551615"}, 2, "is too large"},
		{[]string{"-log-format", "xml"}, 1, `log format "xml"`},
		{[]string{"-listen", "nowhere:-1"}, 1, "worker: listen tcp"},
		{[]string{"-h"}, 0, "Usage of worker"},
	} {
		var stderr bytes.Buffer
		if code := start(t.Context(), tc.args, &stderr); code != tc.code || !strings.Contains(stderr.String(), tc.msg) {
			t.Errorf("start(%q) = %d, %q; want %d and %q", tc.args, code, stderr.String(), tc.code, tc.msg)
		}
	}
}

// recorder is a coordinator that records what workers tell it.
type recorder struct {
	*coordinator.Server
	mu        sync.Mutex
	registers []*pb.WorkerInfo
	outputs   []*pb.PartitionLocation
}

func (r *recorder) RegisterWorker(ctx context.Context, req *pb.RegisterWorkerRequest) (*pb.RegisterWorkerResponse, error) {
	r.mu.Lock()
	r.registers = append(r.registers, req.GetWorker())
	r.mu.Unlock()
	return r.Server.RegisterWorker(ctx, req)
}

func (r *recorder) ReportTask(ctx context.Context, req *pb.ReportTaskRequest) (*pb.ReportTaskResponse, error) {
	r.mu.Lock()
	r.outputs = append(r.outputs, req.GetSuccess().GetOutputs()...)
	r.mu.Unlock()
	return r.Server.ReportTask(ctx, req)
}

func (r *recorder) registered() []*pb.WorkerInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*pb.WorkerInfo(nil), r.registers...)
}

// coordinatorAt serves a new recording coordinator at addr ("" for any
// port), and returns it, its address and a function that stops it.
func coordinatorAt(t *testing.T, addr string) (*recorder, string, func()) {
	t.Helper()
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	r := &recorder{Server: coordinator.NewServer(coordinator.Config{
		HeartbeatInterval: 10 * time.Millisecond, WorkerTimeout: time.Second, RetryAfter: 10 * time.Millisecond,
	})}
	gs := grpc.NewServer()
	pb.RegisterCoordinatorServiceServer(gs, r)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	return r, lis.Addr().String(), gs.Stop
}

// runWorker runs the worker with c until the test ends, and returns what
// it reported as listening and a function that stops it and returns run's
// error.
func runWorker(t *testing.T, c config) (*pb.WorkerInfo, net.Addr, func() error) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	type ready struct {
		info    *pb.WorkerInfo
		metrics net.Addr
	}
	up, done := make(chan ready, 1), make(chan error, 1)
	go func() { done <- run(ctx, c, func(i *pb.WorkerInfo, m net.Addr) { up <- ready{i, m} }) }()
	select {
	case r := <-up:
		var once sync.Once
		var err error
		stop := func() error {
			once.Do(func() { cancel(); err = <-done })
			return err
		}
		t.Cleanup(func() { _ = stop() })
		return r.info, r.metrics, stop
	case err := <-done:
		cancel()
		t.Fatal(err)
	}
	return nil, nil, nil
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); !ok(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

// With -listen "" a worker serves nothing and runs a job from the shared
// -work-dir: it advertises no address, and its map output is a file:// URI.
func TestRunSharedWorkDir(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in.txt")
	if err := os.WriteFile(in, []byte("a b a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	coord, addr, _ := coordinatorAt(t, "")
	sub, err := coord.SubmitJob(t.Context(), &pb.SubmitJobRequest{Job: &pb.JobSpec{
		Program: "wordcount", Inputs: []string{in}, Output: filepath.Join(dir, "out"), NumReduceTasks: 1,
	}})
	if err != nil {
		t.Fatal(err)
	}
	info, _, stop := runWorker(t, config{coordinator: addr, workDir: filepath.Join(dir, "shared")})
	if info.GetShuffleAddress() != "" {
		t.Fatalf("advertised %q with -listen \"\"", info.GetShuffleAddress())
	}
	eventually(t, "the job", func() bool {
		st, err := coord.GetJobStatus(t.Context(), &pb.GetJobStatusRequest{JobId: sub.GetJobId()})
		return err == nil && st.GetState() == pb.JobState_JOB_STATE_SUCCEEDED
	})
	coord.mu.Lock()
	outputs := coord.outputs
	coord.mu.Unlock()
	if len(outputs) == 0 {
		t.Fatal("no map output reported")
	}
	for _, loc := range outputs {
		if loc.GetWorkerAddress() != "" || !strings.HasPrefix(loc.GetUri(), "file://"+filepath.Join(dir, "shared")) {
			t.Fatalf("map output %v; want a file:// URI in the shared directory", loc)
		}
	}
	if err := stop(); err != nil {
		t.Fatalf("run after cancel: %v", err)
	}
}

// -advertise is the address the coordinator hears; -http serves /metrics;
// and a worker registers again when its coordinator restarts.
func TestRunAdvertiseMetricsAndRestart(t *testing.T) {
	coord, addr, stopCoordinator := coordinatorAt(t, "")
	_, metrics, stop := runWorker(t, config{
		coordinator: addr, listen: "127.0.0.1:0", advertise: "example:7071", http: "127.0.0.1:0",
	})
	eventually(t, "a registration", func() bool { return len(coord.registered()) == 1 })
	if got := coord.registered()[0].GetShuffleAddress(); got != "example:7071" {
		t.Fatalf("registered with %q, want example:7071", got)
	}
	resp, err := http.Get("http://" + metrics.String() + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "mr_worker_registrations_total 1\n") {
		t.Fatalf("GET /metrics: %d\n%.500s", resp.StatusCode, body)
	}

	stopCoordinator()
	again, _, _ := coordinatorAt(t, addr)
	eventually(t, "a registration with the restarted coordinator", func() bool { return len(again.registered()) == 1 })
	if err := stop(); err != nil {
		t.Fatalf("run after cancel: %v", err)
	}
}

// Without -work-dir the worker removes its temporary directory on exit.
func TestRunRemovesTempWorkDir(t *testing.T) {
	_, addr, _ := coordinatorAt(t, "")
	t.Setenv("TMPDIR", t.TempDir())
	_, _, stop := runWorker(t, config{coordinator: addr, listen: "127.0.0.1:0"})
	dirs, _ := filepath.Glob(filepath.Join(os.Getenv("TMPDIR"), "mr-worker-*"))
	if len(dirs) != 1 {
		t.Fatalf("work directories %v, want one", dirs)
	}
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dirs[0]); !os.IsNotExist(err) {
		t.Fatalf("work directory after exit: %v", err)
	}
}
