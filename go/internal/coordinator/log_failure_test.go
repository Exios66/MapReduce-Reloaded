package coordinator

import (
	"errors"
	"fmt"
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
	"github.com/exios66/mapreduce-reloaded/go/internal/wal"
)

func logFailureSpec(t *testing.T) *pb.JobSpec {
	t.Helper()
	in := filepath.Join(t.TempDir(), "in.txt")
	if err := os.WriteFile(in, []byte("a b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return &pb.JobSpec{Program: "wordcount", Inputs: []string{in}, Output: t.TempDir(), NumReduceTasks: 1}
}

// Every source of a failed record must stop even RPCs that do not append.
// A log supplied without a path cannot be healed by the server.
func TestLogFailureWithoutPathStopsEveryRPC(t *testing.T) {
	for _, source := range []string{"submission", "assignment", "report", "expiration", "encoding"} {
		t.Run(source, func(t *testing.T) {
			now := time.Unix(1000, 0)
			log := &memLog{}
			s := NewServer(Config{Log: log, WorkerTimeout: time.Minute, Now: func() time.Time { return now }})
			w := register(t, s)
			spec := logFailureSpec(t)
			job := "job-1"
			if source != "submission" {
				sub, err := s.SubmitJob(ctx, &pb.SubmitJobRequest{Job: spec})
				if err != nil {
					t.Fatal(err)
				}
				job = sub.GetJobId()
			}
			var attempt *pb.TaskAttemptId
			if source == "report" || source == "encoding" {
				attempt = next(t, s, w)
			}
			if source != "encoding" {
				log.err = errors.New("disk full")
			}
			var err error
			switch source {
			case "submission":
				_, err = s.SubmitJob(ctx, &pb.SubmitJobRequest{Job: spec})
			case "assignment":
				_, err = s.RequestTask(ctx, &pb.RequestTaskRequest{WorkerId: w})
			case "report", "encoding":
				message := "worker failed"
				if source == "encoding" {
					message = "invalid UTF-8: \xff"
				}
				_, err = s.ReportTask(ctx, &pb.ReportTaskRequest{WorkerId: w, Attempt: attempt,
					Outcome: &pb.ReportTaskRequest_Failure{Failure: &pb.TaskFailure{Message: message}}})
			case "expiration":
				if expired := s.Expire(now.Add(2 * time.Minute)); !slices.Equal(expired, []string{w}) {
					t.Fatalf("expired %v; want %s", expired, w)
				}
			}
			if source != "expiration" {
				assertCode(t, err, codes.Unavailable)
			}
			records := len(log.records)
			workers := len(s.Status().Workers)
			calls := []struct {
				name string
				call func() error
			}{
				{"RegisterWorker", func() error {
					_, err := s.RegisterWorker(ctx, &pb.RegisterWorkerRequest{Worker: &pb.WorkerInfo{}})
					return err
				}},
				{"Heartbeat", func() error {
					_, err := s.Heartbeat(ctx, &pb.HeartbeatRequest{WorkerId: w})
					return err
				}},
				{"SubmitJob", func() error {
					_, err := s.SubmitJob(ctx, &pb.SubmitJobRequest{Job: spec})
					return err
				}},
				{"RequestTask", func() error {
					_, err := s.RequestTask(ctx, &pb.RequestTaskRequest{WorkerId: w})
					return err
				}},
				{"ReportTask", func() error {
					_, err := s.ReportTask(ctx, &pb.ReportTaskRequest{WorkerId: w, Attempt: attempt})
					return err
				}},
				{"GetJobStatus", func() error {
					_, err := s.GetJobStatus(ctx, &pb.GetJobStatusRequest{JobId: job})
					return err
				}},
			}
			for round := range 2 {
				if round > 0 {
					now = now.Add(24 * time.Hour)
				}
				for _, call := range calls {
					t.Run(fmt.Sprintf("round%d/%s", round, call.name), func(t *testing.T) {
						err := call.call()
						assertCode(t, err, codes.Unavailable)
						if !strings.Contains(status.Convert(err).Message(), "restart the coordinator") {
							t.Fatalf("error %v; want restart guidance for a log without a path", err)
						}
					})
				}
			}
			if len(log.records) != records || len(s.Status().Workers) != workers {
				t.Fatal("refused RPCs changed the log or registered a worker")
			}
		})
	}
}

// The RPC that notices a repaired log continues against the recovered
// state, including validation of worker IDs that recovery invalidated.
func TestEveryRPCTriggersLogHealing(t *testing.T) {
	for _, method := range []string{"RegisterWorker", "Heartbeat", "SubmitJob", "RequestTask", "ReportTask", "GetJobStatus"} {
		t.Run(method, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "coordinator.log")
			s, log, err := Recover(Config{}, path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.Close() })
			w := register(t, s)
			spec := logFailureSpec(t)
			sub, err := s.SubmitJob(ctx, &pb.SubmitJobRequest{Job: spec})
			if err != nil {
				t.Fatal(err)
			}
			attempt := next(t, s, w)
			if err := log.Close(); err != nil {
				t.Fatal(err)
			}
			_, err = s.ReportTask(ctx, &pb.ReportTaskRequest{WorkerId: w, Attempt: attempt,
				Outcome: &pb.ReportTaskRequest_Success{Success: &pb.TaskSuccess{Outputs: outputs(0, 1)}}})
			assertCode(t, err, codes.Unavailable)

			switch method {
			case "RegisterWorker":
				if got := register(t, s); got != "r2-w1" {
					t.Fatalf("worker ID %s; want r2-w1", got)
				}
			case "Heartbeat":
				hb, err := s.Heartbeat(ctx, &pb.HeartbeatRequest{WorkerId: w})
				if err != nil || !hb.GetReregister() {
					t.Fatalf("heartbeat after healing: %v, %v; want reregister", hb, err)
				}
			case "SubmitJob":
				_, err := s.SubmitJob(ctx, &pb.SubmitJobRequest{Job: spec})
				assertCode(t, err, codes.FailedPrecondition)
			case "RequestTask":
				_, err := s.RequestTask(ctx, &pb.RequestTaskRequest{WorkerId: w})
				assertCode(t, err, codes.NotFound)
			case "ReportTask":
				if succeed(t, s, w, attempt, outputs(0, 1)) {
					t.Fatal("accepted a report from before healing")
				}
			case "GetJobStatus":
				jobStatus(t, s, sub.GetJobId())
			}
			if st := jobStatus(t, s, sub.GetJobId()); st.GetState() != pb.JobState_JOB_STATE_MAPPING || st.GetMapTasksCompleted() != 0 {
				t.Fatalf("healed status %v; want the unlogged map completion dropped", st)
			}
		})
	}
}

