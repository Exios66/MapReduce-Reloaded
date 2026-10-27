package coordinator

import (
	"time"

	pb "github.com/exios66/mapreduce-reloaded/go/gen/mapreduce/v1"
)

// maxLost is how many lost workers Status remembers.
const maxLost = 100

// Status is what the coordinator's status page shows (§4.8).
type Status struct {
	At      time.Time                  // when it was taken
	Jobs    []*pb.GetJobStatusResponse // every job, in the order submitted
	Workers []WorkerStatus             // the live workers, in the order they registered
	Lost    []LostWorker               // the last maxLost workers lost since the process started, oldest first
}

// WorkerStatus is a live worker and the tasks it is running.
type WorkerStatus struct {
	Worker
	Running []Task
}

// LostWorker is a worker declared dead (§3.3), and the tasks of the running
// job it was running or had completed, which had to run again.
type LostWorker struct {
	ID    string
	At    time.Time
	Tasks []Task
}

// Status returns a copy of the coordinator's state.
func (s *Server) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Status{At: s.now(), Lost: append([]LostWorker(nil), s.lost...)}
	for _, j := range s.order {
		st.Jobs = append(st.Jobs, j.status())
	}
	var running map[string][]Task
	if s.active != nil {
		running = s.active.tasks.Running()
	}
	for _, w := range s.workers.All() {
		st.Workers = append(st.Workers, WorkerStatus{Worker: w, Running: running[w.ID]})
	}
	return st
}
