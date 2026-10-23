package coordinator

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	pb "github.com/exios66/mapreduce-reloaded/go/gen/mapreduce/v1"
	"github.com/exios66/mapreduce-reloaded/go/internal/formats"
	"github.com/exios66/mapreduce-reloaded/go/internal/split"
	mrtask "github.com/exios66/mapreduce-reloaded/go/internal/task"
	"github.com/exios66/mapreduce-reloaded/go/internal/wire"
)

// Config configures a Server.
type Config struct {
	// HeartbeatInterval is how often workers must send heartbeats.
	HeartbeatInterval time.Duration
	// WorkerTimeout is how long a worker may go unheard before it is
	// declared dead (§3.3). Zero means never.
	WorkerTimeout time.Duration
	// RetryAfter is how long a worker with nothing to do waits before
	// asking again.
	RetryAfter time.Duration
	// Log, if set, is the write-ahead log: every change to a job or its
	// tasks is appended to it before any reply depends on it (§3.3).
	Log Log
}

// Log is where a coordinator records its decisions. After a failed Append
// it must keep failing, so that a coordinator whose log has fallen behind
// its memory accepts no more changes; *wal.Log does.
type Log interface {
	Append(record []byte) error
}

// Server implements CoordinatorService. It runs one job at a time; a job
// submitted while another runs is rejected.
//
// All state is guarded by mu (ADR 0006). Nothing slow runs under it:
// SubmitJob plans the input splits and checks the output before locking.
type Server struct {
	pb.UnimplementedCoordinatorServiceServer
	cfg Config

	mu       sync.Mutex
	workers  *Registry
	jobs     map[string]*job
	active   *job // the running job, or nil
	stopping bool
}

type job struct {
	id     string
	spec   *pb.JobSpec // sent with every task
	splits []*pb.InputSplit
	tasks  *TaskTable
	counts map[Task]map[string]int64 // of each task's accepted attempt
}

// NewServer returns a coordinator with no workers and no jobs.
func NewServer(cfg Config) *Server {
	return &Server{cfg: cfg, workers: NewRegistry(), jobs: map[string]*job{}}
}

// Shutdown makes every worker's next RequestTask tell it to exit.
func (s *Server) Shutdown() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopping = true
}

// Expire declares dead every worker not heard from within WorkerTimeout of
// now, and returns their IDs. Their running tasks and completed map tasks
// become idle again, since map output lives on the worker's disk; their
// completed reduce tasks stay done, since that output is in the job's
// output directory (§3.3). A dead worker that calls again is told to
// register anew.
func (s *Server) Expire(now time.Time) []string {
	if s.cfg.WorkerTimeout == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	dead := s.workers.Expire(now, s.cfg.WorkerTimeout)
	for _, w := range dead {
		var reset []Task
		if s.active != nil {
			if err := s.record(&pb.LogRecord{Record: &pb.LogRecord_WorkerLost{WorkerLost: &pb.WorkerLost{WorkerId: w}}}); err != nil {
				slog.Error("logging a lost worker", "worker", w, "err", err)
			}
			reset = s.active.tasks.ResetWorker(w)
		}
		slog.Warn("worker lost", "worker", w, "tasks reset", len(reset))
	}
	return dead
}

// Monitor calls Expire every HeartbeatInterval until ctx is done.
func (s *Server) Monitor(ctx context.Context) {
	if s.cfg.WorkerTimeout == 0 || s.cfg.HeartbeatInterval == 0 {
		return
	}
	tick := time.NewTicker(s.cfg.HeartbeatInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tick.C:
			s.Expire(now)
		}
	}
}

func (s *Server) RegisterWorker(_ context.Context, req *pb.RegisterWorkerRequest) (*pb.RegisterWorkerResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &pb.RegisterWorkerResponse{
		WorkerId:          s.workers.Register(req.GetWorker(), time.Now()),
		HeartbeatInterval: durationpb.New(s.cfg.HeartbeatInterval),
	}, nil
}

func (s *Server) Heartbeat(_ context.Context, req *pb.HeartbeatRequest) (*pb.HeartbeatResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &pb.HeartbeatResponse{Reregister: !s.workers.Heartbeat(req.GetWorkerId(), time.Now())}, nil
}

