// Package worker runs tasks for a coordinator (§3.1): it registers, then
// repeatedly asks for a task, runs it and reports the outcome, until the
// coordinator tells it to shut down.
//
// Map output stays in the worker's own work directory. A worker that
// advertises a shuffle address serves it from there, and reducers fetch it
// over the network (§3.1 step 5). A worker without one writes to a directory
// shared by all workers, and reducers read it from there by URI.
package worker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/exios66/mapreduce-reloaded/go/gen/mapreduce/v1"
	"github.com/exios66/mapreduce-reloaded/go/internal/extsort"
	"github.com/exios66/mapreduce-reloaded/go/internal/formats/kv"
	"github.com/exios66/mapreduce-reloaded/go/internal/shuffle"
	"github.com/exios66/mapreduce-reloaded/go/internal/task"
	"github.com/exios66/mapreduce-reloaded/go/internal/wire"
)

// defaultWait is how long to wait when the coordinator sends no assignment
// this worker understands.
const defaultWait = time.Second

// Worker runs tasks for one coordinator.
type Worker struct {
	coordinator pb.CoordinatorServiceClient
	workDir     string // where map output goes
	info        *pb.WorkerInfo
	shuffle     shuffle.Clients
	id          string
	logger      *slog.Logger
	stats       stats
}

// New returns a worker that writes map output under workDir. If
// info.ShuffleAddress is set, the caller serves workDir there with a
// shuffle.Server.
func New(coordinator pb.CoordinatorServiceClient, workDir string, info *pb.WorkerInfo) *Worker {
	return &Worker{coordinator: coordinator, workDir: workDir, info: info, logger: slog.Default()}
}

// errForgotten means the coordinator no longer knows this worker: it
// declared it dead (§3.3), or restarted.
var errForgotten = errors.New("the coordinator forgot this worker")

// Run registers and runs tasks until the coordinator says to shut down, or
// ctx is done, or an RPC fails. If the coordinator forgets the worker, or
// cannot be reached, as while it restarts, the worker registers again
// under a new ID.
func (w *Worker) Run(ctx context.Context) (err error) {
	defer func() { err = errors.Join(err, w.shuffle.Close()) }()
	for {
		err = w.session(ctx)
		switch {
		case errors.Is(err, errForgotten):
		case status.Code(err) == codes.Unavailable:
			select { // registering waits for the coordinator, but it may fail again at once
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(defaultWait):
			}
		default:
			return err
		}
		w.logger.Warn("registering again", "err", err)
	}
}

// session registers, then runs tasks under that registration, sending
// heartbeats while it does.
func (w *Worker) session(ctx context.Context) error {
	// Wait for the coordinator to come up: workers may start before it.
	reg, err := w.coordinator.RegisterWorker(ctx, &pb.RegisterWorkerRequest{Worker: w.info}, grpc.WaitForReady(true))
	if err != nil {
		return fmt.Errorf("register: %w", err)
	}
	w.stats.registrations.Add(1)
	w.id = reg.GetWorkerId()
	w.logger = slog.Default().With("worker", w.id)
	beating, stop := context.WithCancel(ctx)
	defer stop()
	go w.heartbeat(beating, w.id, reg.GetHeartbeatInterval().AsDuration())

	for {
		resp, err := w.coordinator.RequestTask(ctx, &pb.RequestTaskRequest{WorkerId: w.id})
		if status.Code(err) == codes.NotFound {
			return errForgotten
		}
		if err != nil {
			return fmt.Errorf("request task: %w", err)
		}
		var report *pb.ReportTaskRequest
		switch a := resp.GetAssignment().(type) {
		case *pb.RequestTaskResponse_MapTask:
			log, done := w.attemptLogger(a.MapTask.GetAttempt()), w.stats.start(mapKind)
			outputs, counts, err := w.runMap(a.MapTask, log)
			done(err)
			report = w.report(a.MapTask.GetAttempt(), log, outputs, counts, err)
		case *pb.RequestTaskResponse_ReduceTask:
			log, done := w.attemptLogger(a.ReduceTask.GetAttempt()), w.stats.start(reduceKind)
			counts, err := w.runReduce(ctx, a.ReduceTask, log)
			done(err)
			report = w.report(a.ReduceTask.GetAttempt(), log, nil, counts, err)
		case *pb.RequestTaskResponse_Shutdown:
			return nil
		default:
			wait := defaultWait
			if d := resp.GetWait().GetRetryAfter(); d != nil {
				wait = d.AsDuration()
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
			}
			continue
		}
		if _, err := w.coordinator.ReportTask(ctx, report); err != nil {
			return fmt.Errorf("report task: %w", err)
		}
	}
}

// heartbeat tells the coordinator every interval that worker id is alive,
// until ctx is done. Asking for a task counts as a heartbeat too; these
// cover the time a task runs. A failed heartbeat is not retried: the next
// one, or the next task request, tells the coordinator.
func (w *Worker) heartbeat(ctx context.Context, id string, interval time.Duration) {
	if interval <= 0 {
		return
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			_, _ = w.coordinator.Heartbeat(ctx, &pb.HeartbeatRequest{WorkerId: id})
		}
	}
}

