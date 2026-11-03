package coordinator

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strconv"
	"strings"
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
	// Speculation chooses how backup attempts are started once no task of
	// the current phase is idle: SpeculationPaper (the paper's rule, §3.6),
	// SpeculationLate (LATE, Zaharia et al., OSDI 2008) or SpeculationOff.
	// Empty means Backups decides: paper if it is set, off if not.
	Speculation string
	// SpeculativeCap, SlowTaskPercentile, SlowNodePercentile and MinRuntime
	// are the LATE settings (see LateParams). A zero field means its
	// default: 0.1 of the workers, the 25th percentile for tasks and for
	// workers, and twice HeartbeatInterval.
	SpeculativeCap     float64
	SlowTaskPercentile float64
	SlowNodePercentile float64
	MinRuntime         time.Duration
	// Locate, if set, returns the hosts or zones that hold a split, which
	// map tasks then prefer to run on (§3.4). It runs once per split, when
	// the job is submitted.
	Locate func(*pb.InputSplit) []string
	// LocalityWait is how long the job may hold back its map tasks from
	// workers not on a host that holds their split, in the hope that one
	// that is asks first (delay scheduling). Zero means never.
	LocalityWait time.Duration
	// IDPrefix begins every worker and job ID the server gives out. A
	// coordinator without a log numbers from 1 again when restarted, so it
	// sets a prefix of its own: a worker still holding the last run's IDs,
	// or files under a job's ID, must not take them for this run's.
	// Recover ignores it, as the log keeps IDs unique.
	IDPrefix string
	// Log, if set, is the write-ahead log: every change to a job or its
	// tasks is appended to it before any reply depends on it (§3.3).
	Log Log
	// KeepJobs is how many ended jobs stay in memory for GetJobStatus and
	// the status page; older ones are forgotten, oldest first. Zero means
	// all of them.
	KeepJobs int
	// MaxTaskFailures fails the job once one task has failed this many
	// times, with the last error (#176). Attempts that could not fetch lost
	// map output do not count. Zero means no limit.
	MaxTaskFailures int
	// Now, if set, replaces time.Now as the coordinator's clock, for tests
	// of a clock that is set back.
	Now func() time.Time
}

// The values of Config.Speculation.
const (
	SpeculationPaper = "paper"
	SpeculationLate  = "late"
	SpeculationOff   = "off"
)

// ValidSpeculation reports whether s is a value of Config.Speculation:
// "", paper, late or off.
func ValidSpeculation(s string) bool {
	return s == "" || s == SpeculationPaper || s == SpeculationLate || s == SpeculationOff
}

// speculation returns how the server starts backup attempts.
func (c Config) speculation() string {
	switch {
	case c.Speculation != "":
		return c.Speculation
	case c.Backups:
		return SpeculationPaper
	}
	return SpeculationOff
}

// lateParams returns the LATE settings, with the defaults of Zaharia et al.
// (OSDI 2008) for the zero fields.
func (c Config) lateParams() LateParams {
	p := LateParams{c.SpeculativeCap, c.SlowTaskPercentile, c.SlowNodePercentile, c.MinRuntime}
	if p.SpeculativeCap == 0 {
		p.SpeculativeCap = 0.1
	}
	if p.SlowTaskPercentile == 0 {
		p.SlowTaskPercentile = 25
	}
	if p.SlowNodePercentile == 0 {
		p.SlowNodePercentile = 25
	}
	if p.MinRuntime == 0 {
		p.MinRuntime = 2 * c.HeartbeatInterval
	}
	return p
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

	mu          sync.Mutex
	workers     *Registry
	jobs        map[string]*job
	order       []*job       // every job kept, in the order submitted
	seq         int          // jobs submitted, for the next ID; never reused
	active      *job         // the running job, or nil
	replayEnded []*job       // jobs whose end Recover replayed, to clean up after
	lost        []LostWorker // since this process started, for Status
	stopping    bool
	logErr      error     // why the log refused a record, if it has
	logPath     string    // where Recover opened the log, to reopen it
	healAt      time.Time // when failed may next try to reopen the log
}

