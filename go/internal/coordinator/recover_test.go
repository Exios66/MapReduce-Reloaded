package coordinator

import (
	"os"
	"path/filepath"
	"testing"

	pb "github.com/exios66/mapreduce-reloaded/go/gen/mapreduce/v1"
)

// recoverServer recovers a server from the log at path, and closes the
// log when the test ends.
func recoverServer(t *testing.T, path string) *Server {
	t.Helper()
	s, log, err := Recover(Config{}, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = log.Close() })
	return s
}

func register(t *testing.T, s *Server) string {
	t.Helper()
	reg, err := s.RegisterWorker(ctx, &pb.RegisterWorkerRequest{Worker: &pb.WorkerInfo{}})
	if err != nil {
		t.Fatal(err)
	}
	return reg.GetWorkerId()
}

func next(t *testing.T, s *Server, worker string) *pb.TaskAttemptId {
	t.Helper()
	resp, err := s.RequestTask(ctx, &pb.RequestTaskRequest{WorkerId: worker})
	if err != nil {
		t.Fatal(err)
	}
	if m := resp.GetMapTask(); m != nil {
		return m.GetAttempt()
	}
	return resp.GetReduceTask().GetAttempt()
}

func succeed(t *testing.T, s *Server, worker string, attempt *pb.TaskAttemptId, outputs []*pb.PartitionLocation) bool {
	t.Helper()
	rep, err := s.ReportTask(ctx, &pb.ReportTaskRequest{WorkerId: worker, Attempt: attempt, Outcome: &pb.ReportTaskRequest_Success{
		Success: &pb.TaskSuccess{Outputs: outputs, Counters: &pb.Counters{Values: map[string]int64{"n": 1}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return rep.GetAccepted()
}

func jobStatus(t *testing.T, s *Server, job string) *pb.GetJobStatusResponse {
	t.Helper()
	st, err := s.GetJobStatus(ctx, &pb.GetJobStatusRequest{JobId: job})
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// A restarted coordinator resumes its job: completed tasks stay completed,
// running attempts are given up, and attempt numbers are never reused.
func TestRecover(t *testing.T) {
	path := filepath.Join(t.TempDir(), "coordinator.log")
	in := filepath.Join(t.TempDir(), "in.txt")
	if err := os.WriteFile(in, []byte("a b\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := recoverServer(t, path)
	old := register(t, s)
	sub, err := s.SubmitJob(ctx, &pb.SubmitJobRequest{Job: &pb.JobSpec{
		Program: "wordcount", Inputs: []string{in}, Output: t.TempDir(), NumReduceTasks: 2,
	}})
	if err != nil {
		t.Fatal(err)
	}
	job := sub.GetJobId()
	succeed(t, s, old, next(t, s, old), outputs(0, 2))
	succeed(t, s, old, next(t, s, old), nil) // reduce 0
	running := next(t, s, old)               // reduce 1, lost in the crash

	s = recoverServer(t, path)
	if st := jobStatus(t, s, job); st.GetMapTasksCompleted() != 1 || st.GetReduceTasksCompleted() != 1 || st.GetCounters().GetValues()["n"] != 2 {
		t.Fatalf("recovered status %v; want 1 map and 1 reduce done, counted once", st)
	}
	w := register(t, s)
	if w == old {
		t.Fatalf("new worker got the old ID %s", w)
	}
	retry := next(t, s, w)
	if retry.GetTask().GetIndex() != 1 || retry.GetAttempt() != running.GetAttempt()+1 {
		t.Fatalf("got %v; want reduce 1, attempt %d", retry, running.GetAttempt()+1)
	}
	if succeed(t, s, old, running, nil) {
		t.Fatal("accepted a report from before the restart")
	}
	if !succeed(t, s, w, retry, nil) || jobStatus(t, s, job).GetState() != pb.JobState_JOB_STATE_SUCCEEDED {
		t.Fatal("the job did not finish")
	}

	s = recoverServer(t, path) // replaying a finished job, and a restart, again
	if st := jobStatus(t, s, job); st.GetState() != pb.JobState_JOB_STATE_SUCCEEDED || st.GetCounters().GetValues()["n"] != 3 {
		t.Fatalf("recovered status %v; want succeeded", st)
	}
	if w := register(t, s); w != "r3-w1" {
		t.Fatalf("worker ID %s after the second restart, want r3-w1", w)
	}
}
