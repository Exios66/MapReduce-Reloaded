package main

import (
	"context"
	"errors"
	"flag"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/resolver"
	"google.golang.org/grpc/resolver/manual"
	"google.golang.org/grpc/status"

	"github.com/exios66/mapreduce-reloaded/go/examples/wordcount"
	pb "github.com/exios66/mapreduce-reloaded/go/gen/mapreduce/v1"
	"github.com/exios66/mapreduce-reloaded/go/internal/coordinator"
	"github.com/exios66/mapreduce-reloaded/go/internal/worker"
	"github.com/exios66/mapreduce-reloaded/go/pkg/api"
)

const raven = "../../../testdata/corpus/raven.txt"

func TestCoordinatorDefaultHelp(t *testing.T) {
	for _, command := range []string{"submit", "status"} {
		t.Run(command, func(t *testing.T) {
			var stdout, stderr strings.Builder
			if err := run([]string{command, "-h"}, &stdout, &stderr); !errors.Is(err, flag.ErrHelp) {
				t.Fatalf("help returned %v, want flag.ErrHelp", err)
			}
			if !strings.Contains(stderr.String(), `coordinator address (default "127.0.0.1:18770")`) {
				t.Fatalf("help does not advertise the host endpoint: %q", stderr.String())
			}
			if stdout.Len() != 0 {
				t.Fatalf("help wrote to stdout: %q", stdout.String())
			}
		})
	}
}

// Resolve each CLI target to an ephemeral listener, so testing the default
// does not require binding the host's real coordinator port.
type coordinatorAddressResolver struct {
	address string
	targets chan string
}

func (r *coordinatorAddressResolver) Scheme() string { return "mr-coordinator-test" }

func (r *coordinatorAddressResolver) Build(target resolver.Target, cc resolver.ClientConn, opts resolver.BuildOptions) (resolver.Resolver, error) {
	r.targets <- target.Endpoint()
	b := manual.NewBuilderWithScheme(r.Scheme())
	b.InitialState(resolver.State{Addresses: []resolver.Address{{Addr: r.address}}})
	return b.Build(target, cc, opts)
}

type addressCoordinator struct {
	pb.UnimplementedCoordinatorServiceServer
}

func (addressCoordinator) SubmitJob(context.Context, *pb.SubmitJobRequest) (*pb.SubmitJobResponse, error) {
	return &pb.SubmitJobResponse{JobId: "job-1"}, nil
}

func (addressCoordinator) GetJobStatus(_ context.Context, req *pb.GetJobStatusRequest) (*pb.GetJobStatusResponse, error) {
	if req.GetJobId() != "job-1" {
		return nil, status.Error(codes.NotFound, "unknown job")
	}
	return &pb.GetJobStatusResponse{JobId: "job-1", State: pb.JobState_JOB_STATE_MAPPING}, nil
}

