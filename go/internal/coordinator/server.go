package coordinator

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

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
	// Backups enables backup attempts: once no task of the current phase is
	// idle, a worker that asks for work gets a second attempt of a task
	// still running elsewhere (§3.6).
	Backups bool
	// Locate, if set, returns the hosts or zones that hold a split, which
	// map tasks then prefer to run on (§3.4). It runs once per split, when
	// the job is submitted.
	Locate func(*pb.InputSplit) []string
	// LocalityWait is how long the job may hold back its map tasks from
	// workers not on a host that holds their split, in the hope that one
	// that is asks first (delay scheduling). Zero means never.
	LocalityWait time.Duration
	// Log, if set, is the write-ahead log: every change to a job or its
	// tasks is appended to it before any reply depends on it (§3.3).
	Log Log
	// KeepJobs is how many ended jobs stay in memory for GetJobStatus and
	// the status page; older ones are forgotten, oldest first. Zero means
	// all of them.
	KeepJobs int
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
	order    []*job       // every job kept, in the order submitted
	seq      int          // jobs submitted, for the next ID; never reused
	active   *job         // the running job, or nil
	lost     []LostWorker // since this process started, for Status
	stopping bool
}

type job struct {
	id     string
	spec   *pb.JobSpec // sent with every task
	splits []*pb.InputSplit
	tasks  *TaskTable
	counts map[Task]map[string]int64 // of each task's accepted attempt
	placed map[Task][]pb.Locality    // of each attempt, by number
	ran    map[Task]pb.Locality      // of each task's accepted attempt
	since  time.Time                 // when the job began holding back tasks, if it has
	err    string                    // why the job failed, if it did

	submitted, finished time.Time // finished is zero while the job runs
}

