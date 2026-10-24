package statusweb

import (
	"bytes"
	"fmt"
	"maps"
	"slices"
	"time"

	pb "github.com/exios66/mapreduce-reloaded/go/gen/mapreduce/v1"
	"github.com/exios66/mapreduce-reloaded/go/internal/coordinator"
	"github.com/exios66/mapreduce-reloaded/go/internal/promtext"
)

// metrics renders st in the Prometheus text format, version 0.0.4.
func metrics(st coordinator.Status) []byte {
	var b bytes.Buffer
	family := func(name, help string) { promtext.Family(&b, name, "gauge", help) }

	family("mr_workers", "Live workers.")
	fmt.Fprintf(&b, "mr_workers %d\n", len(st.Workers))

	family("mr_job_tasks", "Tasks of each job, by kind and state.")
	for _, j := range st.Jobs {
		for _, n := range []struct {
			kind, state string
			n           uint32
		}{
			{"map", "total", j.GetMapTasksTotal()},
			{"map", "completed", j.GetMapTasksCompleted()},
			{"reduce", "total", j.GetReduceTasksTotal()},
			{"reduce", "completed", j.GetReduceTasksCompleted()},
		} {
			fmt.Fprintf(&b, "mr_job_tasks{job=%s,kind=%q,state=%q} %d\n", promtext.Label(j.GetJobId()), n.kind, n.state, n.n)
		}
	}

	family("mr_job_duration_seconds", "Time since each job was submitted, until it finished.")
	for _, j := range st.Jobs {
		if d, ok := duration(j, st.At); ok {
			fmt.Fprintf(&b, "mr_job_duration_seconds{job=%s} %g\n", promtext.Label(j.GetJobId()), d.Seconds())
		}
	}

	family("mr_job_counter", "Counters of each job's accepted attempts (§4.9).")
	for _, j := range st.Jobs {
		counts := j.GetCounters().GetValues()
		for _, name := range slices.Sorted(maps.Keys(counts)) {
			fmt.Fprintf(&b, "mr_job_counter{job=%s,name=%s} %d\n", promtext.Label(j.GetJobId()), promtext.Label(name), counts[name])
		}
	}
	return b.Bytes()
}

// duration returns how long j has run, as of now if it has not finished.
// It returns false if the coordinator does not know when j was submitted,
// and 0 if the clock was set back since: the times are wall-clock times.
func duration(j *pb.GetJobStatusResponse, now time.Time) (time.Duration, bool) {
	if j.GetSubmittedAt() == nil {
		return 0, false
	}
	if j.GetFinishedAt() != nil {
		now = j.GetFinishedAt().AsTime()
	}
	return max(0, now.Sub(j.GetSubmittedAt().AsTime())), true
}
