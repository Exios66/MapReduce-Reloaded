package statusweb

import (
	"bytes"
	"fmt"
	"maps"
	"slices"

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

	family("mr_job_counter", "Counters of each job's accepted attempts (§4.9).")
	for _, j := range st.Jobs {
		counts := j.GetCounters().GetValues()
		for _, name := range slices.Sorted(maps.Keys(counts)) {
			fmt.Fprintf(&b, "mr_job_counter{job=%s,name=%s} %d\n", promtext.Label(j.GetJobId()), promtext.Label(name), counts[name])
		}
	}
	return b.Bytes()
}
