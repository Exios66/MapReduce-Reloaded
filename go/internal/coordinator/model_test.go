package coordinator

import (
	"flag"
	"fmt"
	"maps"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	pb "github.com/exios66/mapreduce-reloaded/go/gen/mapreduce/v1"
)

var (
	modelRuns = flag.Uint64("model.runs", 15, "random call sequences TestModel runs")
	modelSeed = flag.Uint64("model.seed", 0, "seed of TestModel's first sequence; the failure message names it")
)

// attempt is one assignment the model has seen.
type attempt struct {
	id     *pb.TaskAttemptId
	worker string
}

func (a attempt) task() Task { return Task{a.id.GetTask().GetKind(), a.id.GetTask().GetIndex()} }

// model is what the server should hold for the running job (#154).
type model struct {
	job      string
	m, r     int
	ended    bool // succeeded or failed
	failed   bool // a fatal failure ended it
	done     map[Task]bool
	doneBy   map[Task]string           // the worker whose map output is used
	counts   map[Task]map[string]int64 // of each task's last accepted attempt
	live     map[string]attempt        // attempts that may still be accepted, by key
	issued   map[string]bool           // every attempt number ever handed out, by key
	reports  []*pb.ReportTaskRequest   // every report sent, to send again
	workers  []string
	reported map[string]bool    // attempts already reported, by key
	given    map[string]attempt // attempts given up, by key, so a late report can name their worker
}

func key(id *pb.TaskAttemptId) string {
	return fmt.Sprintf("%s/%v/%d/%d", id.GetTask().GetJobId(), id.GetTask().GetKind(), id.GetTask().GetIndex(), id.GetAttempt())
}

func (md *model) mapsDone() bool {
	for i := range md.m {
		if !md.done[Task{mapKind, uint32(i)}] {
			return false
		}
	}
	return true
}

func (md *model) total() map[string]int64 {
	sum := map[string]int64{}
	for _, c := range md.counts {
		for k, v := range c {
			sum[k] += v
		}
	}
	return sum
}

// TestModel drives the coordinator's RPCs with random calls from several
// workers, against a model of the job: duplicate, late and stale reports,
// failures with and without a bad record, lost map output, workers that
// expire, jobs of different shapes, and restarts from the log. After each
// call it checks the reply and the job's status against the model, and
// after each restart that the recovered state equals the state before it.
func TestModel(t *testing.T) {
	for i := range *modelRuns {
		seed := *modelSeed + i
		if !t.Run(fmt.Sprint(seed), func(t *testing.T) { runModel(t, seed) }) {
			t.Fatalf("seed %d failed; replay it with -model.seed=%d -model.runs=1", seed, seed)
		}
	}
}

