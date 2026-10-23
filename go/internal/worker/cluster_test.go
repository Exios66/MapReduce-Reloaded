package worker_test

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/exios66/mapreduce-reloaded/go/examples/wordcount"
	pb "github.com/exios66/mapreduce-reloaded/go/gen/mapreduce/v1"
	"github.com/exios66/mapreduce-reloaded/go/internal/coordinator"
	"github.com/exios66/mapreduce-reloaded/go/internal/local"
	"github.com/exios66/mapreduce-reloaded/go/internal/split"
	"github.com/exios66/mapreduce-reloaded/go/internal/task"
	"github.com/exios66/mapreduce-reloaded/go/internal/worker"
	"github.com/exios66/mapreduce-reloaded/go/pkg/api"
)

const corpus = "../../../testdata/corpus"

// flaky is word count whose map tasks fail on their first attempt.
func init() {
	p := wordcount.Program
	p.Mapper = api.MapFunc(func(ctx api.Context, key, value []byte, out api.Emitter) error {
		if ctx.Task().Attempt == 0 {
			return errors.New("first attempts fail")
		}
		return wordcount.Map(ctx, key, value, out)
	})
	api.Register("test.flaky", p)
}

// startCoordinator serves a coordinator on a free localhost port and returns
// it with a client.
func startCoordinator(t *testing.T) (*coordinator.Server, pb.CoordinatorServiceClient) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := coordinator.NewServer(coordinator.Config{HeartbeatInterval: time.Second, RetryAfter: 10 * time.Millisecond})
	gs := grpc.NewServer()
	pb.RegisterCoordinatorServiceServer(gs, srv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return srv, pb.NewCoordinatorServiceClient(conn)
}

// runJob runs program over the corpus on n workers, which join one after
// another. It returns the output directory, the directory holding the map
// output, and the job's final status.
func runJob(t *testing.T, program string, n int) (out, mapDir string, status *pb.GetJobStatusResponse) {
	t.Helper()
	srv, client := startCoordinator(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	inputs := []string{filepath.Join(corpus, "*.txt")}
	splitSize, err := split.SizeFor(inputs, 7)
	if err != nil {
		t.Fatal(err)
	}
	out = filepath.Join(t.TempDir(), "out")
	sub, err := client.SubmitJob(ctx, &pb.SubmitJobRequest{Job: &pb.JobSpec{
		Program: program, Inputs: inputs, Output: out,
		SplitSizeBytes: splitSize, NumReduceTasks: 4, UseCombiner: true,
	}})
	if err != nil {
		t.Fatal(err)
	}

	workDir := t.TempDir()
	done := make(chan error, n)
	for i := range n {
		go func() {
			time.Sleep(time.Duration(i) * 20 * time.Millisecond)
			done <- worker.New(client, workDir, &pb.WorkerInfo{}).Run(ctx)
		}()
	}

	for status.GetState() != pb.JobState_JOB_STATE_SUCCEEDED {
		time.Sleep(10 * time.Millisecond)
		if status, err = client.GetJobStatus(ctx, &pb.GetJobStatusRequest{JobId: sub.GetJobId()}); err != nil {
			t.Fatal(err)
		}
	}
	srv.Shutdown()
	for range n {
		if err := <-done; err != nil {
			t.Fatalf("worker: %v", err)
		}
	}
	return out, filepath.Join(workDir, sub.GetJobId()), status
}

func TestClusterWordCount(t *testing.T) {
	out, _, status := runJob(t, "wordcount", 3)
	assertGolden(t, out)
	if status.GetMapTasksTotal() < 7 || status.GetReduceTasksCompleted() != 4 ||
		status.GetCounters().GetValues()[task.ReduceOutputRecords] != 11786 {
		t.Fatalf("status %v", status)
	}
}

// Failed attempts are retried, and only accepted attempts count.
func TestClusterRetriesFailedTasks(t *testing.T) {
	out, _, status := runJob(t, "test.flaky", 2)
	assertGolden(t, out)
	if got := status.GetCounters().GetValues()[task.MapOutputRecords]; got != 59372 {
		t.Fatalf("map.output.records = %d, want 59372", got)
	}
}

// The cluster's map output is byte for byte what local mode writes for the
// same splits, however the tasks were spread over the workers.
func TestClusterMapOutputMatchesLocal(t *testing.T) {
	_, mapDir, _ := runJob(t, "wordcount", 3)
	inputs := []string{filepath.Join(corpus, "*.txt")}
	splitSize, err := split.SizeFor(inputs, 7)
	if err != nil {
		t.Fatal(err)
	}
	localDir := t.TempDir()
	if _, err := local.Run(api.JobSpec{
		Program: wordcount.Program, Inputs: inputs, Output: filepath.Join(t.TempDir(), "out"),
		SplitSize: splitSize, R: 4,
	}, localDir); err != nil {
		t.Fatal(err)
	}
	assertSameFiles(t, mapDir, localDir)
}

func assertGolden(t *testing.T, out string) {
	t.Helper()
	assertSameFiles(t, out, filepath.Join(corpus, "expected"))
}

// assertSameFiles checks that dirs got and want hold the same files.
func assertSameFiles(t *testing.T, got, want string) {
	t.Helper()
	wantFiles, _ := filepath.Glob(filepath.Join(want, "*"))
	gotFiles, _ := filepath.Glob(filepath.Join(got, "*"))
	if len(wantFiles) == 0 || len(gotFiles) != len(wantFiles) {
		t.Fatalf("%s has %d files, want %d: %v", got, len(gotFiles), len(wantFiles), gotFiles)
	}
	for _, w := range wantFiles {
		wb, _ := os.ReadFile(w)
		gb, err := os.ReadFile(filepath.Join(got, filepath.Base(w)))
		if err != nil || !bytes.Equal(gb, wb) {
			t.Errorf("%s differs from %s (%v)", filepath.Base(w), w, err)
		}
	}
}
