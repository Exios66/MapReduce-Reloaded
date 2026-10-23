package coordinator

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/exios66/mapreduce-reloaded/go/gen/mapreduce/v1"

	_ "github.com/exios66/mapreduce-reloaded/go/examples/wordcount"
)

var ctx = context.Background()

func assertCode(t *testing.T, err error, want codes.Code) {
	t.Helper()
	if status.Code(err) != want {
		t.Fatalf("got %v, want code %v", err, want)
	}
}

// newJob returns a server with one worker and one submitted job over a small
// input in one split, with r reduce tasks (0 for the default).
func newJob(t *testing.T, r uint32) (*Server, string, string) {
	t.Helper()
	in := filepath.Join(t.TempDir(), "in.txt")
	if err := os.WriteFile(in, []byte("a b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewServer(Config{})
	reg, _ := s.RegisterWorker(ctx, &pb.RegisterWorkerRequest{Worker: &pb.WorkerInfo{}})
	sub, err := s.SubmitJob(ctx, &pb.SubmitJobRequest{Job: &pb.JobSpec{
		Program: "wordcount", Inputs: []string{in}, Output: t.TempDir(), NumReduceTasks: r,
	}})
	if err != nil {
		t.Fatal(err)
	}
	return s, reg.GetWorkerId(), sub.GetJobId()
}

func TestSubmitJob(t *testing.T) {
	s, _, id := newJob(t, 0)
	st, err := s.GetJobStatus(ctx, &pb.GetJobStatusRequest{JobId: id})
	if err != nil || st.GetReduceTasksTotal() != 2 || st.GetMapTasksTotal() != 1 ||
		st.GetState() != pb.JobState_JOB_STATE_MAPPING {
		t.Fatalf("status %v, %v; want 1 map, the default 2 reduces", st, err)
	}
	_, err = s.SubmitJob(ctx, &pb.SubmitJobRequest{Job: &pb.JobSpec{
		Program: "wordcount", Inputs: []string{"x"}, Output: "o", NumReduceTasks: 1,
	}})
	assertCode(t, err, codes.InvalidArgument) // no input matches

	empty := filepath.Join(t.TempDir(), "empty.txt")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = s.SubmitJob(ctx, &pb.SubmitJobRequest{Job: &pb.JobSpec{
		Program: "wordcount", Inputs: []string{empty}, Output: "o", NumReduceTasks: 1,
	}})
	assertCode(t, err, codes.InvalidArgument)

	_, err = s.SubmitJob(ctx, &pb.SubmitJobRequest{Job: &pb.JobSpec{Program: "nope"}})
	assertCode(t, err, codes.InvalidArgument)
	_, err = s.SubmitJob(ctx, &pb.SubmitJobRequest{})
	assertCode(t, err, codes.InvalidArgument)
}

func TestOneJobAtATime(t *testing.T) {
	s, _, _ := newJob(t, 1)
	in := filepath.Join(t.TempDir(), "in.txt")
	if err := os.WriteFile(in, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := s.SubmitJob(ctx, &pb.SubmitJobRequest{Job: &pb.JobSpec{
		Program: "wordcount", Inputs: []string{in}, Output: "o", NumReduceTasks: 1,
	}})
	assertCode(t, err, codes.FailedPrecondition)
}

func TestRequestTask(t *testing.T) {
	s, w, _ := newJob(t, 1)
	_, err := s.RequestTask(ctx, &pb.RequestTaskRequest{WorkerId: "nope"})
	assertCode(t, err, codes.NotFound)

	resp, err := s.RequestTask(ctx, &pb.RequestTaskRequest{WorkerId: w})
	if err != nil || resp.GetMapTask() == nil {
		t.Fatalf("got %v, %v; want the map task", resp, err)
	}
	if resp, _ := s.RequestTask(ctx, &pb.RequestTaskRequest{WorkerId: w}); resp.GetWait() == nil {
		t.Fatalf("got %v; want wait while the map runs", resp)
	}
	s.Shutdown()
	if resp, _ := s.RequestTask(ctx, &pb.RequestTaskRequest{WorkerId: w}); resp.GetShutdown() == nil {
		t.Fatalf("got %v; want shutdown", resp)
	}
}

func TestReportTask(t *testing.T) {
	s, w, id := newJob(t, 1)
	resp, _ := s.RequestTask(ctx, &pb.RequestTaskRequest{WorkerId: w})
	attempt := resp.GetMapTask().GetAttempt()
	success := &pb.ReportTaskRequest_Success{Success: &pb.TaskSuccess{
		Outputs:  []*pb.PartitionLocation{{MapIndex: 0, Partition: 0, Uri: "file:///map-output"}},
		Counters: &pb.Counters{Values: map[string]int64{"n": 1}},
	}}

	_, err := s.ReportTask(ctx, &pb.ReportTaskRequest{Attempt: &pb.TaskAttemptId{Task: &pb.TaskId{JobId: "nope"}}})
	assertCode(t, err, codes.NotFound)
	_, err = s.ReportTask(ctx, &pb.ReportTaskRequest{WorkerId: w, Attempt: attempt, Outcome: &pb.ReportTaskRequest_Success{
		Success: &pb.TaskSuccess{},
	}})
	assertCode(t, err, codes.InvalidArgument) // no outputs
	if rep, err := s.ReportTask(ctx, &pb.ReportTaskRequest{WorkerId: "other", Attempt: attempt, Outcome: success}); err != nil || rep.GetAccepted() {
		t.Fatalf("report from another worker: %v, %v; want ignored", rep, err)
	}

	for _, want := range []bool{true, false} { // the second report is a duplicate
		rep, err := s.ReportTask(ctx, &pb.ReportTaskRequest{WorkerId: w, Attempt: attempt, Outcome: success})
		if err != nil || rep.GetAccepted() != want {
			t.Fatalf("report: %v, %v; want accepted=%v", rep, err, want)
		}
	}
	st, _ := s.GetJobStatus(ctx, &pb.GetJobStatusRequest{JobId: id})
	if st.GetState() != pb.JobState_JOB_STATE_REDUCING || st.GetCounters().GetValues()["n"] != 1 {
		t.Fatalf("status %v; want reducing, counters counted once", st)
	}

	resp, _ = s.RequestTask(ctx, &pb.RequestTaskRequest{WorkerId: w})
	reduce := resp.GetReduceTask()
	if len(reduce.GetSources()) != 1 {
		t.Fatalf("reduce task %v; want one source", reduce)
	}
	rep, err := s.ReportTask(ctx, &pb.ReportTaskRequest{WorkerId: w, Attempt: reduce.GetAttempt(), Outcome: &pb.ReportTaskRequest_Failure{
		Failure: &pb.TaskFailure{Message: "boom"},
	}})
	if err != nil || rep.GetAccepted() {
		t.Fatalf("failure report: %v, %v", rep, err)
	}
	if resp, _ := s.RequestTask(ctx, &pb.RequestTaskRequest{WorkerId: w}); resp.GetReduceTask() == nil {
		t.Fatalf("got %v; want the failed reduce task again", resp)
	}
	_, err = s.GetJobStatus(ctx, &pb.GetJobStatusRequest{JobId: "nope"})
	assertCode(t, err, codes.NotFound)
}

func TestHeartbeat(t *testing.T) {
	s, w, _ := newJob(t, 1)
	for id, reregister := range map[string]bool{w: false, "nope": true} {
		resp, _ := s.Heartbeat(ctx, &pb.HeartbeatRequest{WorkerId: id})
		if resp.GetReregister() != reregister {
			t.Errorf("Heartbeat(%q).Reregister = %v", id, resp.GetReregister())
		}
	}
}

// A worker that goes quiet is declared dead, and its completed map task is
// run again (§3.3).
func TestExpire(t *testing.T) {
	s, w, id := newJob(t, 1)
	if dead := s.Expire(time.Now().Add(time.Hour)); dead != nil {
		t.Fatalf("expired %v with no timeout set", dead)
	}
	s.cfg.WorkerTimeout = time.Minute
	resp, _ := s.RequestTask(ctx, &pb.RequestTaskRequest{WorkerId: w})
	_, err := s.ReportTask(ctx, &pb.ReportTaskRequest{WorkerId: w, Attempt: resp.GetMapTask().GetAttempt(), Outcome: &pb.ReportTaskRequest_Success{
		Success: &pb.TaskSuccess{Outputs: []*pb.PartitionLocation{{MapIndex: 0, Partition: 0, Uri: "file:///map-output"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}

	if dead := s.Expire(time.Now()); dead != nil {
		t.Fatalf("expired %v too early", dead)
	}
	if dead := s.Expire(time.Now().Add(2 * time.Minute)); !slices.Equal(dead, []string{w}) {
		t.Fatalf("expired %v, want [%s]", dead, w)
	}
	if st, _ := s.GetJobStatus(ctx, &pb.GetJobStatusRequest{JobId: id}); st.GetMapTasksCompleted() != 0 {
		t.Fatalf("status %v; want the lost map output to be redone", st)
	}
	_, err = s.RequestTask(ctx, &pb.RequestTaskRequest{WorkerId: w})
	assertCode(t, err, codes.NotFound)
	if hb, _ := s.Heartbeat(ctx, &pb.HeartbeatRequest{WorkerId: w}); !hb.GetReregister() {
		t.Fatal("a dead worker's heartbeat should ask it to register again")
	}
	reg, _ := s.RegisterWorker(ctx, &pb.RegisterWorkerRequest{Worker: &pb.WorkerInfo{}})
	if resp, _ := s.RequestTask(ctx, &pb.RequestTaskRequest{WorkerId: reg.GetWorkerId()}); resp.GetMapTask().GetAttempt().GetAttempt() != 1 {
		t.Fatalf("got %v; want attempt 1 of the map task", resp)
	}
}

func TestMonitor(t *testing.T) {
	s, w, _ := newJob(t, 1)
	s.cfg.HeartbeatInterval, s.cfg.WorkerTimeout = time.Millisecond, time.Millisecond
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { s.Monitor(ctx); close(done) }()
	for {
		if hb, _ := s.Heartbeat(ctx, &pb.HeartbeatRequest{WorkerId: w}); hb.GetReregister() {
			break
		}
		time.Sleep(5 * time.Millisecond) // longer than the timeout
	}
	cancel()
	<-done
}