func runModel(t *testing.T, seed uint64) {
	rng := rand.New(rand.NewPCG(seed, 1))
	dir := t.TempDir()
	logPath := filepath.Join(dir, "coordinator.log")
	located := 0 // splits so far, which go to host-0, host-1 and host-2 in turn
	cfg := Config{Backups: true, WorkerTimeout: time.Hour, RetryAfter: time.Second,
		// so that the status counts local and remote assignments, which a
		// restart must keep too
		Locate: func(*pb.InputSplit) []string {
			located++
			return []string{fmt.Sprint("host-", located%3)}
		},
	}
	s, log, err := Recover(cfg, logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = log.Close() }()

	var md *model
	var workers []string // carried from one job to the next
	jobs := 0
	fail := func(format string, args ...any) {
		t.Helper()
		t.Fatalf("seed %d: %s", seed, fmt.Sprintf(format, args...))
	}
	submit := func() {
		jobs++
		lines := 1 + rng.IntN(8)
		in := filepath.Join(dir, fmt.Sprintf("in-%d.txt", jobs))
		if err := os.WriteFile(in, []byte(strings.Repeat("a b\n", lines)), 0o644); err != nil {
			t.Fatal(err)
		}
		r := 1 + rng.IntN(4)
		sub, err := s.SubmitJob(ctx, &pb.SubmitJobRequest{Job: &pb.JobSpec{
			Program: "wordcount", Inputs: []string{in}, Output: filepath.Join(dir, fmt.Sprintf("out-%d", jobs)),
			NumReduceTasks: uint32(r), SplitSizeBytes: uint64(1 + rng.IntN(16)),
		}})
		if err != nil {
			fail("submit: %v", err)
		}
		st, _ := s.GetJobStatus(ctx, &pb.GetJobStatusRequest{JobId: sub.GetJobId()})
		md = &model{
			job: sub.GetJobId(), m: int(st.GetMapTasksTotal()), r: r,
			done: map[Task]bool{}, doneBy: map[Task]string{}, counts: map[Task]map[string]int64{},
			live: map[string]attempt{}, issued: map[string]bool{}, reported: map[string]bool{},
			given: map[string]attempt{},
		}
		md.workers = workers
	}
	submit()

	report := func(req *pb.ReportTaskRequest, a attempt, isNew bool) {
		k := key(req.GetAttempt())
		_, isLive := md.live[k]
		// An attempt whose task a twin completed, or whose job ended, no
		// longer runs: nothing it reports changes the job.
		running := isLive && !md.done[a.task()] && !md.ended
		want := req.GetSuccess() != nil && running
		resp, err := s.ReportTask(ctx, req)
		if err != nil {
			fail("report %s: %v", k, err)
		}
		if resp.GetAccepted() != want {
			fail("report %s (success %v, live %v, done %v, ended %v): accepted %v, want %v",
				k, req.GetSuccess() != nil, isLive, md.done[a.task()], md.ended, resp.GetAccepted(), want)
		}
		if isNew {
			md.reports = append(md.reports, req)
			md.reported[k] = true
		}
		delete(md.live, k)
		if !running {
			return
		}
		switch {
		case resp.GetAccepted():
			md.done[a.task()] = true
			md.counts[a.task()] = req.GetSuccess().GetCounters().GetValues()
			if a.task().Kind == mapKind {
				md.doneBy[a.task()] = a.worker
			}
		case req.GetFailure().GetFatal():
			md.ended, md.failed = true, true
		case req.GetFailure().GetLostOutput() != nil:
			lost := Task{mapKind, req.GetFailure().GetLostOutput().GetMapIndex()}
			if md.done[lost] && md.doneBy[lost] == req.GetFailure().GetLostOutput().GetWorkerAddress() {
				md.done[lost] = false
			}
		}
	}

	for step := range 150 {
		if md.ended {
			workers = md.workers
			submit()
		}
		switch op := rng.IntN(20); {
		case op < 2 || len(md.workers) == 0: // a worker joins
			host := fmt.Sprint("host-", rng.IntN(4)) // host-3 holds no split
			reg, err := s.RegisterWorker(ctx, &pb.RegisterWorkerRequest{Worker: &pb.WorkerInfo{Host: host}})
			if err != nil {
				t.Fatal(err)
			}
			md.workers = append(md.workers, reg.GetWorkerId())

		case op < 8: // a worker asks for a task
			w := md.workers[rng.IntN(len(md.workers))]
			resp, err := s.RequestTask(ctx, &pb.RequestTaskRequest{WorkerId: w})
			if err != nil { // NotFound too: the model drops workers when the coordinator does
				fail("step %d: request: %v", step, err)
			}
			id := resp.GetMapTask().GetAttempt()
			if id == nil {
				id = resp.GetReduceTask().GetAttempt()
			}
			if id == nil {
				continue
			}
			a := attempt{id, w}
			if a.task().Kind == reduceKind && !md.mapsDone() {
				fail("step %d: %v offered before every map task completed", step, a.task())
			}
			if md.done[a.task()] {
				fail("step %d: completed %v offered again", step, a.task())
			}
			if md.issued[key(id)] {
				fail("step %d: attempt %s handed out twice", step, key(id))
			}
			md.issued[key(id)], md.live[key(id)] = true, a

		case op < 13: // an attempt reports
			var keys []string
			for k := range md.live {
				keys = append(keys, k)
			}
			for _, k := range slices.Sorted(maps.Keys(md.issued)) {
				if !md.reported[k] && md.live[k].id == nil && rng.IntN(4) == 0 {
					keys = append(keys, k) // a late report of an attempt given up
				}
			}
			if len(keys) == 0 {
				continue
			}
			slices.Sort(keys)
			k := keys[rng.IntN(len(keys))]
			a, ok := md.live[k]
			if !ok {
				a = md.stale(k)
			}
			req := &pb.ReportTaskRequest{WorkerId: a.worker, Attempt: a.id}
			switch f := rng.IntN(10); {
			case f < 6:
				var outs []*pb.PartitionLocation
				if a.task().Kind == mapKind {
					for p := range md.r {
						outs = append(outs, &pb.PartitionLocation{MapIndex: a.task().Index, Partition: uint32(p), Uri: "file:///m", WorkerAddress: a.worker})
					}
				}
				req.Outcome = &pb.ReportTaskRequest_Success{Success: &pb.TaskSuccess{
					Outputs: outs, Counters: &pb.Counters{Values: map[string]int64{"n": int64(rng.IntN(100))}},
				}}
			case f < 8:
				failure := &pb.TaskFailure{Message: "crash"}
				if rng.IntN(2) == 0 {
					failure.FailedRecord = proto.Uint64(uint64(rng.IntN(3)))
				}
				req.Outcome = &pb.ReportTaskRequest_Failure{Failure: failure}
			case f < 9 && a.task().Kind == reduceKind:
				lost := uint32(rng.IntN(md.m))
				req.Outcome = &pb.ReportTaskRequest_Failure{Failure: &pb.TaskFailure{Message: "fetch", LostOutput: &pb.PartitionLocation{
					MapIndex: lost, Partition: a.task().Index, WorkerAddress: md.doneBy[Task{mapKind, lost}],
				}}}
			case rng.IntN(8) == 0:
				req.Outcome = &pb.ReportTaskRequest_Failure{Failure: &pb.TaskFailure{Message: "bad job", Fatal: true}}
			default:
				req.Outcome = &pb.ReportTaskRequest_Failure{Failure: &pb.TaskFailure{Message: "crash"}}
			}
			report(req, a, true)

		case op < 15: // a report arrives twice
			if len(md.reports) == 0 {
				continue
			}
			req := md.reports[rng.IntN(len(md.reports))]
			report(req, attempt{req.GetAttempt(), req.GetWorkerId()}, false)

		case op < 17: // a heartbeat: the server cancels exactly the attempts given up
			w := md.workers[rng.IntN(len(md.workers))]
			var running []*pb.TaskProgress
			want := map[string]bool{}
			for _, k := range slices.Sorted(maps.Keys(md.issued)) {
				a, ok := md.live[k]
				if ok && a.worker != w {
					continue
				}
				if !ok {
					if a = md.stale(k); a.worker != w || md.reported[k] {
						continue
					}
				}
				running = append(running, &pb.TaskProgress{Attempt: a.id})
				want[k] = !ok || md.ended || md.done[a.task()] // a twin that won stops it too
			}
			resp, err := s.Heartbeat(ctx, &pb.HeartbeatRequest{WorkerId: w, Running: running})
			if err != nil {
				t.Fatal(err)
			}
			if resp.GetReregister() {
				fail("step %d: heartbeat from %s: told to reregister, but it is registered", step, w)
			}
			got := map[string]bool{}
			for _, id := range resp.GetCancel() {
				got[key(id)] = true
			}
			for k, cancel := range want {
				if got[k] != cancel {
					fail("step %d: heartbeat from %s: cancel %s is %v, want %v", step, w, k, got[k], cancel)
				}
			}

		case op < 18: // every worker expires (a partition)
			s.Expire(time.Now().Add(2 * time.Hour))
			for k, a := range md.live {
				md.stash(k, a)
			}
			clear(md.live)
			for task, w := range md.doneBy {
				if slices.Contains(md.workers, w) && md.done[task] && !md.ended {
					md.done[task] = false
				}
			}
			md.workers = nil

		default: // the coordinator restarts from its log
			before := snapshot(t, s, md.job)
			if err := log.Close(); err != nil {
				t.Fatal(err)
			}
			if s, log, err = Recover(cfg, logPath); err != nil {
				fail("step %d: recover: %v", step, err)
			}
			if after := snapshot(t, s, md.job); !proto.Equal(before.status, after.status) || !maps.EqualFunc(before.skip, after.skip, slices.Equal) {
				fail("step %d: after a restart\n%v\n%v\nwant\n%v\n%v", step, after.status, after.skip, before.status, before.skip)
			}
			for k, a := range md.live {
				md.stash(k, a)
			}
			clear(md.live)
			md.workers = nil
		}
		md.check(t, seed, step, s)
	}
}