// healEvery is how often a server whose log has failed tries to rebuild
// itself from the log.
const healEvery = time.Second

type job struct {
	id       string
	spec     *pb.JobSpec // sent with every task
	splits   []*pb.InputSplit
	tasks    *TaskTable
	counts   map[Task]map[string]int64 // of each task's accepted attempt
	placed   map[Task][]pb.Locality    // of each attempt, by number
	ran      map[Task]pb.Locality      // of each task's accepted attempt
	since    time.Time                 // when the job began holding back tasks, if it has
	err      string                    // why the job failed, if it did
	attempts []AttemptRange            // of the job, attempted in order by the coordinator's clock

	submitted, finished time.Time // finished is zero while the job runs
}

// NewServer returns a coordinator with no workers and no jobs.
// cfg.IDPrefix is used as supplied for worker and job IDs; numbering
// starts at 1.
func NewServer(cfg Config) *Server {
	return &Server{cfg: cfg, workers: NewRegistry(cfg.IDPrefix + "w"), jobs: map[string]*job{}}
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
	at := s.now() // now may predate a record logged while we waited for the lock
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

// now reads the coordinator's clock: Config.Now, or time.Now.
func (s *Server) now() time.Time {
	if s.cfg.Now != nil {
		return s.cfg.Now()
	}
	return time.Now()
}

// Monitor calls Expire, as of the coordinator's clock, every
// HeartbeatInterval until ctx is done.
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
		case <-tick.C:
			s.Expire(s.now())
		}
	}
}

// RegisterWorker registers a worker and returns its new ID and heartbeat
// interval. It returns Unavailable while a prior log failure remains
// unrecovered.
func (s *Server) RegisterWorker(_ context.Context, req *pb.RegisterWorkerRequest) (*pb.RegisterWorkerResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.failed(); err != nil {
		return nil, err
	}
	id := s.workers.Register(req.GetWorker(), s.now())
	slog.Info("worker registered", "worker", id, "host", req.GetWorker().GetHost(),
		"shuffle_address", req.GetWorker().GetShuffleAddress(), "workers", s.workers.Len())
	return &pb.RegisterWorkerResponse{
		WorkerId:          id,
		HeartbeatInterval: durationpb.New(s.cfg.HeartbeatInterval),
	}, nil
}

