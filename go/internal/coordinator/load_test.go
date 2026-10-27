package coordinator

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	pb "github.com/exios66/mapreduce-reloaded/go/gen/mapreduce/v1"
)

// The coordinator holds one lock over all its state (ADR 0006). With 50
// workers asking for tasks at once, a job of 2,000 tiny map tasks and 100
// reduce tasks still finishes well within the limit. The test logs how long
// RequestTask took, which is mostly the wait for that lock (#157).
func TestLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("load test")
	}
	const workers, maps, reduces, limit = 50, 2000, 100, 30 * time.Second
	in := filepath.Join(t.TempDir(), "in.txt")
	if err := os.WriteFile(in, []byte(strings.Repeat("a b c d e f g\n", 4*maps)), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewServer(Config{})
	sub, err := s.SubmitJob(ctx, &pb.SubmitJobRequest{Job: &pb.JobSpec{
		Program: "wordcount", Inputs: []string{in}, Output: t.TempDir(), NumMapTasks: maps, NumReduceTasks: reduces,
	}})
	if err != nil {
		t.Fatal(err)
	}
	job := sub.GetJobId()
	if st := jobStatus(t, s, job); st.GetMapTasksTotal() != maps || st.GetReduceTasksTotal() != reduces {
		t.Fatalf("status %v; want %d maps and %d reduces", st, maps, reduces)
	}

	var mu sync.Mutex
	var waits []time.Duration
	done := func() bool { return jobStatus(t, s, job).GetState() == pb.JobState_JOB_STATE_SUCCEEDED }
	start := time.Now()
	var wg sync.WaitGroup
	for range workers {
		w := register(t, s)
		wg.Go(func() {
			var mine []time.Duration
			defer func() { mu.Lock(); waits = append(waits, mine...); mu.Unlock() }()
			for time.Since(start) < limit {
				before := time.Now()
				resp, err := s.RequestTask(ctx, &pb.RequestTaskRequest{WorkerId: w})
				mine = append(mine, time.Since(before))
				if err != nil {
					t.Error(err)
					return
				}
				var rep *pb.ReportTaskRequest
				switch {
				case resp.GetMapTask() != nil:
					id := resp.GetMapTask().GetAttempt()
					rep = &pb.ReportTaskRequest{WorkerId: w, Attempt: id, Outcome: &pb.ReportTaskRequest_Success{
						Success: &pb.TaskSuccess{Outputs: outputs(id.GetTask().GetIndex(), reduces)},
					}}
				case resp.GetReduceTask() != nil:
					rep = &pb.ReportTaskRequest{WorkerId: w, Attempt: resp.GetReduceTask().GetAttempt(), Outcome: &pb.ReportTaskRequest_Success{
						Success: &pb.TaskSuccess{},
					}}
				case done():
					return
				default: // waiting for the last map tasks
					time.Sleep(time.Millisecond)
					continue
				}
				if _, err := s.ReportTask(ctx, rep); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	wg.Wait()
	elapsed := time.Since(start)
	if !done() || elapsed > limit {
		t.Fatalf("job done %v after %v, limit %v: %v", done(), elapsed, limit, jobStatus(t, s, job))
	}
	slices.Sort(waits)
	at := func(q float64) time.Duration { return waits[int(q*float64(len(waits)-1))] }
	t.Logf("%d workers, %d maps, %d reduces: %v; %d RequestTask calls took p50 %v, p99 %v, max %v",
		workers, maps, reduces, elapsed.Round(time.Millisecond), len(waits), at(0.5), at(0.99), waits[len(waits)-1])
}
