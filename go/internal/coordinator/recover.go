package coordinator

import (
	"errors"
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"

	pb "github.com/exios66/mapreduce-reloaded/go/gen/mapreduce/v1"
	"github.com/exios66/mapreduce-reloaded/go/internal/wal"
)

// Recover returns a server that resumes from the write-ahead log at path,
// creating it if needed (§3.3, ADR 0007). It replays the decisions there,
// then logs a restart: the attempts that were running stop, and workers
// register again under IDs the old coordinator never gave out. The server
// logs to path from then on; the caller closes the returned log.
// Logged submission and finish times are preserved; missing times stay unknown.
// It returns log open, read, decoding, or replay errors, or Unavailable if
// recording the restart fails, joined with any error closing the log.
func Recover(cfg Config, path string) (*Server, *wal.Log, error) {
	s := NewServer(cfg)
	restarts := 0
	log, err := wal.Open(path, func(b []byte) error {
		rec := &pb.LogRecord{}
		if err := proto.Unmarshal(b, rec); err != nil {
			return err
		}
		if rec.GetRestarted() != nil {
			restarts++
		}
		return s.replay(rec)
	})
	if err != nil {
		return nil, nil, err
	}
	s.cfg.Log = log
	rec := &pb.LogRecord{Record: &pb.LogRecord_Restarted{Restarted: &pb.Restarted{}}}
	if err := s.record(rec, time.Now()); err != nil {
		return nil, nil, errors.Join(err, log.Close())
	}
	_ = s.replay(rec) // cannot fail
	s.workers = NewRegistry(fmt.Sprintf("r%d-w", restarts+1))
	return s, log, nil
}

// replay applies a logged decision as the handler that logged it did, at
// the time it was made.
// A missing timestamp leaves submission or finish times unknown. It returns
// an error for unknown records, inconsistent assignments, or invalid reports.
func (s *Server) replay(rec *pb.LogRecord) error {
	var at time.Time // zero in logs written before records had times
	if rec.GetAt() != nil {
		at = rec.GetAt().AsTime()
	}
	switch r := rec.GetRecord().(type) {
	case *pb.LogRecord_JobSubmitted:
		s.submit(r.JobSubmitted, at)
	case *pb.LogRecord_TaskAssigned:
		id := r.TaskAssigned.GetAttempt()
		if s.active == nil || s.active.id != id.GetTask().GetJobId() {
			return fmt.Errorf("coordinator log: %v assigned outside the running job", id)
		}
		t := Task{id.GetTask().GetKind(), id.GetTask().GetIndex()}
		attempt, err := s.active.tasks.Assign(t, r.TaskAssigned.GetWorkerId(), at)
		if err == nil && attempt != id.GetAttempt() {
			err = fmt.Errorf("replayed as attempt %d", attempt)
		}
		if err != nil {
			return fmt.Errorf("coordinator log: assigning %v: %w", id, err)
		}
		s.active.placed[t] = append(s.active.placed[t], r.TaskAssigned.GetLocality())
	case *pb.LogRecord_TaskReported:
		job := r.TaskReported.GetAttempt().GetTask().GetJobId()
		j, ok := s.jobs[job]
		if !ok {
			return fmt.Errorf("coordinator log: report for unknown job %q", job)
		}
		if _, err := s.report(j, r.TaskReported, at); err != nil {
			return fmt.Errorf("coordinator log: %w", err)
		}
	case *pb.LogRecord_WorkerLost:
		if s.active != nil {
			s.active.tasks.ResetWorker(r.WorkerLost.GetWorkerId())
		}
	case *pb.LogRecord_Restarted:
		if s.active != nil {
			s.active.tasks.StopAll()
		}
	default:
		return fmt.Errorf("coordinator log: unknown record %v", rec)
	}
	return nil
}