// Heartbeat tells an unknown worker to register again, and a known one
// which of the attempts it runs to stop: those no longer running on it, such
// as a backup whose twin finished first (§3.6), or those of an ended job.
// Known workers also receive the active job ID, or an empty list when idle,
// so they can prune ended jobs' map output. It returns Unavailable while a
// prior log failure remains unrecovered.
func (s *Server) Heartbeat(_ context.Context, req *pb.HeartbeatRequest) (*pb.HeartbeatResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.failed(); err != nil {
		return nil, err
	}
	worker := req.GetWorkerId()
	if !s.workers.Heartbeat(worker, s.now()) {
		slog.Info("heartbeat from an unknown worker; told to register again", "worker", worker)
		return &pb.HeartbeatResponse{Reregister: true}, nil
	}
	resp := &pb.HeartbeatResponse{ActiveJobs: &pb.ActiveJobs{}}
	if s.active != nil {
		resp.ActiveJobs.JobIds = []string{s.active.id}
	}
	for _, p := range req.GetRunning() {
		id := p.GetAttempt()
		if !s.runs(id, worker) {
			slog.Debug("attempt told to stop", "worker", worker, "job", id.GetTask().GetJobId(),
				"kind", kindName(id.GetTask().GetKind()), "index", id.GetTask().GetIndex(), "attempt", id.GetAttempt())
			resp.Cancel = append(resp.Cancel, id)
			continue
		}
		t := Task{id.GetTask().GetKind(), id.GetTask().GetIndex()}
		s.active.tasks.SetFraction(t, id.GetAttempt(), worker, p.GetFractionDone(), s.now())
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
// On success it makes the job active and returns its ID, consisting of
// "job-", Config.IDPrefix, and the job sequence number. It returns
// InvalidArgument for invalid jobs or input planning failures, AlreadyExists
// if checking the output fails, FailedPrecondition if another job is running
// or output setup fails, and Unavailable if recording the submission fails
// or a prior log failure remains unrecovered.
func (s *Server) SubmitJob(ctx context.Context, req *pb.SubmitJobRequest) (*pb.SubmitJobResponse, error) {
	s.mu.Lock()
	err := s.failed()
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
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
	plan, err := split.ForSpec(ctx, in, spec)
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
	if err := s.failed(); err != nil {
		return nil, err
	}
	if s.active != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "job %s is still running", s.active.id)
	}
	if err := mrtask.CheckOutput(ctx, spec.Output); err != nil { // under the lock, so no job finishes in between
		return nil, status.Error(codes.AlreadyExists, err.Error())
	}
	if err := mrtask.Setup(ctx, spec.Output); err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	rec := &pb.JobSubmitted{JobId: fmt.Sprintf("job-%s%d", s.cfg.IDPrefix, s.seq+1), Job: m, Splits: splits}
	now := s.now()
	if err := s.record(&pb.LogRecord{Record: &pb.LogRecord_JobSubmitted{JobSubmitted: rec}}, now); err != nil {
		return nil, err
	}
	s.submit(rec, now)
	slog.Info("job submitted", "job", rec.GetJobId(), "program", m.GetProgram(), "inputs", len(m.GetInputs()),
		"output", m.GetOutput(), "map_tasks", len(splits), "reduce_tasks", m.GetNumReduceTasks(),
		"combiner", m.GetUseCombiner(), "workers", s.workers.Len())
	return &pb.SubmitJobResponse{JobId: rec.GetJobId()}, nil
}

// submit makes the job rec describes, submitted at at, the active one,
// advances the job sequence, and forgets old jobs according to cfg.KeepJobs.
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
	// From the ID, not a count: a compacted log no longer holds every job.
	n, err := strconv.Atoi(strings.TrimPrefix(j.id, "job-"))
	if err != nil {
		n = s.seq + 1
	}
	s.seq = max(s.seq+1, n)
	s.forget()
}

// forget drops the oldest ended jobs beyond cfg.KeepJobs, and their
// records from the log. The active job, the newest, is never dropped.
// It is called with a newly active job; a nonpositive limit retains all jobs.
func (s *Server) forget() {
	limit := s.cfg.KeepJobs
	if s.active != nil {
		limit++
	}
	dropped := false
	for s.cfg.KeepJobs > 0 && len(s.order) > limit && s.order[0] != s.active {
		delete(s.jobs, s.order[0].id)
		s.order[0] = nil
		s.order = s.order[1:]
		dropped = true
	}
	if dropped {
		s.compact()
	}
}

// compacter is a Log that can drop records; *wal.Log is one.
type compacter interface {
	Compact(keep func(record []byte) (bool, error)) error
}

// compact rewrites the log with the records of the jobs still kept, so its
// size follows KeepJobs rather than every job ever run (#178). It keeps
// every Restarted record, which Recover counts to name workers, and a
// WorkerLost record only if the job submitted before it is kept: jobs run
// one at a time, so that is the job it applied to, if any. Replaying the
// result rebuilds the same jobs. A failure is logged; the log is either
// unchanged or, if the failure came after its replacement, refuses
// further records, which stops the coordinator taking new decisions.
func (s *Server) compact() {
	c, ok := s.cfg.Log.(compacter)
	if !ok || s.cfg.KeepJobs <= 0 {
		return
	}
	kept := func(job string) bool { return s.jobs[job] != nil }
	last := ""
	err := c.Compact(func(b []byte) (bool, error) {
		rec := &pb.LogRecord{}
		if err := proto.Unmarshal(b, rec); err != nil {
			return false, err
		}
		switch r := rec.GetRecord().(type) {
		case *pb.LogRecord_JobSubmitted:
			last = r.JobSubmitted.GetJobId()
			return kept(last), nil
		case *pb.LogRecord_TaskAssigned:
			return kept(r.TaskAssigned.GetAttempt().GetTask().GetJobId()), nil
		case *pb.LogRecord_TaskReported:
			return kept(r.TaskReported.GetAttempt().GetTask().GetJobId()), nil
		case *pb.LogRecord_WorkerLost:
			return kept(last), nil
		default: // Restarted, and anything newer: keep what is not understood
			return true, nil
		}
	})
	if err != nil {
		slog.Error("compacting the coordinator log", "err", err)
	}
}

