package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"io"
	"maps"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
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
	"github.com/exios66/mapreduce-reloaded/go/internal/jobfile"
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
	input := filepath.Join(dir, "input.txt")
	if err := os.WriteFile(input, []byte("alpha beta alpha\nbeta gamma\nalpha\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Keep this sequence: disabling one job must not disable later jobs.
	for _, tc := range []struct {
		name  string
		flags []string
		want  bool
	}{
		{"disabled", []string{"-combiner=false"}, false},
		{"default-after-disabled", nil, true},
		{"enabled", []string{"-combiner=true"}, true},
		{"re-enabled", []string{"-combiner=false", "-combiner=true"}, true},
		{"disabled-last", []string{"-combiner=true", "-combiner=false"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := filepath.Join(dir, tc.name)
			args := append([]string{"run", "-local", "-m", "2", "-r", "3"}, tc.flags...)
			args = append(args, "wordcount", out, input)
			var stderr strings.Builder
			if err := run(args, new(strings.Builder), &stderr); err != nil {
				t.Fatal(err)
			}
			if got := strings.Contains(stderr.String(), "combine.input.records\t"); got != tc.want {
				t.Errorf("combiner counters present = %v, want %v: %q", got, tc.want, stderr.String())
			}
			if !tc.want && strings.Contains(stderr.String(), "combine.") {
				t.Errorf("disabled combiner emitted counters: %q", stderr.String())
			}
			parts, err := filepath.Glob(filepath.Join(out, "part-r-*"))
			if err != nil || len(parts) != 3 {
				t.Fatalf("output parts = %v, error = %v; want 3 parts", parts, err)
			}
			got := map[string]string{}
			for _, part := range parts {
				data, err := os.ReadFile(part)
				if err != nil {
					t.Fatal(err)
				}
				for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
					if line == "" {
						continue
					}
					key, value, ok := strings.Cut(line, "\t")
					if _, duplicate := got[key]; !ok || duplicate {
						t.Fatalf("malformed or duplicate output record %q", line)
					}
					got[key] = value
				}
			}
			if want := (map[string]string{"alpha": "3", "beta": "2", "gamma": "1"}); !maps.Equal(got, want) {
				t.Errorf("word counts = %v, want %v", got, want)
			}
			if program, ok := api.Lookup("wordcount"); !ok || program.Combiner == nil {
				t.Error("running a job changed the registered program's combiner")
			}
		})
	}
}

func TestCombinerWithoutProgramCombiner(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.txt")
	if err := os.WriteFile(input, []byte("hit\nmiss\nhit\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"true", "false"} {
		t.Run(value, func(t *testing.T) {
			out := filepath.Join(dir, value)
			var stderr strings.Builder
			args := []string{"run", "-local", "-combiner=" + value, "-param", "pattern=hit", "grep", out, input}
			if err := run(args, new(strings.Builder), &stderr); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(out, "part-r-00000"))
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != "hit\t\nhit\t\n" {
				t.Errorf("output = %q, want both matching lines", data)
			}
			if strings.Contains(stderr.String(), "combine.") {
				t.Errorf("program without a combiner emitted combine counters: %q", stderr.String())
			}
		})
	}
}

func TestCombinerInvalid(t *testing.T) {
	for _, command := range []string{"run", "submit"} {
		for _, value := range []string{"maybe", ""} {
			t.Run(command+"/"+value, func(t *testing.T) {
				out := filepath.Join(t.TempDir(), "out")
				args := []string{command}
				if command == "run" {
					args = append(args, "-local")
				}
				args = append(args, "-combiner="+value, "wordcount", out, raven)
				var stdout, stderr strings.Builder
				err := run(args, &stdout, &stderr)
				if err == nil || !strings.Contains(err.Error(), "invalid boolean value") {
					t.Fatalf("error = %v, want invalid boolean value", err)
				}
				if stdout.Len() != 0 {
					t.Errorf("invalid flag wrote to stdout: %q", stdout.String())
				}
				if _, err := os.Stat(out); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("invalid flag created output, stat error = %v", err)
				}
			})
		}
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
		name  string
		flags []string
		want  bool
	}{
		{"disabled", []string{"-combiner=false"}, false},
		{"default-after-disabled", nil, true},
		{"enabled", []string{"-combiner=true"}, true},
		{"re-enabled", []string{"-combiner=false", "-combiner=true"}, true},
		{"disabled-last", []string{"-combiner=true", "-combiner=false"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"submit", "-coordinator", lis.Addr().String()}, tc.flags...)
			args = append(args, "wordcount", filepath.Join(t.TempDir(), "out"), raven)
			var stdout strings.Builder
			if err := run(args, &stdout, new(strings.Builder)); err != nil {
				t.Fatal(err)
			}
			if stdout.String() != "job-1\n" {
				t.Errorf("submit printed %q, want job ID", stdout.String())
			}
			select {
			case spec := <-c.specs:
				if got := spec.GetUseCombiner(); got != tc.want {
					t.Errorf("use_combiner %v, want %v", got, tc.want)
				}
			case <-time.After(time.Second):
				t.Fatal("coordinator received no job spec")
			}
		})
	}
}