func TestCoordinatorAddress(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	gs := grpc.NewServer()
	pb.RegisterCoordinatorServiceServer(gs, addressCoordinator{})
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	r := &coordinatorAddressResolver{address: lis.Addr().String(), targets: make(chan string, 1)}
	resolver.Register(r)
	// Keep these subtests sequential: the default scheme is process-wide.
	previousScheme := resolver.GetDefaultScheme()
	resolver.SetDefaultScheme(r.Scheme())
	t.Cleanup(func() { resolver.SetDefaultScheme(previousScheme) })

	for _, tc := range []struct {
		name, address string
		flags         []string
	}{
		{name: "default", address: "127.0.0.1:18770"},
		{name: "compose", address: "coordinator:7070", flags: []string{"-coordinator", "coordinator:7070"}},
		{name: "custom-port", address: "127.0.0.1:27070", flags: []string{"-coordinator=127.0.0.1:27070"}},
		{name: "ipv6", address: "[::1]:7070", flags: []string{"-coordinator", "[::1]:7070"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkTarget := func() {
				t.Helper()
				select {
				case got := <-r.targets:
					if got != tc.address {
						t.Fatalf("RPC target %q, want %q", got, tc.address)
					}
				case <-time.After(time.Second):
					t.Fatal("RPC did not resolve a coordinator target")
				}
			}
			var stdout strings.Builder
			args := append([]string{"submit"}, tc.flags...)
			args = append(args, "wordcount", filepath.Join(t.TempDir(), "out"), raven)
			err := run(args, &stdout, new(strings.Builder))
			checkTarget()
			if err != nil {
				t.Fatal(err)
			}
			jobID := strings.TrimSpace(stdout.String())
			if stdout.String() != "job-1\n" {
				t.Fatalf("submit printed %q", stdout.String())
			}

			stdout.Reset()
			args = append([]string{"status"}, tc.flags...)
			err = run(append(args, jobID), &stdout, new(strings.Builder))
			checkTarget()
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(stdout.String(), jobID+"\tMAPPING\n") {
				t.Fatalf("status printed %q", stdout.String())
			}

			stdout.Reset()
			err = run(append(args, "missing"), &stdout, new(strings.Builder))
			checkTarget()
			if status.Code(err) != codes.NotFound {
				t.Fatalf("missing job returned %v, want NotFound", err)
			}
			if stdout.Len() != 0 {
				t.Fatalf("failed status wrote %q", stdout.String())
			}
		})
	}
}

// test.param emits the value of its "key" parameter once per line.
func init() {
	api.Register("test.param", api.Program{
		Mapper: api.MapFunc(func(ctx api.Context, _, _ []byte, out api.Emitter) error {
			return out.Emit([]byte(ctx.Param("key")), []byte("1"))
		}),
		Reducer: api.ReduceFunc(wordcount.Sum),
	})
}

// Each -param reaches the program, and a later one with the same name wins.
func TestParams(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out")
	args := []string{"run", "-local", "-param", "key=old", "-param", "key=a=b", "test.param", out, raven}
	if err := run(args, new(strings.Builder), new(strings.Builder)); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(out, "part-r-00000"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(got), "a=b\t") || strings.Count(string(got), "\n") != 1 {
		t.Fatalf("output %q; want one record keyed a=b", got)
	}
}

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

// -combiner=false runs the job without its combiner, for the evaluation's
// E1: the output is the same, and no combine counters are counted.
func TestCombinerSwitch(t *testing.T) {
	dir := t.TempDir()
	job := func(out string, flags ...string) string {
		t.Helper()
		var stderr strings.Builder
		args := append(append([]string{"run", "-local"}, flags...), "wordcount", filepath.Join(dir, out), raven)
		if err := run(args, new(strings.Builder), &stderr); err != nil {
			t.Fatal(err)
		}
		return stderr.String()
	}
	with, without := job("with"), job("without", "-combiner=false")
	if !strings.Contains(with, "combine.input.records\t") {
		t.Errorf("combiner on, but no combine counters: %q", with)
	}
	if strings.Contains(without, "combine.") {
		t.Errorf("-combiner=false, but combine counters: %q", without)
	}
	a, errA := os.ReadFile(filepath.Join(dir, "with", "part-r-00000"))
	b, errB := os.ReadFile(filepath.Join(dir, "without", "part-r-00000"))
	if err := errors.Join(errA, errB); err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Error("the output differs with and without the combiner")
	}
}

// specCoordinator records the job it is sent.
type specCoordinator struct {
	pb.UnimplementedCoordinatorServiceServer
	specs chan *pb.JobSpec
}

func (c specCoordinator) SubmitJob(_ context.Context, req *pb.SubmitJobRequest) (*pb.SubmitJobResponse, error) {
	c.specs <- req.GetJob()
	return &pb.SubmitJobResponse{JobId: "job-1"}, nil
}