// RequestTask hands an idle worker its next task: map tasks first, the
// nearest to their input (§3.4), reduce tasks once every map task has
// completed (§3.1), then backup attempts if enabled (§3.6, or LATE).
// It returns a wait instruction when no task is ready, or a shutdown
// instruction after Shutdown. Errors are NotFound for an unknown worker,
// Internal if assigning a task fails, or Unavailable if recording it fails
// or a prior log failure remains unrecovered.
func (s *Server) RequestTask(_ context.Context, req *pb.RequestTaskRequest) (*pb.RequestTaskResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.failed(); err != nil {
		return nil, err
	}
	worker, now := req.GetWorkerId(), s.now()
	if !s.workers.Heartbeat(worker, now) {
		return nil, status.Errorf(codes.NotFound, "unknown worker %q: register again", worker)
	}
	if s.stopping {
		return &pb.RequestTaskResponse{Assignment: &pb.RequestTaskResponse_Shutdown{Shutdown: &pb.ShutdownInstruction{}}}, nil
	}
	if s.active != nil && !req.GetPaused() {
		info := s.workerInfo(worker)
		t, ok := s.active.tasks.Next(func(i uint32) int { return int(locality(s.active.splits[i], info)) })
		backup := false
		switch {
		case ok && s.holdBack(t, info, now):
			ok = false
		case !ok && s.cfg.speculation() == SpeculationPaper:
			t, ok = s.active.tasks.Backup(worker)
			backup = ok
		case !ok && s.cfg.speculation() == SpeculationLate:
			t, ok = s.active.tasks.LateBackup(worker, now, s.cfg.lateParams(), s.workers.Len())
			backup = ok
		}
		if ok {
			return s.assign(t, worker, now, backup)
		}
	}
	return &pb.RequestTaskResponse{Assignment: &pb.RequestTaskResponse_Wait{
		Wait: &pb.WaitInstruction{RetryAfter: durationpb.New(s.cfg.RetryAfter)},
	}}, nil
}

// assign starts an attempt at t, a task of the active job, on worker at now;
// backup says it duplicates an attempt still running. It returns the map or reduce assignment, or Internal for task table errors
// and Unavailable for log errors. An error does not roll back the attempt.
func (s *Server) assign(t Task, worker string, now time.Time, backup bool) (*pb.RequestTaskResponse, error) {
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
	j.attempt(t, attempt, worker, now, backup)
	if err := s.record(&pb.LogRecord{Record: &pb.LogRecord_TaskAssigned{TaskAssigned: rec}}, now); err != nil {
		return nil, err
	}
	slog.Info("task assigned", "job", j.id, "kind", kindName(t.Kind), "index", t.Index, "attempt", attempt,
		"worker", worker, "backup", backup, "locality", rec.GetLocality())
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
// validation errors, and Unavailable if recording the report fails or a
// prior log failure remains unrecovered. A log error does not undo the
// report's effects, but a report that ends the job cleans up its output
// only once the log holds it: a coordinator that dies in between replays
// a running job, whose reduce attempts still need the job's TempDir (#182).
func (s *Server) ReportTask(_ context.Context, req *pb.ReportTaskRequest) (*pb.ReportTaskResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.failed(); err != nil {
		return nil, err
	}
	j, ok := s.jobs[req.GetAttempt().GetTask().GetJobId()]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "unknown job %q", req.GetAttempt().GetTask().GetJobId())
	}
	if req.GetOutcome() == nil {
		return nil, status.Error(codes.InvalidArgument, "report has neither success nor failure")
	}
	now := s.now()
	accepted, ended, err := s.report(j, req, now)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if err := s.record(&pb.LogRecord{Record: &pb.LogRecord_TaskReported{TaskReported: req}}, now); err != nil {
		return nil, err
	}
	logReport(j, req, accepted, ended)
	if ended {
		j.cleanup()
	}
	return &pb.ReportTaskResponse{Accepted: accepted}, nil
}