// NewServer returns a coordinator with no workers and no jobs.
func NewServer(cfg Config) *Server {
	return &Server{cfg: cfg, workers: NewRegistry("w"), jobs: map[string]*job{}}
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
// A zero WorkerTimeout disables expiration. Log failures do not prevent
// workers from expiring or their tasks from being reset.
func (s *Server) Expire(now time.Time) []string {
	if s.cfg.WorkerTimeout == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	dead := s.workers.Expire(now, s.cfg.WorkerTimeout)
	at := time.Now() // now may predate a record logged while we waited for the lock
	for _, w := range dead {
		var reset []Task
		if s.active != nil {
			if err := s.record(&pb.LogRecord{Record: &pb.LogRecord_WorkerLost{WorkerLost: &pb.WorkerLost{WorkerId: w}}}, at); err != nil {
				slog.Error("logging a lost worker", "worker", w, "err", err)
			}
			reset = s.active.tasks.ResetWorker(w)
		}
		s.lost = append(s.lost, LostWorker{ID: w, At: now, Tasks: reset})
		s.lost = s.lost[max(0, len(s.lost)-maxLost):]
		slog.Warn("worker lost", "worker", w, "tasks_reset", len(reset))
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

// Heartbeat tells an unknown worker to register again, and a known one
// which of the attempts it runs to stop: those no longer running on it, such
// as a backup whose twin finished first (§3.6), or those of an ended job.
func (s *Server) Heartbeat(_ context.Context, req *pb.HeartbeatRequest) (*pb.HeartbeatResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	worker := req.GetWorkerId()
	if !s.workers.Heartbeat(worker, time.Now()) {
		return &pb.HeartbeatResponse{Reregister: true}, nil
	}
	resp := &pb.HeartbeatResponse{ActiveJobs: &pb.ActiveJobs{}}
	if s.active != nil {
		resp.ActiveJobs.JobIds = []string{s.active.id}
	}
	for _, p := range req.GetRunning() {
		if id := p.GetAttempt(); !s.runs(id, worker) {
			resp.Cancel = append(resp.Cancel, id)
		}
	}
	return resp, nil
}

// runs reports whether attempt, of the active job, is running on worker.
func (s *Server) runs(attempt *pb.TaskAttemptId, worker string) bool {
	j := s.jobs[attempt.GetTask().GetJobId()]
	t := Task{attempt.GetTask().GetKind(), attempt.GetTask().GetIndex()}
	return j != nil && j == s.active && j.tasks.Runs(t, attempt.GetAttempt(), worker)
}

// SubmitJob validates the job, defaulting R to a multiple of the number of
// workers (§3.5), and plans its map tasks.
// On success it makes the job active and returns its ID. It returns
// InvalidArgument for invalid jobs or input planning failures, AlreadyExists
// if checking the output fails, FailedPrecondition if another job is running
// or output setup fails, and Unavailable if recording the submission fails.
func (s *Server) SubmitJob(ctx context.Context, req *pb.SubmitJobRequest) (*pb.SubmitJobResponse, error) {
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
	plan, err := split.Plan(ctx, in, spec.Inputs, spec.SplitSize)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if len(plan) == 0 {
		return nil, status.Error(codes.InvalidArgument, "the input is empty")
	}
	splits := make([]*pb.InputSplit, len(plan))
	for i, sp := range plan {
		if splits[i], err = wire.InputSplit(sp); err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		if s.cfg.Locate != nil {
			splits[i].Locations = s.cfg.Locate(splits[i])
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "job %s is still running", s.active.id)
	}
	if err := mrtask.CheckOutput(ctx, spec.Output); err != nil { // under the lock, so no job finishes in between
		return nil, status.Error(codes.AlreadyExists, err.Error())
	}
	if err := mrtask.Setup(ctx, spec.Output); err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	rec := &pb.JobSubmitted{JobId: fmt.Sprintf("job-%d", s.seq+1), Job: m, Splits: splits}
	now := time.Now()
	if err := s.record(&pb.LogRecord{Record: &pb.LogRecord_JobSubmitted{JobSubmitted: rec}}, now); err != nil {
		return nil, err
	}
	s.submit(rec, now)
	return &pb.SubmitJobResponse{JobId: rec.GetJobId()}, nil
}

// submit makes the job rec describes, submitted at at, the active one.
func (s *Server) submit(rec *pb.JobSubmitted, at time.Time) {
	j := &job{
		id:        rec.GetJobId(),
		spec:      rec.GetJob(),
		splits:    rec.GetSplits(),
		tasks:     NewTaskTable(len(rec.GetSplits()), int(rec.GetJob().GetNumReduceTasks())),
		counts:    map[Task]map[string]int64{},
		placed:    map[Task][]pb.Locality{},
		ran:       map[Task]pb.Locality{},
		submitted: at,
	}
	s.jobs[j.id], s.active = j, j
	s.order = append(s.order, j)
	s.seq++
	s.forget()
}

// forget drops the oldest ended jobs beyond cfg.KeepJobs. The active job,
// the newest, is never dropped.
func (s *Server) forget() {
	for s.cfg.KeepJobs > 0 && len(s.order) > s.cfg.KeepJobs+1 && s.order[0] != s.active {
		delete(s.jobs, s.order[0].id)
		s.order[0] = nil
		s.order = s.order[1:]
	}
}

// RequestTask hands an idle worker its next task: map tasks first, the
// nearest to their input (§3.4), reduce tasks once every map task has
// completed (§3.1), then backup attempts if enabled (§3.6).
// It returns a wait instruction when no task is ready, or a shutdown
// instruction after Shutdown. Errors are NotFound for an unknown worker,
// Internal if assigning a task fails, or Unavailable if recording it fails.
func (s *Server) RequestTask(_ context.Context, req *pb.RequestTaskRequest) (*pb.RequestTaskResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	worker, now := req.GetWorkerId(), time.Now()
	if !s.workers.Heartbeat(worker, now) {
		return nil, status.Errorf(codes.NotFound, "unknown worker %q: register again", worker)
	}
	if s.stopping {
		return &pb.RequestTaskResponse{Assignment: &pb.RequestTaskResponse_Shutdown{Shutdown: &pb.ShutdownInstruction{}}}, nil
	}
	if s.active != nil {
		info := s.workerInfo(worker)
		t, ok := s.active.tasks.Next(func(i uint32) int { return int(locality(s.active.splits[i], info)) })
		switch {
		case ok && s.holdBack(t, info, now):
			ok = false
		case !ok && s.cfg.Backups:
			t, ok = s.active.tasks.Backup(worker)
		}
		if ok {
			return s.assign(t, worker, now)
		}
	}
	return &pb.RequestTaskResponse{Assignment: &pb.RequestTaskResponse_Wait{
		Wait: &pb.WaitInstruction{RetryAfter: durationpb.New(s.cfg.RetryAfter)},
	}}, nil
}

// assign starts an attempt at t, a task of the active job, on worker at now.
// It returns the map or reduce assignment, or Internal for task table errors
// and Unavailable for log errors. An error does not roll back the attempt.
func (s *Server) assign(t Task, worker string, now time.Time) (*pb.RequestTaskResponse, error) {
	j := s.active
	attempt, err := j.tasks.Assign(t, worker, now)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	skip, err := j.tasks.Skip(t)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	id := &pb.TaskAttemptId{Task: &pb.TaskId{JobId: j.id, Kind: t.Kind, Index: t.Index}, Attempt: attempt}
	rec := &pb.TaskAssigned{Attempt: id, WorkerId: worker}
	if t.Kind == pb.TaskKind_TASK_KIND_MAP {
		rec.Locality = locality(j.splits[t.Index], s.workerInfo(worker))
	}
	j.placed[t] = append(j.placed[t], rec.GetLocality())
	if err := s.record(&pb.LogRecord{Record: &pb.LogRecord_TaskAssigned{TaskAssigned: rec}}, now); err != nil {
		return nil, err
	}
	if t.Kind == pb.TaskKind_TASK_KIND_MAP {
		return &pb.RequestTaskResponse{Assignment: &pb.RequestTaskResponse_MapTask{
			MapTask: &pb.MapTask{Attempt: id, Job: j.spec, Split: j.splits[t.Index], SkipRecords: skip},
		}}, nil
	}
	return &pb.RequestTaskResponse{Assignment: &pb.RequestTaskResponse_ReduceTask{
		ReduceTask: &pb.ReduceTask{Attempt: id, Job: j.spec, Sources: j.tasks.Sources(t.Index), SkipRecords: skip},
	}}, nil
}

// holdBack reports whether a worker should wait rather than run t, the
// nearest task to it, because t is a map task whose split is on other hosts.
// The job holds such tasks back for at most LocalityWait at a time, so that
// none waits for ever for a host that is not there: the wait starts when it
// first holds one back, and ends when it starts one on a host that holds its
// split.
func (s *Server) holdBack(t Task, info *pb.WorkerInfo, now time.Time) bool {
	j := s.active
	if t.Kind != pb.TaskKind_TASK_KIND_MAP || s.cfg.LocalityWait == 0 {
		return false
	}
	switch locality(j.splits[t.Index], info) {
	case pb.Locality_LOCALITY_UNSPECIFIED, pb.Locality_LOCALITY_HOST:
		j.since = time.Time{}
		return false
	}
	if j.since.IsZero() {
		j.since = now
	}
	return now.Sub(j.since) < s.cfg.LocalityWait
}

// workerInfo returns what a live worker said about itself when it
// registered, or nil.
func (s *Server) workerInfo(id string) *pb.WorkerInfo {
	w, _ := s.workers.Lookup(id)
	return w.Info
}

// locality says how close a worker is to a split. A location matches the
// worker's host or its zone; an empty one matches neither.
func locality(split *pb.InputSplit, w *pb.WorkerInfo) pb.Locality {
	at := func(place string) bool { return place != "" && slices.Contains(split.GetLocations(), place) }
	switch {
	case len(split.GetLocations()) == 0:
		return pb.Locality_LOCALITY_UNSPECIFIED
	case at(w.GetHost()):
		return pb.Locality_LOCALITY_HOST
	case at(w.GetZone()):
		return pb.Locality_LOCALITY_ZONE
	}
	return pb.Locality_LOCALITY_REMOTE
}

// ReportTask records the outcome of an attempt, from the worker it was
// assigned to. The first successful attempt of a task is accepted, and only
// its counters are kept (§3.3, §4.9).
// Valid failure reports and stale or duplicate attempts are not accepted.
// It returns NotFound for an unknown job,
// InvalidArgument for a report with no outcome or for task or output
// validation errors, and Unavailable if
// recording the report fails. A log error does not undo the report's effects.
func (s *Server) ReportTask(_ context.Context, req *pb.ReportTaskRequest) (*pb.ReportTaskResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[req.GetAttempt().GetTask().GetJobId()]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "unknown job %q", req.GetAttempt().GetTask().GetJobId())
	}
	if req.GetOutcome() == nil {
		return nil, status.Error(codes.InvalidArgument, "report has neither success nor failure")
	}
	now := time.Now()
	accepted, err := s.report(j, req, now)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if err := s.record(&pb.LogRecord{Record: &pb.LogRecord_TaskReported{TaskReported: req}}, now); err != nil {
		return nil, err
	}
	return &pb.ReportTaskResponse{Accepted: accepted}, nil
}

// report applies req, received at at, to j, and says whether it completed
// its task.
// Failure reports and stale attempts return false; invalid tasks or map
// outputs return an error. If the report ends the active job, at is its
// finish time.
func (s *Server) report(j *job, req *pb.ReportTaskRequest, at time.Time) (accepted bool, err error) {
	id := req.GetAttempt()
	t := Task{id.GetTask().GetKind(), id.GetTask().GetIndex()}
	if f := req.GetFailure(); f != nil {
		err := j.tasks.Fail(t, id.GetAttempt(), req.GetWorkerId(), f)
		if limit := j.spec.MaxSkippedRecords; limit != nil && uint64(j.tasks.Skipped()) > *limit {
			s.end(j, fmt.Sprintf("more than %d bad records to skip", *limit), at)
		}
		return false, err
	}
	accepted, err = j.tasks.Complete(t, id.GetAttempt(), req.GetWorkerId(), req.GetSuccess().GetOutputs())
	if accepted {
		// A map task run again after its worker died replaces its counts.
		j.counts[t] = req.GetSuccess().GetCounters().GetValues()
		j.ran[t] = j.placed[t][id.GetAttempt()]
		if j.tasks.Done() {
			s.end(j, "", at)
		}
	}
	return accepted, err
}

// end ends j at at if it is the active job: it failed with err, or
// succeeded if err is empty. Attempts still running may report, but its
// state no longer changes.
func (s *Server) end(j *job, err string, at time.Time) {
	if s.active != j {
		return
	}
	j.err, j.finished = err, at
	j.cleanup()
	s.active = nil
}

func (s *Server) GetJobStatus(_ context.Context, req *pb.GetJobStatusRequest) (*pb.GetJobStatusResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[req.GetJobId()]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "unknown job %q", req.GetJobId())
	}
	return j.status(), nil
}