// mr submit asks for the combiner unless -combiner=false.
func TestSubmitCombiner(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	c := specCoordinator{specs: make(chan *pb.JobSpec, 2)}
	gs := grpc.NewServer()
	pb.RegisterCoordinatorServiceServer(gs, c)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	for _, tc := range []struct {
		flags []string
		want  bool
	}{{nil, true}, {[]string{"-combiner=false"}, false}} {
		args := append(append([]string{"submit", "-coordinator", lis.Addr().String()}, tc.flags...),
			"wordcount", filepath.Join(t.TempDir(), "out"), raven)
		if err := run(args, new(strings.Builder), new(strings.Builder)); err != nil {
			t.Fatal(err)
		}
		if got := (<-c.specs).GetUseCombiner(); got != tc.want {
			t.Errorf("%v: use_combiner %v, want %v", tc.flags, got, tc.want)
		}
	}
}

// -input-format kv reads what -output-format kv wrote: one record per
// distinct word of the first job.
func TestInputFormat(t *testing.T) {
	dir := t.TempDir()
	counters := func(args ...string) string {
		t.Helper()
		var stderr strings.Builder
		if err := run(append([]string{"run", "-local"}, args...), new(strings.Builder), &stderr); err != nil {
			t.Fatal(err)
		}
		return stderr.String()
	}
	first := counters("-output-format", "kv", "-r", "2", "wordcount", filepath.Join(dir, "words"), raven)
	second := counters("-input-format", "kv", "wordcount", filepath.Join(dir, "again"), filepath.Join(dir, "words", "part-r-*"))
	words := regexp.MustCompile(`reduce\.output\.records\t(\d+)`).FindStringSubmatch(first)
	records := regexp.MustCompile(`map\.input\.records\t(\d+)`).FindStringSubmatch(second)
	if words == nil || records == nil || words[1] != records[1] {
		t.Fatalf("first job wrote %v records, second read %v", words, records)
	}
}

// Submission waits through an initial connection failure while the coordinator starts.
func TestSubmitWaitsForCoordinator(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lis.Close() }()

	gs := grpc.NewServer()
	pb.RegisterCoordinatorServiceServer(gs, coordinator.NewServer(coordinator.Config{}))
	defer gs.Stop()
	go func() {
		// Reject the first connection before serving, as during a cold start.
		conn, err := lis.Accept()
		if err != nil {
			return
		}
		_ = conn.Close()
		_ = gs.Serve(lis)
	}()

	var stdout strings.Builder
	if err := submit([]string{"-coordinator", lis.Addr().String(), "-r", "1", "wordcount", filepath.Join(t.TempDir(), "out"), raven}, &stdout, new(strings.Builder)); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "job-1\n" {
		t.Fatalf("submit printed %q", stdout.String())
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
	if err := run([]string{"submit", "-coordinator", addr, "-max-skipped", "0", "-wait", "wordcount", out, raven}, &stdout, new(strings.Builder)); err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^job-1\tSUCCEEDED\nmap\t1/1\nreduce\t2/2\nbackups\t0\nbackups_won\t0\nmap_host_local\t0\nmap_zone_local\t0\nseconds\t\d+\.\d{3}\n`).MatchString(stdout.String()) {
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
		{[]string{"run", "-local", "nope", "out", raven}, `unknown program "nope"; registered: distsort, grep, invertedindex, reverselinks, termvector, test.param, urlfreq, wordcount`},
		{[]string{"run", "-local", "-param", "novalue", "wordcount", "out", raven}, "not name=value"},
		{[]string{"submit", "-param", "=v", "wordcount", "out", raven}, "not name=value"},
		{[]string{"run", "-local", "-r", "4294967296", "wordcount", "out", "in"}, "too large"},
		{[]string{"run", "-local", "-m", "2", "wordcount", "out", "missing"}, "no input matches"},
		{[]string{"run", "-local", "-intermediate-codec", "gzip", "wordcount", "out", raven}, `unsupported_codec: "gzip"`},
		{[]string{"run", "-local", "-output-codec", "zstd", "wordcount", "out", raven}, "takes no codec"},
		{[]string{"run", "-local", "-output-format", "parquet", "wordcount", "out", raven}, `unknown format "parquet"`},
		{[]string{"run", "-local", "-input-format", "csv", "wordcount", "out", raven}, `unknown format "csv"`},
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