func (md *model) stash(k string, a attempt) { md.given[k] = a }

func (md *model) stale(k string) attempt { return md.given[k] }

// check compares the job's status with the model.
func (md *model) check(t *testing.T, seed uint64, step int, s *Server) {
	t.Helper()
	st, err := s.GetJobStatus(ctx, &pb.GetJobStatusRequest{JobId: md.job})
	if err != nil {
		t.Fatalf("seed %d step %d: %v", seed, step, err)
	}
	var maps, reduces uint32
	for task, done := range md.done {
		if done && task.Kind == mapKind {
			maps++
		} else if done {
			reduces++
		}
	}
	if st.GetMapTasksCompleted() != maps || st.GetReduceTasksCompleted() != reduces {
		t.Fatalf("seed %d step %d: status says %d maps and %d reduces done, the model %d and %d",
			seed, step, st.GetMapTasksCompleted(), st.GetReduceTasksCompleted(), maps, reduces)
	}
	if got, want := st.GetCounters().GetValues(), md.total(); !sameCounts(got, want) {
		t.Fatalf("seed %d step %d: counters %v, want %v", seed, step, got, want)
	}
	succeeded := int(maps) == md.m && int(reduces) == md.r
	state := st.GetState()
	ok := state != pb.JobState_JOB_STATE_SUCCEEDED && state != pb.JobState_JOB_STATE_FAILED
	switch {
	case md.failed:
		ok = state == pb.JobState_JOB_STATE_FAILED
	case succeeded:
		ok = state == pb.JobState_JOB_STATE_SUCCEEDED
		md.ended = true
	case int(maps) == md.m:
		ok = state == pb.JobState_JOB_STATE_REDUCING
	default:
		ok = state == pb.JobState_JOB_STATE_MAPPING
	}
	if !ok {
		t.Fatalf("seed %d step %d: state %v with %d/%d maps and %d/%d reduces done, failed %v",
			seed, step, state, maps, md.m, reduces, md.r, md.failed)
	}
}

// sameCounts compares counters, a missing one counting as 0.
func sameCounts(a, b map[string]int64) bool {
	for k, v := range b {
		if v != 0 && a[k] != v {
			return false
		}
	}
	for k, v := range a {
		if v != 0 && b[k] != v {
			return false
		}
	}
	return true
}

type state struct {
	status *pb.GetJobStatusResponse
	skip   map[Task][]uint64
}

// snapshot is what a restart must keep of the job: its status, which holds
// progress, counters and locality counts, and every task's skip list.
func snapshot(t *testing.T, s *Server, job string) state {
	t.Helper()
	st, err := s.GetJobStatus(ctx, &pb.GetJobStatusRequest{JobId: job})
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	skip := map[Task][]uint64{}
	tt := s.jobs[job].tasks
	for _, kind := range []pb.TaskKind{mapKind, reduceKind} {
		n := len(tt.maps)
		if kind == reduceKind {
			n = len(tt.reduces)
		}
		for i := range n {
			task := Task{kind, uint32(i)}
			if skip[task], err = tt.Skip(task); err != nil {
				t.Fatal(err)
			}
		}
	}
	return state{st, skip}
}