// Both an inaccessible log and a log whose records cannot be replayed
// leave the coordinator stopped. Retry timing uses the supplied clock,
// including a clock moving backward and the exact retry boundary.
func TestLogHealingRetryBoundary(t *testing.T) {
	for _, failure := range []string{"open", "replay"} {
		t.Run(failure, func(t *testing.T) {
			now := time.Unix(1000, 0)
			path := filepath.Join(t.TempDir(), "coordinator.log")
			s, log, err := Recover(Config{Now: func() time.Time { return now }}, path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.Close() })
			w := register(t, s)
			sub, err := s.SubmitJob(ctx, &pb.SubmitJobRequest{Job: logFailureSpec(t)})
			if err != nil {
				t.Fatal(err)
			}
			if err := log.Close(); err != nil {
				t.Fatal(err)
			}
			_, err = s.RequestTask(ctx, &pb.RequestTaskRequest{WorkerId: w})
			assertCode(t, err, codes.Unavailable)
			original, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			switch failure {
			case "open":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(path, 0o755); err != nil {
					t.Fatal(err)
				}
			case "replay":
				broken, err := wal.Open(path, func([]byte) error { return nil })
				if err != nil {
					t.Fatal(err)
				}
				// A valid WAL frame containing an invalid protobuf must fail
				// recovery, rather than being discarded as a torn tail.
				appendErr := broken.Append([]byte{0xff})
				closeErr := broken.Close()
				if err := errors.Join(appendErr, closeErr); err != nil {
					t.Fatal(err)
				}
			}
			_, err = s.GetJobStatus(ctx, &pb.GetJobStatusRequest{JobId: sub.GetJobId()})
			assertCode(t, err, codes.Unavailable)
			if !strings.Contains(status.Convert(err).Message(), "retrying") {
				t.Fatalf("error %v; want retry guidance", err)
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, original, 0o644); err != nil {
				t.Fatal(err)
			}
			deadline := now.Add(healEvery)
			for _, at := range []time.Time{now.Add(-time.Hour), deadline.Add(-time.Nanosecond)} {
				now = at
				_, err := s.GetJobStatus(ctx, &pb.GetJobStatusRequest{JobId: sub.GetJobId()})
				assertCode(t, err, codes.Unavailable)
			}
			now = deadline
			if st := jobStatus(t, s, sub.GetJobId()); st.GetState() != pb.JobState_JOB_STATE_MAPPING {
				t.Fatalf("status at retry boundary %v; want mapping", st)
			}
			if got := register(t, s); got != "r2-w1" {
				t.Fatalf("worker ID %s; failed recoveries must not count as restarts", got)
			}
		})
	}
}

