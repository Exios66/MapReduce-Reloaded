package main

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/exios66/mapreduce-reloaded/go/examples/wordcount"
	pb "github.com/exios66/mapreduce-reloaded/go/gen/mapreduce/v1"
	"github.com/exios66/mapreduce-reloaded/go/internal/coordinator"
	"github.com/exios66/mapreduce-reloaded/go/internal/worker"
	"github.com/exios66/mapreduce-reloaded/go/pkg/api"
)

const raven = "../../../testdata/corpus/raven.txt"

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
	if !regexp.MustCompile(`^job-1\tSUCCEEDED\nmap\t1/1\nreduce\t2/2\nbackups\t0\nbackups_won\t0\nseconds\t\d+\.\d{3}\n`).MatchString(stdout.String()) {
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