// SubmitJob validates the job, defaulting R to a multiple of the number of
// workers (§3.5), and plans its map tasks.
func (s *Server) SubmitJob(_ context.Context, req *pb.SubmitJobRequest) (*pb.SubmitJobResponse, error) {
	m := proto.CloneOf(req.GetJob())
	if m == nil {
		return nil, status.Error(codes.InvalidArgument, "no job")
	}
	if m.GetNumReduceTasks() == 0 {
		s.mu.Lock()
		m.NumReduceTasks = split.DefaultR(s.workers.Len())
		s.mu.Unlock()
	}
	spec, err := wire.JobSpec(m)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	in, err := formats.Input(spec.InputFormat)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	plan, err := split.Plan(in, spec.Inputs, spec.SplitSize)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if len(plan) == 0 {
		return nil, status.Error(codes.InvalidArgument, "the input is empty")
	}
	if err := mrtask.CheckOutput(spec.Output); err != nil {
		return nil, status.Error(codes.AlreadyExists, err.Error())
	}
	splits := make([]*pb.InputSplit, len(plan))
	for i, sp := range plan {
		if splits[i], err = wire.InputSplit(sp); err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "job %s is still running", s.active.id)
	}
	rec := &pb.JobSubmitted{JobId: fmt.Sprintf("job-%d", len(s.jobs)+1), Job: m, Splits: splits}
	if err := s.record(&pb.LogRecord{Record: &pb.LogRecord_JobSubmitted{JobSubmitted: rec}}); err != nil {
		return nil, err
	}
	s.submit(rec)
	return &pb.SubmitJobResponse{JobId: rec.GetJobId()}, nil
}

// submit makes the job rec describes the active one.
func (s *Server) submit(rec *pb.JobSubmitted) {
	j := &job{
		id:     rec.GetJobId(),
		spec:   rec.GetJob(),
		splits: rec.GetSplits(),
		tasks:  NewTaskTable(len(rec.GetSplits()), int(rec.GetJob().GetNumReduceTasks())),
		counts: map[Task]map[string]int64{},
	}
	s.jobs[j.id], s.active = j, j
}

// RequestTask hands an idle worker its next task: map tasks first, reduce
// tasks once every map task has completed (§3.1).
func (s *Server) RequestTask(_ context.Context, req *pb.RequestTaskRequest) (*pb.RequestTaskResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	worker := req.GetWorkerId()
	if !s.workers.Heartbeat(worker, time.Now()) {
		return nil, status.Errorf(codes.NotFound, "unknown worker %q: register again", worker)
	}
	if s.stopping {
		return &pb.RequestTaskResponse{Assignment: &pb.RequestTaskResponse_Shutdown{Shutdown: &pb.ShutdownInstruction{}}}, nil
	}
	if s.active != nil {
		if t, ok := s.active.tasks.Next(); ok {
			return s.assign(t, worker)
		}
	}
	return &pb.RequestTaskResponse{Assignment: &pb.RequestTaskResponse_Wait{
		Wait: &pb.WaitInstruction{RetryAfter: durationpb.New(s.cfg.RetryAfter)},
	}}, nil
}

// assign starts an attempt at t, a task of the active job, on worker.
func (s *Server) assign(t Task, worker string) (*pb.RequestTaskResponse, error) {
	j := s.active
	attempt, err := j.tasks.Assign(t, worker)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	id := &pb.TaskAttemptId{Task: &pb.TaskId{JobId: j.id, Kind: t.Kind, Index: t.Index}, Attempt: attempt}
	rec := &pb.TaskAssigned{Attempt: id, WorkerId: worker}
	if err := s.record(&pb.LogRecord{Record: &pb.LogRecord_TaskAssigned{TaskAssigned: rec}}); err != nil {
		return nil, err
	}
	if t.Kind == pb.TaskKind_TASK_KIND_MAP {
		return &pb.RequestTaskResponse{Assignment: &pb.RequestTaskResponse_MapTask{
			MapTask: &pb.MapTask{Attempt: id, Job: j.spec, Split: j.splits[t.Index]},
		}}, nil
	}
	return &pb.RequestTaskResponse{Assignment: &pb.RequestTaskResponse_ReduceTask{
		ReduceTask: &pb.ReduceTask{Attempt: id, Job: j.spec, Sources: j.tasks.Sources(t.Index)},
	}}, nil
}