// Healing preserves durable progress, counters and timestamps, discards
// an unlogged assignment, and preserves the server's shutdown instruction.
// A second failure exercises replacement of an already replaced log.
func TestRepeatedLogHealingPreservesDurableState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "coordinator.log")
	now := time.Unix(1000, 0)
	s, log, err := Recover(Config{Now: func() time.Time { return now }}, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	w := register(t, s)
	spec := logFailureSpec(t)
	spec.NumReduceTasks = 2
	sub, err := s.SubmitJob(ctx, &pb.SubmitJobRequest{Job: spec})
	if err != nil {
		t.Fatal(err)
	}
	succeed(t, s, w, next(t, s, w), outputs(0, 2))
	succeed(t, s, w, next(t, s, w), nil)
	want := jobStatus(t, s, sub.GetJobId())
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = s.RequestTask(ctx, &pb.RequestTaskRequest{WorkerId: w})
	assertCode(t, err, codes.Unavailable)
	now = now.Add(time.Hour)
	if got := jobStatus(t, s, sub.GetJobId()); !proto.Equal(got, want) {
		t.Fatalf("healed status %v; want durable status %v", got, want)
	}
	w = register(t, s)
	if w != "r2-w1" {
		t.Fatalf("worker ID %s; want r2-w1", w)
	}
	retry := next(t, s, w)
	if retry.GetTask().GetKind() != pb.TaskKind_TASK_KIND_REDUCE || retry.GetTask().GetIndex() != 1 || retry.GetAttempt() != 0 {
		t.Fatalf("retry %v; want reduce 1, attempt 0: the failed assignment was never logged", retry)
	}
	// Close must close the replacement log, not the original closed handle.
	if err := s.Close(); err != nil {
		t.Fatalf("close healed server: %v", err)
	}
	_, err = s.ReportTask(ctx, &pb.ReportTaskRequest{WorkerId: w, Attempt: retry,
		Outcome: &pb.ReportTaskRequest_Success{Success: &pb.TaskSuccess{}}})
	assertCode(t, err, codes.Unavailable)
	s.Shutdown()
	if got := jobStatus(t, s, sub.GetJobId()); !proto.Equal(got, want) {
		t.Fatalf("status after second healing %v; want %v", got, want)
	}
	w = register(t, s)
	if w != "r3-w1" {
		t.Fatalf("worker ID %s; want r3-w1", w)
	}
	resp, err := s.RequestTask(ctx, &pb.RequestTaskRequest{WorkerId: w})
	if err != nil || resp.GetShutdown() == nil {
		t.Fatalf("request after healing during shutdown: %v, %v; want shutdown", resp, err)
	}
}

type closingLog struct {
	memLog
	closeErr error
	closed   int
}

func (l *closingLog) Close() error {
	l.closed++
	return l.closeErr
}

func TestServerCloseLog(t *testing.T) {
	for _, log := range []Log{nil, &memLog{}} {
		if err := NewServer(Config{Log: log}).Close(); err != nil {
			t.Fatalf("Close with log %T: %v; want nil without a closer", log, err)
		}
	}
	for _, closeErr := range []error{nil, errors.New("close failed")} {
		log := &closingLog{closeErr: closeErr}
		err := NewServer(Config{Log: log}).Close()
		if !errors.Is(err, closeErr) || log.closed != 1 {
			t.Fatalf("Close = %v, calls = %d; want %v and one call", err, log.closed, closeErr)
		}
	}
}