// kindName is a task kind's name as the logs show it: map or reduce.
func kindName(k pb.TaskKind) string {
	return strings.ToLower(strings.TrimPrefix(k.String(), "TASK_KIND_"))
}

// logReport logs req, a worker's report on an attempt of j, and, if it ended
// j, how j ended. accepted says the report completed its task, which a
// failure or a slower twin of a finished task does not. ReportTask calls it
// and Recover does not, so replaying the log repeats no event.
func logReport(j *job, req *pb.ReportTaskRequest, accepted, ended bool) {
	id := req.GetAttempt()
	attrs := []any{"job", j.id, "kind", kindName(id.GetTask().GetKind()), "index", id.GetTask().GetIndex(),
		"attempt", id.GetAttempt(), "worker", req.GetWorkerId()}
	if f := req.GetFailure(); f != nil {
		slog.Warn("task attempt failed", append(attrs, "err", f.GetMessage(), "fatal", f.GetFatal(),
			"lost_output", f.GetLostOutput() != nil, "bad_record", f.FailedRecord != nil)...)
	} else {
		slog.Info("task attempt finished", append(attrs, "accepted", accepted)...)
	}
	if !ended {
		return
	}
	secs := j.finished.Sub(j.submitted).Seconds()
	if j.err != "" {
		slog.Error("job failed", "job", j.id, "err", j.err, "seconds", secs)
		return
	}
	slog.Info("job succeeded", "job", j.id, "seconds", secs)
}

// report applies req, received at at, to j, and says whether it completed
// its task and whether it ended the job, whose output the caller then
// cleans up.
// Failure reports, stale attempts and reports for an ended job return
// false, and a report for an ended job changes nothing; invalid tasks or
// map outputs return an error. If the report ends the active job, at is its
// finish time.
func (s *Server) report(j *job, req *pb.ReportTaskRequest, at time.Time) (accepted, ended bool, err error) {
	if s.active != j { // an ended job's outcome and progress are final
		return false, false, nil
	}
	id := req.GetAttempt()
	t := Task{id.GetTask().GetKind(), id.GetTask().GetIndex()}
	if f := req.GetFailure(); f != nil {
		counted := j.tasks.Runs(t, id.GetAttempt(), req.GetWorkerId())
		if counted { // a stale report changes nothing; an ended job's rows stay
			j.attemptEnded(t, id.GetAttempt(), at, false)
		}
		err := j.tasks.Fail(t, id.GetAttempt(), req.GetWorkerId(), f)
		n := j.tasks.Failures(t)
		switch limit := j.spec.MaxSkippedRecords; {
		case !counted || err != nil: // a stale or invalid report changes nothing
		case f.GetFatal():
			s.end(j, fmt.Sprintf("%v cannot run: %s", t, f.GetMessage()), at)
		case s.cfg.MaxTaskFailures > 0 && n >= s.cfg.MaxTaskFailures && f.GetLostOutput() == nil:
			s.end(j, fmt.Sprintf("%v failed %d times; the last error: %s", t, n, f.GetMessage()), at)
		case limit != nil && uint64(j.tasks.Skipped()) > *limit:
			s.end(j, fmt.Sprintf("more than %d bad records to skip", *limit), at)
		}
		return false, s.active != j, err
	}
	accepted, err = j.tasks.CompleteAt(t, id.GetAttempt(), req.GetWorkerId(), req.GetSuccess().GetOutputs(), at)
	if accepted {
		j.attemptEnded(t, id.GetAttempt(), at, true)
		// A map task run again after its worker died replaces its counts.
		j.counts[t] = req.GetSuccess().GetCounters().GetValues()
		j.ran[t] = j.placed[t][id.GetAttempt()]
		if j.tasks.Done() {
			s.end(j, "", at)
		}
	}
	return accepted, s.active != j, err
}

