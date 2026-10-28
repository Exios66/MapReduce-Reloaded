package coordinator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	pb "github.com/exios66/mapreduce-reloaded/go/gen/mapreduce/v1"
)

// fail reports that attempt failed with f.
func fail(t *testing.T, s *Server, worker string, attempt *pb.TaskAttemptId, f *pb.TaskFailure) {
	t.Helper()
	if _, err := s.ReportTask(ctx, &pb.ReportTaskRequest{WorkerId: worker, Attempt: attempt, Outcome: &pb.ReportTaskRequest_Failure{Failure: f}}); err != nil {
		t.Fatal(err)
	}
}

func wantFailed(t *testing.T, s *Server, job, msg string) {
	t.Helper()
	st := jobStatus(t, s, job)
	if st.GetState() != pb.JobState_JOB_STATE_FAILED || !strings.Contains(st.GetError(), msg) {
		t.Fatalf("status %v; want FAILED with %q", st, msg)
	}
}

// A fatal failure, as from a worker that cannot load the job file, fails
// the job at once (#176).
func TestFatalFailureFailsJob(t *testing.T) {
	s, w, job := newJob(t, 1)
	fail(t, s, w, next(t, s, w), &pb.TaskFailure{Message: "job file: undefined: Map", Fatal: true})
	wantFailed(t, s, job, "cannot run: job file: undefined: Map")
}

// A task that always fails fails the job after MaxTaskFailures attempts,
// with its last error. Reduce attempts that could not fetch lost map output
// do not count, and without a limit a task is retried for ever.
func TestMaxTaskFailures(t *testing.T) {
	s, w, job := newJobWith(t, Config{MaxTaskFailures: 3}, 1)
	for i := range 2 {
		fail(t, s, w, next(t, s, w), &pb.TaskFailure{Message: "boom " + string(rune('a'+i))})
	}
	if st := jobStatus(t, s, job).GetState(); st == pb.JobState_JOB_STATE_FAILED {
		t.Fatal("failed after 2 of 3 failures")
	}
	fail(t, s, w, next(t, s, w), &pb.TaskFailure{Message: "boom c"})
	wantFailed(t, s, job, "failed 3 times; the last error: boom c")

	s, w, job = newJobWith(t, Config{MaxTaskFailures: 3}, 1)
	outs := outputs(0, 1)
	outs[0].WorkerAddress = "gone:1"
	succeed(t, s, w, next(t, s, w), outs)
	for range 5 { // the map output is lost each time; the map task runs again
		fail(t, s, w, next(t, s, w), &pb.TaskFailure{Message: "fetch", LostOutput: outs[0]})
		if st := jobStatus(t, s, job).GetState(); st == pb.JobState_JOB_STATE_FAILED {
			t.Fatalf("lost map output failed the job: %v", jobStatus(t, s, job))
		}
		succeed(t, s, w, next(t, s, w), outs)
	}

	s, w, job = newJob(t, 1) // no limit
	for range 20 {
		fail(t, s, w, next(t, s, w), &pb.TaskFailure{Message: "boom"})
	}
	if st := jobStatus(t, s, job).GetState(); st == pb.JobState_JOB_STATE_FAILED {
		t.Fatal("failed with no limit")
	}
}

// With the smallest limit allowed, skipping a bad record still works: two
// crashes on it, then an attempt that skips it.
func TestMaxTaskFailuresKeepsSkipping(t *testing.T) {
	s, w, job := newJobWith(t, Config{MaxTaskFailures: SkipAfter + 1}, 1)
	record := uint64(7)
	for range SkipAfter {
		fail(t, s, w, next(t, s, w), &pb.TaskFailure{Message: "panic", FailedRecord: &record})
	}
	resp, err := s.RequestTask(ctx, &pb.RequestTaskRequest{WorkerId: w})
	if err != nil {
		t.Fatal(err)
	}
	m := resp.GetMapTask()
	if len(m.GetSkipRecords()) != 1 || m.GetSkipRecords()[0] != record {
		t.Fatalf("map task %v; want record %d skipped", m, record)
	}
	succeed(t, s, w, m.GetAttempt(), outputs(0, 1))
	succeed(t, s, w, next(t, s, w), nil)
	if st := jobStatus(t, s, job).GetState(); st != pb.JobState_JOB_STATE_SUCCEEDED {
		t.Fatalf("job %v; want it to succeed", jobStatus(t, s, job))
	}
}

// A job that failed by the limit is still failed after a restart.
func TestMaxTaskFailuresRecovers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "coordinator.log")
	in := filepath.Join(t.TempDir(), "in.txt")
	if err := os.WriteFile(in, []byte("a b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := Config{MaxTaskFailures: 3}
	s := recoverServer(t, cfg, path)
	w := register(t, s)
	sub, err := s.SubmitJob(ctx, &pb.SubmitJobRequest{Job: &pb.JobSpec{
		Program: "wordcount", Inputs: []string{in}, Output: t.TempDir(), NumReduceTasks: 1,
	}})
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		fail(t, s, w, next(t, s, w), &pb.TaskFailure{Message: "boom"})
	}
	wantFailed(t, s, sub.GetJobId(), "failed 3 times")
	s = recoverServer(t, cfg, path)
	wantFailed(t, s, sub.GetJobId(), "failed 3 times")
}
