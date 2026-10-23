package main

import (
	"context"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"

	pb "github.com/exios66/mapreduce-reloaded/go/gen/mapreduce/v1"
	"github.com/exios66/mapreduce-reloaded/go/internal/coordinator"
	"github.com/exios66/mapreduce-reloaded/go/internal/worker"
)

const raven = "../../../testdata/corpus/raven.txt"

func TestRunLocal(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out")
	var stderr strings.Builder
	args := []string{"run", "-local", "-m", "2", "-r", "3", "wordcount", out, raven}
	if err := run(args, new(strings.Builder), &stderr); err != nil {
		t.Fatal(err)
	}
	if parts, _ := filepath.Glob(filepath.Join(out, "part-r-*")); len(parts) != 3 {
		t.Errorf("got %d part files, want 3", len(parts))
	}
	if !strings.Contains(stderr.String(), "map.input.records\t") {
		t.Errorf("counters not printed: %q", stderr.String())
	}
}

// submit -wait runs a job on a cluster, and status reports it.
func TestSubmitAndStatus(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := coordinator.NewServer(coordinator.Config{RetryAfter: 10 * time.Millisecond})
	gs := grpc.NewServer()
	pb.RegisterCoordinatorServiceServer(gs, srv)
	go func() { _ = gs.Serve(lis) }()
	defer gs.Stop()

	addr := lis.Addr().String()
	client, closeConn, err := dial(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer closeConn()
	done := make(chan error, 1) // buffered, so the worker never blocks if the test fails first
	go func() { done <- worker.New(client, t.TempDir(), &pb.WorkerInfo{}).Run(context.Background()) }()

	out := filepath.Join(t.TempDir(), "out")
	var stdout strings.Builder
	if err := run([]string{"submit", "-coordinator", addr, "-wait", "wordcount", out, raven}, &stdout, new(strings.Builder)); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(stdout.String(), "job-1\tSUCCEEDED\nmap\t1/1\nreduce\t2/2\n") {
		t.Fatalf("submit printed %q", stdout.String())
	}
	if parts, _ := filepath.Glob(filepath.Join(out, "part-r-*")); len(parts) != 2 {
		t.Errorf("got %d part files, want R = 2 for one worker", len(parts))
	}

	stdout.Reset()
	if err := run([]string{"status", "-coordinator", addr, "job-1"}, &stdout, new(strings.Builder)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "SUCCEEDED") || !strings.Contains(stdout.String(), "reduce.output.records\t") {
		t.Fatalf("status printed %q", stdout.String())
	}

	srv.Shutdown()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestErrors(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{nil, "usage"},
		{[]string{"nope"}, "usage"},
		{[]string{"run", "wordcount", "out", "in"}, "needs -local"},
		{[]string{"run", "-local", "wordcount", "out"}, "usage"},
		{[]string{"run", "-local", "nope", "out", raven}, `unknown program "nope"; registered: wordcount`},
		{[]string{"run", "-local", "-r", "4294967296", "wordcount", "out", "in"}, "too large"},
		{[]string{"run", "-local", "-m", "2", "wordcount", "out", "missing"}, "no input matches"},
		{[]string{"run", "-bogus"}, "not defined"},
		{[]string{"submit", "wordcount", "out"}, "usage"},
		{[]string{"submit", "-bogus"}, "not defined"},
		{[]string{"status"}, "usage"},
		{[]string{"status", "-bogus"}, "not defined"},
	} {
		if err := run(tc.args, new(strings.Builder), new(strings.Builder)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("run(%q) = %v, want an error containing %q", tc.args, err, tc.want)
		}
	}
}
