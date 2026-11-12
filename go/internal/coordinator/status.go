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
	// Attempts is each job's try, up to maxAttempts. History comes from the
	// running server's memory: a coordinator restart loses what its write-ahead
	// log held for assignments, on which history would be rebuilt.
	Attempts map[string][]AttemptRange
}

// WorkerStatus is a live worker and the tasks it is running, with the
// fraction of each one's input the worker last reported processed.
type WorkerStatus struct {
	Worker
	Running  []Task
	Progress map[Task]float32
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
	st.Attempts = map[string][]AttemptRange{}
	for _, j := range s.order {
		st.Jobs = append(st.Jobs, j.status())
		st.Attempts[j.id] = append([]AttemptRange(nil), j.attempts...)
	}
	var running map[string][]Task
	if s.active != nil {
		running = s.active.tasks.Running()
	}
	for _, w := range s.workers.All() {
		ws := WorkerStatus{Worker: w, Running: running[w.ID]}
		if len(ws.Running) > 0 {
			ws.Progress = map[Task]float32{}
			for _, t := range ws.Running {
				ws.Progress[t] = s.active.tasks.Fraction(t, w.ID)
			}
		}
		st.Workers = append(st.Workers, ws)
	}
	return st
}