// status describes the job's progress.
// Submission and finish timestamps are omitted when unknown; a running
// job has no finish timestamp.
func (j *job) status() *pb.GetJobStatusResponse {
	mapsDone, reducesDone := j.tasks.Progress()
	state := pb.JobState_JOB_STATE_MAPPING
	switch {
	case j.err != "":
		state = pb.JobState_JOB_STATE_FAILED
	case j.tasks.Done():
		state = pb.JobState_JOB_STATE_SUCCEEDED
	case mapsDone == len(j.splits):
		state = pb.JobState_JOB_STATE_REDUCING
	}
	backups, won := j.tasks.Backups()
	near := map[pb.Locality]uint32{}
	for t, l := range j.ran {
		if st, _ := j.tasks.State(t); st == pb.TaskState_TASK_STATE_COMPLETED {
			near[l]++
		}
	}
	return &pb.GetJobStatusResponse{
		JobId:                j.id,
		State:                state,
		MapTasksTotal:        uint32(len(j.splits)),
		MapTasksCompleted:    uint32(mapsDone),
		ReduceTasksTotal:     j.spec.GetNumReduceTasks(),
		ReduceTasksCompleted: uint32(reducesDone),
		Counters:             &pb.Counters{Values: j.total()},
		Error:                j.err,
		MapTasksHostLocal:    near[pb.Locality_LOCALITY_HOST],
		MapTasksZoneLocal:    near[pb.Locality_LOCALITY_ZONE],
		SubmittedAt:          timestamp(j.submitted),
		FinishedAt:           timestamp(j.finished),
		BackupAttempts:       uint32(backups),
		BackupWins:           uint32(won),
	}
}

// timestamp returns t as a Timestamp, or nil if t is zero: a job not yet
// finished, or one replayed from a log written before records had times.
func timestamp(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
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

// record appends rec, a decision made at at, to the log, if there is one.
// It runs under the lock, so no reply reveals a change before the log holds
// it (ADR 0007).
// With a log configured it sets rec.At, returning Unavailable if encoding
// or appending fails. Without a log it returns nil without changing rec.
func (s *Server) record(rec *pb.LogRecord, at time.Time) error {
	if s.cfg.Log == nil {
		return nil
	}
	rec.At = timestamppb.New(at)
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
// files. The job's outcome is already decided, so an error is only logged.
func (j *job) cleanup() {
	path, err := wire.Path(j.spec.GetOutput())
	if err == nil {
		err = mrtask.Cleanup(context.Background(), path)
	}
	if err != nil {
		slog.Warn("cleaning up job output", "job", j.id, "err", err)
	}
}