const exampleJob = "../../../examples/wordcount.go"

// A .go path in place of a program name runs that job file; the example's
// output is the compiled wordcount's expected output.
func TestRunLocalJobFile(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out")
	args := []string{"run", "-local", "-r", "4", exampleJob, out, "../../../testdata/corpus/*.txt"}
	if err := run(args, new(strings.Builder), new(strings.Builder)); err != nil {
		t.Fatal(err)
	}
	want, err := filepath.Glob("../../../testdata/corpus/expected/*")
	if err != nil {
		t.Fatal(err)
	}
	if len(want) == 0 {
		t.Fatal("no golden files found")
	}
	for _, w := range want {
		a, errA := os.ReadFile(w)
		b, errB := os.ReadFile(filepath.Join(out, filepath.Base(w)))
		if errA != nil || errB != nil || string(a) != string(b) {
			t.Errorf("%s differs from the expected output", filepath.Base(w))
		}
	}
	err = run([]string{"run", "-local", "missing.go", out + "2", raven}, new(strings.Builder), new(strings.Builder))
	if err == nil || !strings.Contains(err.Error(), "missing.go") {
		t.Errorf("a missing job file: got %v", err)
	}
}

// mr submit reads a job file on the client and sends its source.
func TestSubmitJobFile(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	c := specCoordinator{specs: make(chan *pb.JobSpec, 1)}
	gs := grpc.NewServer()
	pb.RegisterCoordinatorServiceServer(gs, c)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	args := []string{"submit", "-coordinator", lis.Addr().String(), exampleJob, filepath.Join(t.TempDir(), "out"), raven}
	if err := run(args, new(strings.Builder), new(strings.Builder)); err != nil {
		t.Fatal(err)
	}
	src, _ := os.ReadFile(exampleJob)
	spec := <-c.specs
	if spec.GetProgram() != "wordcount.go" || spec.GetProgramSource() != string(src) {
		t.Fatalf("sent program %q with %d bytes of source", spec.GetProgram(), len(spec.GetProgramSource()))
	}
}

func TestReadJobFileBoundaries(t *testing.T) {
	for _, size := range []int{0, jobfile.MaxSize - 1, jobfile.MaxSize, jobfile.MaxSize + 1} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "job.go")
			want := strings.Repeat("x", size)
			if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
				t.Fatal(err)
			}
			got, err := readJobFile(path)
			if size > jobfile.MaxSize {
				if err == nil || !strings.Contains(err.Error(), "1 MiB limit") || got != nil {
					t.Fatalf("oversized file: %d bytes, %v", len(got), err)
				}
			} else if err != nil || string(got) != want {
				t.Fatalf("read %d bytes, error=%v; want %d", len(got), err, size)
			}
		})
	}
	if _, err := readJobFile(filepath.Join(t.TempDir(), "missing.go")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing file: %v", err)
	}
	if _, err := readJobFile(t.TempDir()); err == nil || !strings.Contains(err.Error(), "job file") {
		t.Fatalf("directory accepted: %v", err)
	}
}

