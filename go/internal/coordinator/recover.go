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
// logs to path from then on, and reopens it the same way if it ever fails;
// the caller closes the server, or the returned log if it never failed.
// Logged submission and finish times are preserved; missing times stay unknown.
// It returns log open, read, decoding, or replay errors, or Unavailable if
// recording the restart fails, joined with any error closing the log.
func Recover(cfg Config, path string) (*Server, *wal.Log, error) {
	cfg.IDPrefix = ""
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
	// Clean up the ended jobs only now, when the log says which job runs:
	// a later job may write to the same output, and needs its TempDir.
	for _, j := range s.replayEnded {
		if s.active == nil || s.active.spec.GetOutput() != j.spec.GetOutput() {
			j.cleanup()
		}
	}
	s.replayEnded = nil
	s.cfg.Log = log
	s.compact() // replay may have forgotten jobs while the log was not set
	rec := &pb.LogRecord{Record: &pb.LogRecord_Restarted{Restarted: &pb.Restarted{}}}
	if err := s.record(rec, s.now()); err != nil {
		return nil, nil, errors.Join(err, log.Close())
	}
	_ = s.replay(rec) // cannot fail
	s.workers = NewRegistry(fmt.Sprintf("r%d-w", restarts+1))
	s.logPath = path
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
		_, ended, err := s.report(j, r.TaskReported, at)
		if err != nil {
			return fmt.Errorf("coordinator log: %w", err)
		}
		if ended { // the coordinator may have died before cleaning up
			s.replayEnded = append(s.replayEnded, j)
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
