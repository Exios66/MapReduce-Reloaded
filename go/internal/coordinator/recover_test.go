package coordinator

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"

	pb "github.com/exios66/mapreduce-reloaded/go/gen/mapreduce/v1"
)

// recoverServer recovers a server from the log at path, and closes the
// log when the test ends.
func recoverServer(t *testing.T, cfg Config, path string) *Server {
	t.Helper()
	s, log, err := Recover(cfg, path)
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
// running attempts are given up, and attempt numbers are never reused. The
// job keeps the times it was submitted and finished.
func TestRecover(t *testing.T) {
	path := filepath.Join(t.TempDir(), "coordinator.log")
	in := filepath.Join(t.TempDir(), "in.txt")
	if err := os.WriteFile(in, []byte("a b\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := recoverServer(t, Config{}, path)
	old := register(t, s)
	sub, err := s.SubmitJob(ctx, &pb.SubmitJobRequest{Job: &pb.JobSpec{
		Program: "wordcount", Inputs: []string{in}, Output: t.TempDir(), NumReduceTasks: 2,
	}})
	if err != nil {
		t.Fatal(err)
	}
	job := sub.GetJobId()
	submitted := jobStatus(t, s, job).GetSubmittedAt()
	if submitted == nil || jobStatus(t, s, job).GetFinishedAt() != nil {
		t.Fatalf("running job status %v; want a submit time and no finish time", jobStatus(t, s, job))
	}
	succeed(t, s, old, next(t, s, old), outputs(0, 2))
	succeed(t, s, old, next(t, s, old), nil) // reduce 0
	running := next(t, s, old)               // reduce 1, lost in the crash

	s = recoverServer(t, Config{}, path)
	if st := jobStatus(t, s, job); st.GetMapTasksCompleted() != 1 || st.GetReduceTasksCompleted() != 1 || st.GetCounters().GetValues()["n"] != 2 {
		t.Fatalf("recovered status %v; want 1 map and 1 reduce done, counted once", st)
	}
	if st := jobStatus(t, s, job); !proto.Equal(st.GetSubmittedAt(), submitted) || st.GetFinishedAt() != nil {
		t.Fatalf("recovered status %v; want submitted at %v and not finished", st, submitted)
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
	finished := jobStatus(t, s, job).GetFinishedAt()
	if finished == nil || finished.AsTime().Before(submitted.AsTime()) {
		t.Fatalf("job finished at %v, submitted at %v", finished, submitted)
	}

	s = recoverServer(t, Config{}, path) // replaying a finished job, and a restart, again
	if st := jobStatus(t, s, job); st.GetState() != pb.JobState_JOB_STATE_SUCCEEDED || st.GetCounters().GetValues()["n"] != 3 {
		t.Fatalf("recovered status %v; want succeeded", st)
	}
	if st := jobStatus(t, s, job); !proto.Equal(st.GetSubmittedAt(), submitted) || !proto.Equal(st.GetFinishedAt(), finished) {
		t.Fatalf("recovered status %v; want submitted at %v and finished at %v", st, submitted, finished)
	}
	if w := register(t, s); w != "r3-w1" {
		t.Fatalf("worker ID %s after the second restart, want r3-w1", w)
	}
}

// A backup attempt is logged like any other, and replays whether or not
// the restarted coordinator makes backups itself.
func TestRecoverBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "coordinator.log")
	in := filepath.Join(t.TempDir(), "in.txt")
	if err := os.WriteFile(in, []byte("a b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := recoverServer(t, Config{Backups: true}, path)
	w1, w2 := register(t, s), register(t, s)
	sub, err := s.SubmitJob(ctx, &pb.SubmitJobRequest{Job: &pb.JobSpec{
		Program: "wordcount", Inputs: []string{in}, Output: t.TempDir(), NumReduceTasks: 1,
	}})
	if err != nil {
		t.Fatal(err)
	}
	original := next(t, s, w1)
	backup := next(t, s, w2)
	if backup.GetTask().GetIndex() != 0 || backup.GetAttempt() != 1 {
		t.Fatalf("w2 got %v; want attempt 1 of map 0", backup)
	}
	if !succeed(t, s, w2, backup, outputs(0, 1)) {
		t.Fatal("backup's completion rejected")
	}

	s = recoverServer(t, Config{}, path)
	if st := jobStatus(t, s, sub.GetJobId()); st.GetMapTasksCompleted() != 1 || st.GetBackupAttempts() != 1 || st.GetBackupWins() != 1 {
		t.Fatalf("recovered status %v; want the map done by its one backup", st)
	}
	if succeed(t, s, w1, original, outputs(0, 1)) {
		t.Fatal("accepted the original's completion after the backup's")
	}
}

// A log written before records had times still replays; its job has no
// submit or finish time.
func TestReplayWithoutTimes(t *testing.T) {
	s := NewServer(Config{})
	rec := &pb.LogRecord{Record: &pb.LogRecord_JobSubmitted{JobSubmitted: &pb.JobSubmitted{
		JobId: "job-1", Job: &pb.JobSpec{NumReduceTasks: 1}, Splits: []*pb.InputSplit{{}},
	}}}
	if err := s.replay(rec); err != nil {
		t.Fatal(err)
	}
	if st := jobStatus(t, s, "job-1"); st.GetSubmittedAt() != nil || st.GetFinishedAt() != nil {
		t.Fatalf("status %v; want no times", st)
	}
}

// Eviction must replay consistently, and restarting must not reuse IDs
// belonging to forgotten jobs. Zero retention means keep all jobs.
func TestRecoverJobRetention(t *testing.T) {
	for _, keep := range []int{0, 1, 2} {
		t.Run(fmt.Sprintf("keep=%d", keep), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "coordinator.log")
			in := filepath.Join(t.TempDir(), "in.txt")
			if err := os.WriteFile(in, []byte("a\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			s := recoverServer(t, Config{KeepJobs: keep}, path)
			w := register(t, s)
			submit := func() string {
				t.Helper()
				sub, err := s.SubmitJob(ctx, &pb.SubmitJobRequest{Job: &pb.JobSpec{
					Program: "wordcount", Inputs: []string{in}, Output: t.TempDir(), NumReduceTasks: 1,
				}})
				if err != nil {
					t.Fatal(err)
				}
				return sub.GetJobId()
			}
			for n := 1; n <= 4; n++ {
				if id := submit(); id != fmt.Sprintf("job-%d", n) {
					t.Fatalf("job ID=%s", id)
				}
				if n < 4 {
					if !succeed(t, s, w, next(t, s, w), outputs(0, 1)) || !succeed(t, s, w, next(t, s, w), nil) {
						t.Fatal("completion rejected")
					}
				}
			}
			checkRetained := func() {
				t.Helper()
				for n := 1; n <= 4; n++ {
					_, err := s.GetJobStatus(ctx, &pb.GetJobStatusRequest{JobId: fmt.Sprintf("job-%d", n)})
					want := codes.OK
					if keep > 0 && n < 4-keep {
						want = codes.NotFound
					}
					assertCode(t, err, want)
				}
			}
			checkRetained()
			s = recoverServer(t, Config{KeepJobs: keep}, path)
			checkRetained()
			w = register(t, s)
			hb, err := s.Heartbeat(ctx, &pb.HeartbeatRequest{WorkerId: w})
			if err != nil || hb.GetActiveJobs() == nil || len(hb.GetActiveJobs().GetJobIds()) != 1 || hb.GetActiveJobs().GetJobIds()[0] != "job-4" {
				t.Fatalf("recovered active jobs: %v, %v", hb, err)
			}
			if !succeed(t, s, w, next(t, s, w), outputs(0, 1)) || !succeed(t, s, w, next(t, s, w), nil) {
				t.Fatal("recovered job did not finish")
			}
			if id := submit(); id != "job-5" {
				t.Fatalf("ID reused after restart: %s", id)
			}
		})
	}
}

func TestRecoverPreservesJobSource(t *testing.T) {
	src, err := os.ReadFile("../../../examples/wordcount.go")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "coordinator.log")
	in := filepath.Join(t.TempDir(), "in.txt")
	if err := os.WriteFile(in, []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := recoverServer(t, Config{}, path)
	spec := &pb.JobSpec{
		Program: "custom.go", ProgramSource: string(src), Inputs: []string{in}, Output: t.TempDir(),
		NumReduceTasks: 1, UseCombiner: false, Params: map[string]string{"label": "recovered"},
	}
	if _, err := s.SubmitJob(ctx, &pb.SubmitJobRequest{Job: spec}); err != nil {
		t.Fatal(err)
	}
	s = recoverServer(t, Config{}, path)
	w := register(t, s)
	resp, err := s.RequestTask(ctx, &pb.RequestTaskRequest{WorkerId: w})
	if err != nil || resp.GetMapTask() == nil || !proto.Equal(resp.GetMapTask().GetJob(), spec) {
		t.Fatalf("recovered task lost source or options: %v, %v", resp, err)
	}
}