// attemptLogger returns the worker's logger tagged with attempt's job and
// task, for the attempt's user code and the worker's own messages about it.
func (w *Worker) attemptLogger(attempt *pb.TaskAttemptId) *slog.Logger {
	log := w.logger.With("job", attempt.GetTask().GetJobId())
	if id, err := wire.TaskID(attempt); err == nil { // else the attempt fails with err
		log = log.With("task", id)
	}
	return log
}

// runMap runs a map task and returns where its output partitions are.
func (w *Worker) runMap(m *pb.MapTask, log *slog.Logger) ([]*pb.PartitionLocation, map[string]int64, error) {
	spec, err := wire.JobSpec(m.GetJob())
	if err != nil {
		return nil, nil, err
	}
	id, err := wire.TaskID(m.GetAttempt())
	if err != nil {
		return nil, nil, err
	}
	split, err := wire.Split(m.GetSplit())
	if err != nil {
		return nil, nil, err
	}
	dir := filepath.Join(w.workDir, m.GetAttempt().GetTask().GetJobId())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, err
	}
	sizes, counts, err := task.Map(spec, id, split, dir, m.GetSkipRecords(), log)
	if err != nil {
		return nil, nil, err
	}
	locations := make([]*pb.PartitionLocation, len(sizes))
	for r, size := range sizes {
		loc := &pb.PartitionLocation{
			MapIndex: id.Index, Partition: uint32(r), SizeBytes: uint64(size),
			WorkerAddress: w.info.GetShuffleAddress(),
		}
		if loc.WorkerAddress == "" {
			if loc.Uri, err = wire.URI(task.MapOutput(dir, int(id.Index), r)); err != nil {
				return nil, nil, err
			}
		}
		locations[r] = loc
		w.stats.mapOutputBytes.Add(uint64(size))
	}
	return locations, counts, nil
}

// runReduce runs a reduce task over the map outputs it lists.
func (w *Worker) runReduce(ctx context.Context, m *pb.ReduceTask, log *slog.Logger) (counts map[string]int64, err error) {
	spec, err := wire.JobSpec(m.GetJob())
	if err != nil {
		return nil, err
	}
	id, err := wire.TaskID(m.GetAttempt())
	if err != nil {
		return nil, err
	}
	sources := make([]extsort.Records, len(m.GetSources()))
	for i, src := range m.GetSources() {
		var r io.ReadCloser
		if r, err = w.open(ctx, m.GetAttempt().GetTask().GetJobId(), src); err != nil {
			return nil, err
		}
		defer func() { err = errors.Join(err, r.Close()) }()
		sources[i] = kv.NewReader(r)
	}
	return task.Reduce(spec, id, sources, m.GetSkipRecords(), log)
}

// open opens one map output: from the worker that holds it, or from shared
// storage when its location has no worker address.
func (w *Worker) open(ctx context.Context, job string, src *pb.PartitionLocation) (io.ReadCloser, error) {
	if addr := src.GetWorkerAddress(); addr != "" {
		client, err := w.shuffle.Get(addr)
		if err != nil {
			return nil, err
		}
		r := shuffle.NewReader(ctx, client, &pb.FetchPartitionRequest{
			JobId: job, MapIndex: src.GetMapIndex(), Partition: src.GetPartition(),
		})
		return fetch{r, src, &w.stats.fetchedBytes}, nil
	}
	path, err := wire.Path(src.GetUri())
	if err != nil {
		return nil, err
	}
	return os.Open(path)
}

// report builds the report of an attempt that returned outputs and counts,
// or failed with err, which it logs to log.
func (w *Worker) report(id *pb.TaskAttemptId, log *slog.Logger, outputs []*pb.PartitionLocation, counts map[string]int64, err error) *pb.ReportTaskRequest {
	req := &pb.ReportTaskRequest{WorkerId: w.id, Attempt: id}
	if err != nil {
		log.Warn("task failed", "err", err)
		failure := &pb.TaskFailure{Message: err.Error()}
		var lost *lostOutput
		if errors.As(err, &lost) {
			failure.LostOutput = lost.src
		}
		var crash *task.CrashError
		if errors.As(err, &crash) {
			failure.FailedRecord = &crash.Record
		}
		req.Outcome = &pb.ReportTaskRequest_Failure{Failure: failure}
	} else {
		req.Outcome = &pb.ReportTaskRequest_Success{Success: &pb.TaskSuccess{
			Outputs: outputs, Counters: &pb.Counters{Values: counts},
		}}
	}
	return req
}

// fetch reads a map output from the worker that holds it, adding the bytes
// read to fetched. When that worker cannot serve it, the error says which
// output was lost.
type fetch struct {
	*shuffle.Reader
	src     *pb.PartitionLocation
	fetched *atomic.Uint64
}

func (f fetch) Read(p []byte) (int, error) {
	n, err := f.Reader.Read(p)
	f.fetched.Add(uint64(n))
	switch status.Code(err) {
	case codes.Unavailable, codes.NotFound, codes.DeadlineExceeded:
		err = &lostOutput{f.src, err}
	}
	return n, err
}

// lostOutput is the error of a fetch from a worker that is gone or no
// longer has the output (§3.3).
type lostOutput struct {
	src *pb.PartitionLocation
	err error
}

func (e *lostOutput) Error() string {
	return fmt.Sprintf("map %d output at %s: %v", e.src.GetMapIndex(), e.src.GetWorkerAddress(), e.err)
}

func (e *lostOutput) Unwrap() error { return e.err }
