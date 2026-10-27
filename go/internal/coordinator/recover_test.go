package coordinator

import (
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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

// The log keeps only the jobs the coordinator keeps (#178): after many
// jobs its size follows KeepJobs, and a restart recovers the kept jobs and
// the running one, forgets the rest, never reuses a job ID, and still
// numbers workers past every restart.
func TestCompactLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "coordinator.log")
	in := filepath.Join(t.TempDir(), "in.txt")
	if err := os.WriteFile(in, []byte("a b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := Config{KeepJobs: 2}
	s := recoverServer(t, cfg, path)
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
	run := func() string {
		t.Helper()
		job := submit()
		succeed(t, s, w, next(t, s, w), outputs(0, 1))
		succeed(t, s, w, next(t, s, w), nil)
		if jobStatus(t, s, job).GetState() != pb.JobState_JOB_STATE_SUCCEEDED {
			t.Fatalf("%s did not finish", job)
		}
		return job
	}
	logSize := func() int64 {
		t.Helper()
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		return info.Size()
	}

	for range 5 {
		run()
	}
	after5 := logSize()
	var ended []string
	for range 20 {
		ended = append(ended, run())
	}
	// Records carry times, so sizes vary by a few bytes.
	if after25 := logSize(); after25 > after5+after5/4 {
		t.Fatalf("log is %d bytes after 25 jobs, %d after 5; want it bounded by KeepJobs", after25, after5)
	}
	running := submit()
	succeed(t, s, w, next(t, s, w), outputs(0, 1)) // its map task is done

	s = recoverServer(t, cfg, path)
	for _, job := range ended[:len(ended)-2] {
		if _, err := s.GetJobStatus(ctx, &pb.GetJobStatusRequest{JobId: job}); status.Code(err) != codes.NotFound {
			t.Fatalf("forgotten %s: got %v, want NotFound", job, err)
		}
	}
	for _, job := range ended[len(ended)-2:] {
		if st := jobStatus(t, s, job); st.GetState() != pb.JobState_JOB_STATE_SUCCEEDED {
			t.Fatalf("kept %s recovered as %v", job, st)
		}
	}
	if st := jobStatus(t, s, running); st.GetState() != pb.JobState_JOB_STATE_REDUCING || st.GetMapTasksCompleted() != 1 {
		t.Fatalf("running %s recovered as %v; want reducing, its map done", running, st)
	}
	if w = register(t, s); w != "r2-w1" {
		t.Fatalf("worker ID %s after the first restart, want r2-w1", w)
	}
	succeed(t, s, w, next(t, s, w), nil)
	if job := run(); job != "job-27" {
		t.Fatalf("next job is %s, want job-27", job)
	}
	s = recoverServer(t, cfg, path)
	if w := register(t, s); w != "r3-w1" {
		t.Fatalf("worker ID %s after the second restart, want r3-w1", w)
	}
}