// The display name must not expose the client directory, and loading
// must use the captured source even if the file is later removed.
func TestJobFlagsCaptureSource(t *testing.T) {
	src, err := os.ReadFile(exampleJob)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "my wordcount.go")
	if err := os.WriteFile(path, src, 0o644); err != nil {
		t.Fatal(err)
	}
	fs := newFlagSet("run", new(strings.Builder))
	flags := addJobFlags(fs, 2)
	if err := fs.Parse([]string{"-combiner=false", path, "output", raven}); err != nil {
		t.Fatal(err)
	}
	j, err := flags.job(fs)
	if err != nil {
		t.Fatal(err)
	}
	if j.program != "my wordcount.go" || j.source != string(src) || j.combiner {
		t.Fatalf("captured job=%+v", j)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	p, err := j.load()
	if err != nil || p.Mapper == nil || p.Reducer == nil || p.Combiner == nil {
		t.Fatalf("load captured source: %+v, %v", p, err)
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

// -m is the exact number of map tasks, and submit reports it.
func TestSubmitExactM(t *testing.T) {
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
	done := make(chan error, 1)
	go func() { done <- worker.New(client, t.TempDir(), &pb.WorkerInfo{}).Run(context.Background()) }()

	out := filepath.Join(t.TempDir(), "out")
	var stdout, stderr strings.Builder
	if err := run([]string{"submit", "-coordinator", addr, "-m", "5", "-r", "1", "-wait", "wordcount", out, raven}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "\nmap\t5/5\n") || stderr.String() != "mr: 5 map tasks (-m 5)\n" {
		t.Fatalf("submit printed %q, stderr %q", stdout.String(), stderr.String())
	}
	srv.Shutdown()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// upload copies files, directories and globs to where the containers see
// /data/..., and cat prints a job's output files in order.
func TestUploadAndCat(t *testing.T) {
	data := t.TempDir()
	t.Setenv("MR_DATA_DIR", data)
	src := t.TempDir()
	write := func(name, text string) {
		t.Helper()
		p := filepath.Join(src, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("a.txt", "a")
	write("dir/b.txt", "b")
	write("dir/sub/c.txt", "c")
	write("g1.log", "g1")
	write("g2.log", "g2")

	var stdout strings.Builder
	args := []string{"upload", filepath.Join(src, "a.txt"), filepath.Join(src, "dir"), filepath.Join(src, "*.log"), "/data/input/x"}
	if err := run(args, &stdout, new(strings.Builder)); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"a.txt": "a", "b.txt": "b", "sub/c.txt": "c", "g1.log": "g1", "g2.log": "g2"} {
		p := filepath.Join(data, "input", "x", name)
		got, err := os.ReadFile(p)
		if err != nil || string(got) != want {
			t.Errorf("%s: got %q, %v; want %q", name, got, err, want)
		}
		if info, err := os.Stat(p); err == nil && info.Mode().Perm()&0o044 != 0o044 {
			t.Errorf("%s: mode %v is not readable by the containers", name, info.Mode())
		}
	}
	if n := strings.Count(stdout.String(), "\n"); n != 5 {
		t.Errorf("upload printed %d lines, want 5: %q", n, stdout.String())
	}
	if tmp, _ := filepath.Glob(filepath.Join(data, "input", "x", ".upload-*")); len(tmp) != 0 {
		t.Errorf("temporary files left: %v", tmp)
	}

	out := filepath.Join(data, "out", "wc")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{"part-r-00001": "b\t2\n", "part-r-00000": "a\t1\n", "_SUCCESS": "x"} {
		if err := os.WriteFile(filepath.Join(out, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	stdout.Reset()
	if err := run([]string{"cat", "/data/out/wc/"}, &stdout, new(strings.Builder)); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "a\t1\nb\t2\n" {
		t.Errorf("cat printed %q", stdout.String())
	}
}

func TestSharedPathErrors(t *testing.T) {
	t.Setenv("MR_DATA_DIR", t.TempDir())
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"cat", "/out/wc"}, "not in shared storage"},
		{[]string{"cat", "/database/wc"}, "not in shared storage"},
		{[]string{"cat", "/data/../etc"}, "not in shared storage"},
		{[]string{"cat", "/data/out/none"}, "no part-r-* files"},
		{[]string{"upload", "/no/such/file", "/data/input"}, "no such file"},
		{[]string{"upload", "/data/input"}, "usage"},
	} {
		err := run(tc.args, new(strings.Builder), new(strings.Builder))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: error %v, want %q", tc.args, err, tc.want)
		}
	}
	t.Setenv("MR_DATA_DIR", filepath.Join(t.TempDir(), "missing"))
	if err := run([]string{"cat", "/data/out/wc"}, new(strings.Builder), new(strings.Builder)); err == nil || !strings.Contains(err.Error(), "MR_DATA_DIR") {
		t.Errorf("missing data directory: error %v", err)
	}
}

// A failed command exits with status 1 and prints its error after "mr:".
func TestExitCode(t *testing.T) {
	var stderr bytes.Buffer
	if code := exitCode(run([]string{"nope"}, io.Discard, &stderr), &stderr); code != 1 || !strings.HasPrefix(stderr.String(), "mr: ") {
		t.Fatalf("exit %d, stderr %q; want 1 and an mr: message", code, stderr.String())
	}
	for _, err := range []error{nil, flag.ErrHelp} {
		stderr.Reset()
		if code := exitCode(err, &stderr); code != 0 || stderr.Len() != 0 {
			t.Fatalf("exit %d, stderr %q for %v; want 0 and nothing", code, stderr.String(), err)
		}
	}
	stderr.Reset()
	if code := exitCode(run([]string{"status", "-h"}, io.Discard, &stderr), io.Discard); code != 0 || !strings.Contains(stderr.String(), "Usage") {
		t.Fatalf("mr status -h: exit %d, stderr %q; want 0 and the usage", code, stderr.String())
	}
}
