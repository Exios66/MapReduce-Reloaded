package worker_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"iter"
	"maps"
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
	"github.com/exios66/mapreduce-reloaded/go/internal/shuffle"
	"github.com/exios66/mapreduce-reloaded/go/internal/split"
	"github.com/exios66/mapreduce-reloaded/go/internal/task"
	"github.com/exios66/mapreduce-reloaded/go/internal/worker"
	"github.com/exios66/mapreduce-reloaded/go/pkg/api"
)

const corpus = "../../../testdata/corpus"

var inputs = []string{filepath.Join(corpus, "*.txt")}

// The first attempt of map task 0 of test.hang-map, and of reduce task 0 of
// test.hang-reduce, never returns, like a task on a machine that died. Each
// sends on its channel when it gets there, once per job, so the tests also
// pass under go test -count=N.
var mapStuck, reduceStuck = make(chan struct{}, 1), make(chan struct{}, 1)

// The first attempt of reduce task 0 of test.pause-reduce also sends on
// reduceStuck, then fails once resume is sent on.
var resume = make(chan struct{})

func init() {
	first := func(ctx api.Context, kind api.TaskKind) bool {
		id := ctx.Task()
		return id.Kind == kind && id.Index == 0 && id.Attempt == 0
	}

	flaky := wordcount.Program // map tasks fail on their first attempt
	flaky.Mapper = api.MapFunc(func(ctx api.Context, key, value []byte, out api.Emitter) error {
		if ctx.Task().Attempt == 0 {
			return errors.New("first attempts fail")
		}
		return wordcount.Map(ctx, key, value, out)
	})
	api.Register("test.flaky", flaky)

	hangMap := wordcount.Program
	hangMap.Mapper = api.MapFunc(func(ctx api.Context, key, value []byte, out api.Emitter) error {
		if first(ctx, api.MapTask) {
			mapStuck <- struct{}{}
			select {}
		}
		return wordcount.Map(ctx, key, value, out)
	})
	api.Register("test.hang-map", hangMap)

	hangReduce := wordcount.Program
	hangReduce.Reducer = api.ReduceFunc(func(ctx api.Context, key []byte, values iter.Seq[[]byte], out api.Emitter) error {
		if first(ctx, api.ReduceTask) {
			reduceStuck <- struct{}{}
			select {}
		}
		return wordcount.Program.Reducer.Reduce(ctx, key, values, out)
	})
	api.Register("test.hang-reduce", hangReduce)

	pauseReduce := wordcount.Program
	pauseReduce.Reducer = api.ReduceFunc(func(ctx api.Context, key []byte, values iter.Seq[[]byte], out api.Emitter) error {
		if first(ctx, api.ReduceTask) {
			reduceStuck <- struct{}{}
			<-resume
			return errors.New("paused")
		}
		return wordcount.Program.Reducer.Reduce(ctx, key, values, out)
	})
	api.Register("test.pause-reduce", pauseReduce)
}

// listen serves the services that register adds on addr, or on a free
// localhost port if addr is "". It returns the address, and stop, which
// closes the port at once.
func listen(t *testing.T, addr string, register func(*grpc.Server)) (string, func()) {
	t.Helper()
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	gs := grpc.NewServer()
	register(gs)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	return lis.Addr().String(), gs.Stop
}

// cluster is a coordinator that declares a worker dead after 100 ms of
// silence and logs to a write-ahead log. Tests add workers to it, each with
// its own work directory and shuffle server, and may kill them, or restart
// the coordinator.
type cluster struct {
	t       *testing.T
	ctx     context.Context
	srv     *coordinator.Server
	addr    string
	stop    func() // stops the coordinator
	client  pb.CoordinatorServiceClient
	workDir string
	live    []chan error // Run results of the workers not killed
}

