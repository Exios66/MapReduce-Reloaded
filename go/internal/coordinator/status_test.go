package coordinator

import (
	"slices"
	"testing"
	"time"

	pb "github.com/exios66/mapreduce-reloaded/go/gen/mapreduce/v1"
)

// Status lists the jobs, the live workers with what they run, and the lost
// workers with the tasks that had to run again.
func TestStatus(t *testing.T) {
	t.Parallel()
	s, a, id := newJobWith(t, Config{WorkerTimeout: time.Minute}, 1)
	b := register(t, s)
	m0 := Task{pb.TaskKind_TASK_KIND_MAP, 0}
	next(t, s, a)

	st := s.Status()
	if len(st.Jobs) != 1 || st.Jobs[0].GetJobId() != id || st.Jobs[0].GetState() != pb.JobState_JOB_STATE_MAPPING {
		t.Fatalf("jobs %v; want %s mapping", st.Jobs, id)
	}
	if len(st.Workers) != 2 || st.Workers[0].ID != a || !slices.Equal(st.Workers[0].Running, []Task{m0}) ||
		st.Workers[1].ID != b || st.Workers[1].Running != nil {
		t.Fatalf("workers %+v; want %s running %v, then %s idle", st.Workers, a, m0, b)
	}

	s.active.tasks.SetFraction(m0, 0, a, 0.5, time.Now())
	if st := s.Status(); st.Workers[0].Progress[m0] != 0.5 || st.Workers[1].Progress != nil {
		t.Fatalf("progress %v, %v; want %s at 0.5 and %s none", st.Workers[0].Progress, st.Workers[1].Progress, a, b)
	}

	lostAt := time.Now().Add(90 * time.Second)
	s.workers.Heartbeat(b, lostAt.Add(-time.Second))
	s.Expire(lostAt)
	st = s.Status()
	if len(st.Workers) != 1 || st.Workers[0].ID != b {
		t.Fatalf("workers %+v; want only %s", st.Workers, b)
	}
	if len(st.Lost) != 1 || st.Lost[0].ID != a || !st.Lost[0].At.Equal(lostAt) || !slices.Equal(st.Lost[0].Tasks, []Task{m0}) {
		t.Fatalf("lost %+v; want %s, running %v", st.Lost, a, m0)
	}
}
