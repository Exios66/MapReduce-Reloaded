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
	"time"

	"google.golang.org/grpc"

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
}

// New returns a worker that writes map output under workDir. If
// info.ShuffleAddress is set, the caller serves workDir there with a
// shuffle.Server.
func New(coordinator pb.CoordinatorServiceClient, workDir string, info *pb.WorkerInfo) *Worker {
	return &Worker{coordinator: coordinator, workDir: workDir, info: info, logger: slog.Default()}
}

// Run registers and runs tasks until the coordinator says to shut down, or
// ctx is done, or an RPC fails.
func (w *Worker) Run(ctx context.Context) (err error) {
	defer func() { err = errors.Join(err, w.shuffle.Close()) }()
	// Wait for the coordinator to come up: workers may start before it.
	reg, err := w.coordinator.RegisterWorker(ctx, &pb.RegisterWorkerRequest{Worker: w.info}, grpc.WaitForReady(true))
	if err != nil {
		return fmt.Errorf("register: %w", err)
	}
	w.id = reg.GetWorkerId()
	w.logger = slog.Default().With("worker", w.id)

	for {
		resp, err := w.coordinator.RequestTask(ctx, &pb.RequestTaskRequest{WorkerId: w.id})
		if err != nil {
			return fmt.Errorf("request task: %w", err)
		}
		var report *pb.ReportTaskRequest
		switch a := resp.GetAssignment().(type) {
		case *pb.RequestTaskResponse_MapTask:
			outputs, counts, err := w.runMap(a.MapTask)
			report = w.report(a.MapTask.GetAttempt(), outputs, counts, err)
		case *pb.RequestTaskResponse_ReduceTask:
			counts, err := w.runReduce(ctx, a.ReduceTask)
			report = w.report(a.ReduceTask.GetAttempt(), nil, counts, err)
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

// runMap runs a map task and returns where its output partitions are.
func (w *Worker) runMap(m *pb.MapTask) ([]*pb.PartitionLocation, map[string]int64, error) {
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
	sizes, counts, err := task.Map(spec, id, split, dir)
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
	}
	return locations, counts, nil
}

// runReduce runs a reduce task over the map outputs it lists.
func (w *Worker) runReduce(ctx context.Context, m *pb.ReduceTask) (counts map[string]int64, err error) {
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
	return task.Reduce(spec, id, sources)
}

// open opens one map output: from the worker that holds it, or from shared
// storage when its location has no worker address.
func (w *Worker) open(ctx context.Context, job string, src *pb.PartitionLocation) (io.ReadCloser, error) {
	if addr := src.GetWorkerAddress(); addr != "" {
		client, err := w.shuffle.Get(addr)
		if err != nil {
			return nil, err
		}
		return shuffle.NewReader(ctx, client, &pb.FetchPartitionRequest{
			JobId: job, MapIndex: src.GetMapIndex(), Partition: src.GetPartition(),
		}), nil
	}
	path, err := wire.Path(src.GetUri())
	if err != nil {
		return nil, err
	}
	return os.Open(path)
}

// report builds the report of an attempt that returned outputs and counts,
// or failed with err.
func (w *Worker) report(id *pb.TaskAttemptId, outputs []*pb.PartitionLocation, counts map[string]int64, err error) *pb.ReportTaskRequest {
	req := &pb.ReportTaskRequest{WorkerId: w.id, Attempt: id}
	if err != nil {
		w.logger.Warn("task failed", "task", id.GetTask(), "attempt", id.GetAttempt(), "err", err)
		req.Outcome = &pb.ReportTaskRequest_Failure{Failure: &pb.TaskFailure{Message: err.Error()}}
	} else {
		req.Outcome = &pb.ReportTaskRequest_Success{Success: &pb.TaskSuccess{
			Outputs: outputs, Counters: &pb.Counters{Values: counts},
		}}
	}
	return req
}