func newCluster(t *testing.T) *cluster {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	c := &cluster{t: t, ctx: ctx, workDir: t.TempDir()}
	c.start()
	conn, err := grpc.NewClient(c.addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	c.client = pb.NewCoordinatorServiceClient(conn)
	return c
}

// start starts the coordinator from its log, on c.addr once it has one.
func (c *cluster) start() {
	srv, log, err := coordinator.Recover(coordinator.Config{
		HeartbeatInterval: 10 * time.Millisecond,
		WorkerTimeout:     100 * time.Millisecond,
		RetryAfter:        10 * time.Millisecond,
	}, filepath.Join(c.workDir, "coordinator.log"))
	if err != nil {
		c.t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(c.ctx)
	go srv.Monitor(ctx)
	addr, stop := listen(c.t, c.addr, func(gs *grpc.Server) { pb.RegisterCoordinatorServiceServer(gs, srv) })
	c.srv, c.addr = srv, addr
	c.stop = func() {
		stop()
		cancel()
		_ = log.Close()
	}
	c.t.Cleanup(c.stop)
}

// submit submits program over the corpus with about 7 map tasks and 4
// reduce tasks, and returns the job ID.
func (c *cluster) submit(program string) string {
	c.t.Helper()
	splitSize, err := split.SizeFor(inputs, 7)
	if err != nil {
		c.t.Fatal(err)
	}
	sub, err := c.client.SubmitJob(c.ctx, &pb.SubmitJobRequest{Job: &pb.JobSpec{
		Program: program, Inputs: inputs, Output: c.output(),
		SplitSizeBytes: splitSize, NumReduceTasks: 4, UseCombiner: true,
	}})
	if err != nil {
		c.t.Fatal(err)
	}
	return sub.GetJobId()
}

func (c *cluster) output() string { return filepath.Join(c.workDir, "out") }

// addWorker starts a worker. kill stops it as if its machine died: its
// heartbeats stop, and its map output can no longer be fetched.
func (c *cluster) addWorker() (kill func()) {
	dir := filepath.Join(c.workDir, fmt.Sprint(len(c.live)))
	addr, stopShuffle := listen(c.t, "", func(gs *grpc.Server) { pb.RegisterShuffleServiceServer(gs, shuffle.NewServer(dir)) })
	ctx, cancel := context.WithCancel(c.ctx)
	done := make(chan error, 1)
	go func() { done <- worker.New(c.client, dir, &pb.WorkerInfo{ShuffleAddress: addr}).Run(ctx) }()
	i := len(c.live)
	c.live = append(c.live, done)
	return func() {
		cancel()
		stopShuffle()
		c.live[i] = nil
	}
}

// wait waits for job to succeed, then shuts the cluster down and returns the
// job's final status.
func (c *cluster) wait(job string) *pb.GetJobStatusResponse {
	c.t.Helper()
	var status *pb.GetJobStatusResponse
	for status.GetState() != pb.JobState_JOB_STATE_SUCCEEDED {
		time.Sleep(10 * time.Millisecond)
		var err error
		if status, err = c.client.GetJobStatus(c.ctx, &pb.GetJobStatusRequest{JobId: job}, grpc.WaitForReady(true)); err != nil {
			c.t.Fatal(err)
		}
	}
	c.srv.Shutdown()
	for _, done := range c.live {
		if done == nil {
			continue
		}
		if err := <-done; err != nil {
			c.t.Fatalf("worker: %v", err)
		}
	}
	return status
}

// mapFiles returns the map output files of job on every worker.
func (c *cluster) mapFiles(job string) []string {
	files, _ := filepath.Glob(filepath.Join(c.workDir, "*", job, "*"))
	return files
}

// runJob runs program on n workers, which join one after another.
func runJob(t *testing.T, program string, n int) (*cluster, string, *pb.GetJobStatusResponse) {
	t.Helper()
	c := newCluster(t)
	job := c.submit(program)
	for range n {
		c.addWorker()
		time.Sleep(20 * time.Millisecond)
	}
	return c, job, c.wait(job)
}

func TestClusterWordCount(t *testing.T) {
	c, _, status := runJob(t, "wordcount", 3)
	assertGolden(t, c.output())
	if status.GetMapTasksTotal() < 7 || status.GetReduceTasksCompleted() != 4 ||
		status.GetCounters().GetValues()[task.ReduceOutputRecords] != 11786 {
		t.Fatalf("status %v", status)
	}
}

// Failed attempts are retried, and only accepted attempts count.
func TestClusterRetriesFailedTasks(t *testing.T) {
	c, _, status := runJob(t, "test.flaky", 2)
	assertGolden(t, c.output())
	assertMapOutputRecords(t, status)
}

// The cluster's map output is byte for byte what local mode writes for the
// same splits, however the tasks were spread over the workers.
func TestClusterMapOutputMatchesLocal(t *testing.T) {
	c, job, _ := runJob(t, "wordcount", 3)
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
	assertSameFiles(t, c.mapFiles(job), localDir)
}

// A worker that dies is detected, and its work is done again elsewhere
// (§3.3).
func TestClusterSurvivesWorkerLoss(t *testing.T) {
	t.Run("mid-map", func(t *testing.T) {
		// The worker dies running map task 0; another runs it again.
		c := newCluster(t)
		job := c.submit("test.hang-map")
		kill := c.addWorker()
		<-mapStuck
		kill()
		c.addWorker()
		status := c.wait(job)
		assertGolden(t, c.output())
		assertMapOutputRecords(t, status)
	})
	t.Run("after map, mid-reduce", func(t *testing.T) {
		// The worker runs every map task, then dies running reduce task 0.
		// Its map output is gone with it, so the map tasks run again, and
		// reduce tasks that were reading it fail and run again too.
		c := newCluster(t)
		job := c.submit("test.hang-reduce")
		kill := c.addWorker()
		<-reduceStuck
		kill()
		c.addWorker()
		status := c.wait(job)
		assertGolden(t, c.output())
		assertMapOutputRecords(t, status)
	})
}

// A restarted coordinator resumes the job from its log: its workers
// register again once it is back, completed map tasks are not run again,
// and the reduce task that was running is (§3.3).
func TestClusterSurvivesCoordinatorRestart(t *testing.T) {
	c := newCluster(t)
	job := c.submit("test.pause-reduce")
	c.addWorker()
	<-reduceStuck
	before := modTimes(t, c.mapFiles(job))
	c.stop()
	resume <- struct{}{}               // the paused reduce fails, and cannot report it
	time.Sleep(100 * time.Millisecond) // while the coordinator is down
	c.start()
	status := c.wait(job)
	assertGolden(t, c.output())
	assertMapOutputRecords(t, status)
	if after := modTimes(t, c.mapFiles(job)); !maps.Equal(after, before) {
		t.Fatal("map tasks ran again after the restart")
	}
}

func modTimes(t *testing.T, files []string) map[string]time.Time {
	t.Helper()
	times := map[string]time.Time{}
	for _, f := range files {
		info, err := os.Stat(f)
		if err != nil {
			t.Fatal(err)
		}
		times[f] = info.ModTime()
	}
	return times
}

// A reduce that cannot fetch a map output reports it, and the map task runs
// again, though the worker that held the output is still alive (§3.3).
func TestClusterReRunsLostMapOutput(t *testing.T) {
	c := newCluster(t)
	job := c.submit("test.pause-reduce")
	c.addWorker()
	<-reduceStuck // every map task is done
	for _, f := range c.mapFiles(job) {
		if err := os.Remove(f); err != nil {
			t.Fatal(err)
		}
	}
	resume <- struct{}{}
	status := c.wait(job)
	assertGolden(t, c.output())
	assertMapOutputRecords(t, status)
}

// assertMapOutputRecords checks that each map task's records counted once,
// however many times it ran.
func assertMapOutputRecords(t *testing.T, status *pb.GetJobStatusResponse) {
	t.Helper()
	if got := status.GetCounters().GetValues()[task.MapOutputRecords]; got != 59372 {
		t.Fatalf("%s = %d, want 59372", task.MapOutputRecords, got)
	}
}

func assertGolden(t *testing.T, out string) {
	t.Helper()
	got, _ := filepath.Glob(filepath.Join(out, "*"))
	assertSameFiles(t, got, filepath.Join(corpus, "expected"))
}

// assertSameFiles checks that the files got are the files in dir want, by
// name and content.
func assertSameFiles(t *testing.T, got []string, want string) {
	t.Helper()
	wantFiles, _ := filepath.Glob(filepath.Join(want, "*"))
	if len(wantFiles) == 0 || len(got) != len(wantFiles) {
		t.Fatalf("got %d files, want %d: %v", len(got), len(wantFiles), got)
	}
	for _, g := range got {
		w := filepath.Join(want, filepath.Base(g))
		gb, _ := os.ReadFile(g)
		wb, err := os.ReadFile(w)
		if err != nil || !bytes.Equal(gb, wb) {
			t.Errorf("%s differs from %s (%v)", g, w, err)
		}
	}
}