// ReportTask records the outcome of an attempt, from the worker it was
// assigned to. The first successful attempt of a task is accepted, and only
// its counters are kept (§3.3, §4.9).
func (s *Server) ReportTask(_ context.Context, req *pb.ReportTaskRequest) (*pb.ReportTaskResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[req.GetAttempt().GetTask().GetJobId()]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "unknown job %q", req.GetAttempt().GetTask().GetJobId())
	}
	accepted, err := s.report(j, req)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if err := s.record(&pb.LogRecord{Record: &pb.LogRecord_TaskReported{TaskReported: req}}); err != nil {
		return nil, err
	}
	return &pb.ReportTaskResponse{Accepted: accepted}, nil
}

// report applies req to j, and says whether it completed its task.
func (s *Server) report(j *job, req *pb.ReportTaskRequest) (accepted bool, err error) {
	id := req.GetAttempt()
	t := Task{id.GetTask().GetKind(), id.GetTask().GetIndex()}
	if f := req.GetFailure(); f != nil {
		return false, j.tasks.Fail(t, id.GetAttempt(), req.GetWorkerId(), f.GetLostOutput())
	}
	accepted, err = j.tasks.Complete(t, id.GetAttempt(), req.GetWorkerId(), req.GetSuccess().GetOutputs())
	if accepted {
		// A map task run again after its worker died replaces its counts.
		j.counts[t] = req.GetSuccess().GetCounters().GetValues()
		if j.tasks.Done() && s.active == j {
			j.cleanup()
			s.active = nil
		}
	}
	return accepted, err
}

func (s *Server) GetJobStatus(_ context.Context, req *pb.GetJobStatusRequest) (*pb.GetJobStatusResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[req.GetJobId()]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "unknown job %q", req.GetJobId())
	}
	mapsDone, reducesDone := j.tasks.Progress()
	state := pb.JobState_JOB_STATE_MAPPING
	switch {
	case j.tasks.Done():
		state = pb.JobState_JOB_STATE_SUCCEEDED
	case mapsDone == len(j.splits):
		state = pb.JobState_JOB_STATE_REDUCING
	}
	return &pb.GetJobStatusResponse{
		JobId:                j.id,
		State:                state,
		MapTasksTotal:        uint32(len(j.splits)),
		MapTasksCompleted:    uint32(mapsDone),
		ReduceTasksTotal:     j.spec.GetNumReduceTasks(),
		ReduceTasksCompleted: uint32(reducesDone),
		Counters:             &pb.Counters{Values: j.total()},
	}, nil
}

// total sums the counters of every task's accepted attempt.
func (j *job) total() map[string]int64 {
	sum := map[string]int64{}
	for _, counts := range j.counts {
		for name, n := range counts {
			sum[name] += n
		}
	}
	return sum
}

// record appends rec to the log, if there is one. It runs under the lock,
// so no reply reveals a change before the log holds it (ADR 0007).
func (s *Server) record(rec *pb.LogRecord) error {
	if s.cfg.Log == nil {
		return nil
	}
	b, err := proto.Marshal(rec)
	if err == nil {
		err = s.cfg.Log.Append(b)
	}
	if err != nil {
		return status.Errorf(codes.Unavailable, "coordinator log: %v", err)
	}
	return nil
}

// cleanup removes what dead or failed reduce attempts left in the job's
// output. It runs under the lock, once per job, so that the job is never
// reported done before its output is clean; the directory holds at most R
// files. The output is already committed, so an error is only logged.
func (j *job) cleanup() {
	path, err := wire.Path(j.spec.GetOutput())
	if err == nil {
		err = mrtask.Cleanup(path)
	}
	if err != nil {
		slog.Warn("cleaning up job output", "job", j.id, "err", err)
	}
}
