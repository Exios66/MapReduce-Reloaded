// Package worker runs tasks for a coordinator (§3.1): it registers, then
// repeatedly asks for a task, runs it and reports the outcome, until the
// coordinator tells it to shut down.
//
// Map output goes to a directory shared by all workers, and reducers read it
// from there by URI. The shuffle server (#18) will serve it over the network
// instead.
package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	pb "github.com/exios66/mapreduce-reloaded/go/gen/mapreduce/v1"
	"github.com/exios66/mapreduce-reloaded/go/internal/task"
	"github.com/exios66/mapreduce-reloaded/go/internal/wire"
)

// defaultWait is how long to wait when the coordinator sends no assignment
// this worker understands.
const defaultWait = time.Second

// Worker runs tasks for one coordinator.
type Worker struct {
	coordinator pb.CoordinatorServiceClient
	workDir     string // the shared directory for map output
	info        *pb.WorkerInfo
	id          string
	logger      *slog.Logger
}

// New returns a worker that writes map output under workDir.
func New(coordinator pb.CoordinatorServiceClient, workDir string, info *pb.WorkerInfo) *Worker {
	return &Worker{coordinator: coordinator, workDir: workDir, info: info, logger: slog.Default()}
}

// Run registers and runs tasks until the coordinator says to shut down, or
// ctx is done, or an RPC fails.
func (w *Worker) Run(ctx context.Context) error {
	reg, err := w.coordinator.RegisterWorker(ctx, &pb.RegisterWorkerRequest{Worker: w.info})
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
			counts, err := w.runReduce(a.ReduceTask)
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
		uri, err := wire.URI(task.MapOutput(dir, int(id.Index), r))
		if err != nil {
			return nil, nil, err
		}
		locations[r] = &pb.PartitionLocation{MapIndex: id.Index, Partition: uint32(r), SizeBytes: uint64(size), Uri: uri}
	}
	return locations, counts, nil
}

// runReduce runs a reduce task over the map outputs it lists.
func (w *Worker) runReduce(m *pb.ReduceTask) (map[string]int64, error) {
	spec, err := wire.JobSpec(m.GetJob())
	if err != nil {
		return nil, err
	}
	id, err := wire.TaskID(m.GetAttempt())
	if err != nil {
		return nil, err
	}
	paths := make([]string, len(m.GetSources()))
	for i, src := range m.GetSources() {
		if paths[i], err = wire.Path(src.GetUri()); err != nil {
			return nil, err
		}
	}
	sources, closeSources, err := task.OpenSources(paths)
	if err != nil {
		return nil, err
	}
	counts, err := task.Reduce(spec, id, sources)
	return counts, errors.Join(err, closeSources())
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
