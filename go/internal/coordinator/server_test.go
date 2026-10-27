package coordinator

import (
	"context"
	"errors"
	"fmt"
	"github.com/exios66/mapreduce-reloaded/go/pkg/api"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

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
	return newJobWith(t, Config{}, r)
}

func newJobWith(t *testing.T, cfg Config, r uint32) (*Server, string, string) {
	t.Helper()
	in := filepath.Join(t.TempDir(), "in.txt")
	if err := os.WriteFile(in, []byte("a b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewServer(cfg)
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
	// A job file that does not load is rejected at submission (ADR 0015).
	_, err = s.SubmitJob(ctx, &pb.SubmitJobRequest{Job: &pb.JobSpec{
		Program: "bad.go", ProgramSource: "package job\nfunc Map(", Inputs: []string{"x"}, Output: "o",
	}})
	assertCode(t, err, codes.InvalidArgument)
	_, err = s.SubmitJob(ctx, &pb.SubmitJobRequest{})
	assertCode(t, err, codes.InvalidArgument)

	used := t.TempDir() // an earlier job's output
	in := filepath.Join(used, "part-r-00000")
	if err := os.WriteFile(in, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = NewServer(Config{}).SubmitJob(ctx, &pb.SubmitJobRequest{Job: &pb.JobSpec{
		Program: "wordcount", Inputs: []string{in}, Output: used, NumReduceTasks: 1,
	}})
	assertCode(t, err, codes.AlreadyExists)
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
	other := register(t, s)
	for _, w := range []string{w, other} { // backups are off by default
		if resp, _ := s.RequestTask(ctx, &pb.RequestTaskRequest{WorkerId: w}); resp.GetWait() == nil {
			t.Fatalf("got %v; want wait while the map runs", resp)
		}
	}
	s.Shutdown()
	if resp, _ := s.RequestTask(ctx, &pb.RequestTaskRequest{WorkerId: w}); resp.GetShutdown() == nil {
		t.Fatalf("got %v; want shutdown", resp)
	}
}

// A map task goes to a worker on a host, or else in a zone, that holds its
// split (§3.4); the status counts where accepted attempts ran, and so does
// a recovered coordinator.
func TestLocality(t *testing.T) {
	dir := t.TempDir()
	where := map[string][]string{"0": {"host-a"}, "1": {"host-b"}, "2": {"zone-b"}}
	var inputs []string
	for name := range 3 {
		in := filepath.Join(dir, fmt.Sprint(name))
		if err := os.WriteFile(in, []byte("a b\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		inputs = append(inputs, in)
	}
	log := filepath.Join(dir, "log")
	s, l, err := Recover(Config{Locate: func(sp *pb.InputSplit) []string { return where[filepath.Base(sp.GetUri())] }}, log)
	if err != nil {
		t.Fatal(err)
	}
	at := func(host, zone string) string {
		reg, _ := s.RegisterWorker(ctx, &pb.RegisterWorkerRequest{Worker: &pb.WorkerInfo{Host: host, Zone: zone}})
		return reg.GetWorkerId()
	}
	a, b := at("host-a", "zone-a"), at("host-b", "zone-b")
	sub, err := s.SubmitJob(ctx, &pb.SubmitJobRequest{Job: &pb.JobSpec{
		Program: "wordcount", Inputs: inputs, Output: t.TempDir(), NumReduceTasks: 1,
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		worker string
		want   uint32
	}{{b, 1}, {b, 2}, {a, 0}} {
		attempt := next(t, s, c.worker)
		if got := attempt.GetTask().GetIndex(); got != c.want {
			t.Fatalf("%s got map %d, want %d", c.worker, got, c.want)
		}
		succeed(t, s, c.worker, attempt, []*pb.PartitionLocation{{MapIndex: c.want, Uri: "file:///map-output"}})
	}
	want := func(s *Server) {
		t.Helper()
		if st := jobStatus(t, s, sub.GetJobId()); st.GetMapTasksHostLocal() != 2 || st.GetMapTasksZoneLocal() != 1 {
			t.Fatalf("status %v; want 2 maps host-local, 1 zone-local", st)
		}
	}
	want(s)
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	want(recoverServer(t, Config{}, log))
}

// A map task is held back from a worker on another host for LocalityWait,
// then given to it.
func TestLocalityWait(t *testing.T) {
	const wait = 50 * time.Millisecond
	cfg := Config{LocalityWait: wait, Locate: func(*pb.InputSplit) []string { return []string{"host-a"} }}
	s, _, _ := newJobWith(t, cfg, 1)
	reg, _ := s.RegisterWorker(ctx, &pb.RegisterWorkerRequest{Worker: &pb.WorkerInfo{Host: "host-b"}})
	b := reg.GetWorkerId()
	if resp, _ := s.RequestTask(ctx, &pb.RequestTaskRequest{WorkerId: b}); resp.GetWait() == nil {
		t.Fatalf("got %v; want wait for a worker on host-a", resp)
	}
	time.Sleep(wait)
	if resp, _ := s.RequestTask(ctx, &pb.RequestTaskRequest{WorkerId: b}); resp.GetMapTask() == nil {
		t.Fatalf("got %v; want the map task after the wait", resp)
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
	_, err = s.ReportTask(ctx, &pb.ReportTaskRequest{WorkerId: w, Attempt: reduce.GetAttempt()})
	assertCode(t, err, codes.InvalidArgument) // neither success nor failure
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

// After SkipAfter attempts crash on a record, the next attempt is told to
// skip it (§4.6).
func TestSkipRecords(t *testing.T) {
	s, w, _ := newJob(t, 1)
	for range SkipAfter {
		resp, _ := s.RequestTask(ctx, &pb.RequestTaskRequest{WorkerId: w})
		if skip := resp.GetMapTask().GetSkipRecords(); len(skip) != 0 {
			t.Fatalf("skipping %v before %d crashes", skip, SkipAfter)
		}
		if _, err := s.ReportTask(ctx, &pb.ReportTaskRequest{WorkerId: w, Attempt: resp.GetMapTask().GetAttempt(), Outcome: &pb.ReportTaskRequest_Failure{
			Failure: &pb.TaskFailure{FailedRecord: proto.Uint64(0)},
		}}); err != nil {
			t.Fatal(err)
		}
	}
	resp, _ := s.RequestTask(ctx, &pb.RequestTaskRequest{WorkerId: w})
	if skip := resp.GetMapTask().GetSkipRecords(); !slices.Equal(skip, []uint64{0}) {
		t.Fatalf("map task %v; want it to skip record 0", resp.GetMapTask())
	}
}

// A job fails once it would skip more records than its limit.
func TestSkipLimit(t *testing.T) {
	s, w, id := newJob(t, 1)
	s.jobs[id].spec.MaxSkippedRecords = proto.Uint64(1)
	crash := func(n uint64) *pb.MapTask {
		t.Helper()
		resp, _ := s.RequestTask(ctx, &pb.RequestTaskRequest{WorkerId: w})
		if _, err := s.ReportTask(ctx, &pb.ReportTaskRequest{WorkerId: w, Attempt: resp.GetMapTask().GetAttempt(), Outcome: &pb.ReportTaskRequest_Failure{
			Failure: &pb.TaskFailure{FailedRecord: &n},
		}}); err != nil {
			t.Fatal(err)
		}
		return resp.GetMapTask()
	}
	crash(0)
	crash(0)
	crash(3)
	if m := crash(3); !slices.Equal(m.GetSkipRecords(), []uint64{0}) {
		t.Fatalf("map task %v; want it to skip record 0", m)
	}
	st, _ := s.GetJobStatus(ctx, &pb.GetJobStatusRequest{JobId: id})
	if st.GetState() != pb.JobState_JOB_STATE_FAILED || st.GetError() != "more than 1 bad records to skip" {
		t.Fatalf("status %v; want failed", st)
	}
	if resp, _ := s.RequestTask(ctx, &pb.RequestTaskRequest{WorkerId: w}); resp.GetWait() == nil {
		t.Fatalf("got %v; want wait once the job has failed", resp)
	}
}

// memLog keeps the records appended to it, and fails once err is set.
type memLog struct {
	records []*pb.LogRecord
	err     error
}

func (l *memLog) Append(b []byte) error {
	if l.err != nil {
		return l.err
	}
	rec := &pb.LogRecord{}
	if err := proto.Unmarshal(b, rec); err != nil {
		return err
	}
	l.records = append(l.records, rec)
	return nil
}

// kinds names the records in the log, e.g. "task_assigned".
func (l *memLog) kinds() []string {
	var names []string
	for _, rec := range l.records {
		m := rec.ProtoReflect()
		names = append(names, string(m.WhichOneof(m.Descriptor().Oneofs().ByName("record")).Name()))
	}
	return names
}

// Every decision of a job, and nothing else, is logged before its reply.
func TestLog(t *testing.T) {
	log := &memLog{}
	s, w, id := newJobWith(t, Config{Log: log, WorkerTimeout: time.Minute}, 1)
	run := func() *pb.TaskAttemptId {
		resp, err := s.RequestTask(ctx, &pb.RequestTaskRequest{WorkerId: w})
		if err != nil {
			t.Fatal(err)
		}
		attempt := resp.GetMapTask().GetAttempt()
		if attempt == nil {
			attempt = resp.GetReduceTask().GetAttempt()
		}
		return attempt
	}
	report := func(attempt *pb.TaskAttemptId, outputs ...*pb.PartitionLocation) {
		if _, err := s.ReportTask(ctx, &pb.ReportTaskRequest{WorkerId: w, Attempt: attempt, Outcome: &pb.ReportTaskRequest_Success{
			Success: &pb.TaskSuccess{Outputs: outputs},
		}}); err != nil {
			t.Fatal(err)
		}
	}
	report(run(), outputs(0, 1)...)
	_, _ = s.Heartbeat(ctx, &pb.HeartbeatRequest{WorkerId: w})
	report(run())
	if _, err := s.GetJobStatus(ctx, &pb.GetJobStatusRequest{JobId: id}); err != nil {
		t.Fatal(err)
	}
	want := []string{"job_submitted", "task_assigned", "task_reported", "task_assigned", "task_reported"}
	if got := log.kinds(); !slices.Equal(got, want) {
		t.Fatalf("log holds %v, want %v", got, want)
	}
	for i, rec := range log.records {
		if rec.GetAt() == nil || i > 0 && rec.GetAt().AsTime().Before(log.records[i-1].GetAt().AsTime()) {
			t.Fatalf("record %d at %v, after one at %v", i, rec.GetAt(), log.records[max(0, i-1)].GetAt())
		}
	}

	s, _, _ = newJobWith(t, Config{Log: log, WorkerTimeout: time.Minute}, 1)
	if got := log.kinds()[len(want):]; !slices.Equal(got, want[:1]) {
		t.Fatalf("second job logged %v", got)
	}
	recordStart := time.Now()
	s.Expire(recordStart.Add(time.Hour))
	if got := log.kinds()[len(want)+1:]; !slices.Equal(got, []string{"worker_lost"}) {
		t.Fatalf("expiry logged %v", got)
	}
	if at := log.records[len(log.records)-1].GetAt().AsTime(); at.Before(recordStart) || at.After(time.Now()) {
		t.Fatalf("worker loss recorded at %v; want the current time, not the expiry cutoff", at)
	}
}

// A coordinator whose log fails makes no reply that depends on it.
func TestLogFails(t *testing.T) {
	log := &memLog{}
	s, w, _ := newJobWith(t, Config{Log: log}, 1)
	log.err = errors.New("disk full")
	_, err := s.RequestTask(ctx, &pb.RequestTaskRequest{WorkerId: w})
	assertCode(t, err, codes.Unavailable)
	_, err = s.ReportTask(ctx, &pb.ReportTaskRequest{WorkerId: w, Attempt: &pb.TaskAttemptId{
		Task: &pb.TaskId{JobId: "job-1", Kind: pb.TaskKind_TASK_KIND_MAP},
	}, Outcome: &pb.ReportTaskRequest_Failure{Failure: &pb.TaskFailure{}}})
	assertCode(t, err, codes.Unavailable)

	in := filepath.Join(t.TempDir(), "in.txt")
	if err := os.WriteFile(in, []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s = NewServer(Config{Log: log})
	_, err = s.SubmitJob(ctx, &pb.SubmitJobRequest{Job: &pb.JobSpec{
		Program: "wordcount", Inputs: []string{in}, Output: t.TempDir(), NumReduceTasks: 1,
	}})
	assertCode(t, err, codes.Unavailable)
	if len(s.jobs) != 0 {
		t.Fatal("a job whose submission was not logged was kept")
	}
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

// A heartbeat lists the jobs still running, so workers can delete the
// map output of the others; none once the job has ended.
func TestHeartbeatActiveJobs(t *testing.T) {
	s, w, job := newJob(t, 1)
	resp, _ := s.Heartbeat(ctx, &pb.HeartbeatRequest{WorkerId: w})
	if got := resp.GetActiveJobs().GetJobIds(); !slices.Equal(got, []string{job}) {
		t.Fatalf("active jobs %v, want [%s]", got, job)
	}
	s.mu.Lock()
	s.end(s.active, "", time.Now())
	s.mu.Unlock()
	resp, _ = s.Heartbeat(ctx, &pb.HeartbeatRequest{WorkerId: w})
	if resp.GetActiveJobs() == nil || len(resp.GetActiveJobs().GetJobIds()) != 0 {
		t.Fatalf("active jobs %v after the job ended, want an empty list", resp.GetActiveJobs())
	}
}

// Only the KeepJobs most recent ended jobs are kept, and a forgotten job's
// ID is not used again.
func TestKeepJobs(t *testing.T) {
	s, _, first := newJobWith(t, Config{KeepJobs: 2}, 1)
	in := filepath.Join(t.TempDir(), "in.txt")
	if err := os.WriteFile(in, []byte("a b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ids := []string{first}
	for range 3 {
		s.mu.Lock()
		s.end(s.active, "", time.Now())
		s.mu.Unlock()
		sub, err := s.SubmitJob(ctx, &pb.SubmitJobRequest{Job: &pb.JobSpec{
			Program: "wordcount", Inputs: []string{in}, Output: t.TempDir(), NumReduceTasks: 1,
		}})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, sub.GetJobId())
	}
	if want := []string{"job-1", "job-2", "job-3", "job-4"}; !slices.Equal(ids, want) {
		t.Fatalf("IDs %v, want %v", ids, want)
	}
	for i, id := range ids {
		_, err := s.GetJobStatus(ctx, &pb.GetJobStatusRequest{JobId: id})
		if forgotten := i == 0; forgotten != (status.Code(err) == codes.NotFound) {
			t.Errorf("GetJobStatus(%s): %v", id, err)
		}
	}
	s.mu.Lock()
	s.end(s.active, "", time.Now())
	s.mu.Unlock()
	if len(s.jobs) != 2 || len(s.order) != 2 {
		t.Fatalf("idle coordinator kept %d jobs and %d ordered entries, want 2", len(s.jobs), len(s.order))
	}
	for i, id := range ids {
		_, err := s.GetJobStatus(ctx, &pb.GetJobStatusRequest{JobId: id})
		if forgotten := i < 2; forgotten != (status.Code(err) == codes.NotFound) {
			t.Errorf("idle GetJobStatus(%s): %v", id, err)
		}
	}
}

func TestPausedTaskRequest(t *testing.T) {
	s, w, _ := newJob(t, 1)
	req := &pb.RequestTaskRequest{WorkerId: w, Paused: true}
	resp, err := s.RequestTask(ctx, req)
	if err != nil || resp.GetWait() == nil {
		t.Fatalf("paused request: %v, %v; want wait", resp, err)
	}
	resp, err = s.RequestTask(ctx, &pb.RequestTaskRequest{WorkerId: w})
	if err != nil || resp.GetMapTask() == nil || resp.GetMapTask().GetAttempt().GetAttempt() != 0 {
		t.Fatalf("resumed request: %v, %v; want first map attempt", resp, err)
	}
	s.Shutdown()
	resp, err = s.RequestTask(ctx, req)
	if err != nil || resp.GetShutdown() == nil {
		t.Fatalf("paused shutdown: %v, %v; want shutdown", resp, err)
	}
}

// A worker is told to stop the attempts it should no longer run (§3.6).
func TestHeartbeatCancels(t *testing.T) {
	s, w, _ := newJob(t, 1)
	other, _ := s.RegisterWorker(ctx, &pb.RegisterWorkerRequest{Worker: &pb.WorkerInfo{}})
	resp, _ := s.RequestTask(ctx, &pb.RequestTaskRequest{WorkerId: w})
	attempt := resp.GetMapTask().GetAttempt()
	stale := proto.CloneOf(attempt)
	stale.Task.JobId = "job-0"
	cancels := func(worker string, attempts ...*pb.TaskAttemptId) int {
		req := &pb.HeartbeatRequest{WorkerId: worker}
		for _, a := range attempts {
			req.Running = append(req.Running, &pb.TaskProgress{Attempt: a})
		}
		resp, err := s.Heartbeat(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		return len(resp.GetCancel())
	}
	if n := cancels(w, attempt); n != 0 {
		t.Fatalf("running attempt: %d cancelled, want 0", n)
	}
	if n := cancels(other.GetWorkerId(), attempt, stale); n != 2 {
		t.Fatalf("another worker's attempt and an unknown job's: %d cancelled, want 2", n)
	}
	_, err := s.ReportTask(ctx, &pb.ReportTaskRequest{WorkerId: w, Attempt: attempt, Outcome: &pb.ReportTaskRequest_Success{
		Success: &pb.TaskSuccess{Outputs: []*pb.PartitionLocation{{MapIndex: 0, Partition: 0, Uri: "file:///map-output"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if n := cancels(w, attempt); n != 1 {
		t.Fatalf("completed attempt: %d cancelled, want 1", n)
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

// A submission cannot choose how much state the coordinator allocates.
func TestSubmitJobTaskCountLimits(t *testing.T) {
	in := filepath.Join(t.TempDir(), "in.txt")
	if err := os.WriteFile(in, []byte("a b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		job  *pb.JobSpec
		want string
	}{
		{"reducers", &pb.JobSpec{NumReduceTasks: api.MaxReduceTasks + 1}, "reduce tasks R"},
		{"mappers", &pb.JobSpec{NumReduceTasks: 1, NumMapTasks: api.MaxMapTasks + 1}, "map tasks M"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.job.Program, tc.job.Inputs, tc.job.Output = "wordcount", []string{in}, t.TempDir()
			_, err := NewServer(Config{}).SubmitJob(ctx, &pb.SubmitJobRequest{Job: tc.job})
			assertCode(t, err, codes.InvalidArgument)
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestSubmitJobRejectsTooManySplits(t *testing.T) {
	in := filepath.Join(t.TempDir(), "big.txt")
	f, err := os.Create(in)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(api.MaxMapTasks + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	job := &pb.JobSpec{Program: "wordcount", Inputs: []string{in}, Output: t.TempDir(), NumReduceTasks: 1, SplitSizeBytes: 1}
	_, err = NewServer(Config{}).SubmitJob(ctx, &pb.SubmitJobRequest{Job: job})
	assertCode(t, err, codes.InvalidArgument)
	if !strings.Contains(err.Error(), "map tasks") && !strings.Contains(err.Error(), "the most is") {
		t.Fatalf("got %v, want a split limit error", err)
	}
}