// end ends j at at if it is the active job: it failed with err, or
// succeeded if err is empty. Attempts still running may report, but its
// state no longer changes. The caller cleans up j's output.
func (s *Server) end(j *job, err string, at time.Time) {
	if s.active != j {
		return
	}
	j.err, j.finished = err, at
	s.active = nil
	s.forget()
}

// GetJobStatus returns a retained job's state, progress, and counters.
// It returns NotFound for an unknown or forgotten job, or Unavailable
// while a prior log failure remains unrecovered.
func (s *Server) GetJobStatus(_ context.Context, req *pb.GetJobStatusRequest) (*pb.GetJobStatusResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.failed(); err != nil {
		return nil, err
	}
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
// A failure prevents later RPCs from proceeding until recovery succeeds.
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
		s.logErr = err
		return s.logFailure()
	}
	return nil
}

// failed returns Unavailable while the log refuses records. The memory may
// then hold a change the log does not, such as a job ended by a report that
// could not be logged, so no RPC may act on it. A server from Recover
// instead rebuilds itself from the log, as a restart would, once the log
// takes records again: a full disk stops the cluster only until space is
// freed. It returns nil if the log has not failed or recovery succeeds.
// After a recovery failure, it retries no sooner than healEvery later on
// the coordinator's clock. Without a log path, a restart is required.
// Call it under the lock.
func (s *Server) failed() error {
	if s.logErr == nil {
		return nil
	}
	if s.logPath != "" && !s.now().Before(s.healAt) {
		if err := s.heal(); err == nil {
			return nil
		} else {
			s.logErr = err
		}
		s.healAt = s.now().Add(healEvery)
	}
	return s.logFailure()
}

// logFailure describes the log error without trying to recover, so a failed
// append still fails its RPC even if the log can be reopened. Call it under
// the lock.
func (s *Server) logFailure() error {
	if s.logPath == "" {
		return status.Errorf(codes.Unavailable, "coordinator log failed; restart the coordinator: %v", s.logErr)
	}
	return status.Errorf(codes.Unavailable, "coordinator log failed, retrying: %v", s.logErr)
}

// heal replaces the server's jobs and workers with those recovered from
// its log, dropping every change the log never took. Running attempts stop
// and workers register again, as after a restart. Call it under the lock.
// It replaces the log and clears the failure on success. Recover errors
// propagate; errors closing the old log are ignored.
func (s *Server) heal() error {
	if c, ok := s.cfg.Log.(io.Closer); ok {
		_ = c.Close() // it has already failed; reopening it is what matters
	}
	cfg := s.cfg
	cfg.Log = nil
	n, _, err := Recover(cfg, s.logPath)
	if err != nil {
		return err
	}
	slog.Info("coordinator log works again; recovered from it", "path", s.logPath)
	s.cfg.Log = n.cfg.Log
	s.workers, s.jobs, s.order, s.seq, s.active = n.workers, n.jobs, n.order, n.seq, n.active
	s.logErr = nil
	return nil
}

// Close closes the log the server writes to, if it can be closed. Use it
// rather than the log Recover returned, which a healed server has replaced.
// It returns any close error, or nil when there is no closable log.
func (s *Server) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.cfg.Log.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

// cleanup removes what dead or failed reduce attempts left in the job's
// output. It runs under the lock, once the log holds the job's end, so that
// the job is never reported done before its output is clean; the directory holds at most R
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
