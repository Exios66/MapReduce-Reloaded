package worker

import (
	"bytes"
	"fmt"
	"net/http"
	"sync/atomic"

	"github.com/exios66/mapreduce-reloaded/go/internal/promtext"
)

// The kinds of task a worker runs, as indexes into stats.
const (
	mapKind = iota
	reduceKind
)

var kinds = [...]string{mapKind: "map", reduceKind: "reduce"}

// stats counts what a worker has done since it started, across
// registrations.
type stats struct {
	running        [len(kinds)]atomic.Int64
	ok, failed     [len(kinds)]atomic.Uint64
	mapInputBytes  atomic.Uint64
	mapOutputBytes atomic.Uint64
	fetchedBytes   atomic.Uint64
	outputBytes    atomic.Uint64
	registrations  atomic.Uint64
	prunedJobs     atomic.Uint64 // jobs whose map output was deleted
	prunedBytes    atomic.Uint64
	diskWaits      atomic.Uint64 // times the worker stopped taking tasks
	diskFree       atomic.Uint64 // at the last check
}

// start counts a task of kind as running; done counts how it ended.
func (s *stats) start(kind int) (done func(error)) {
	s.running[kind].Add(1)
	return func(err error) {
		s.running[kind].Add(-1)
		if err != nil {
			s.failed[kind].Add(1)
		} else {
			s.ok[kind].Add(1)
		}
	}
}

// MetricsHandler serves the worker's Prometheus metrics at /metrics.
func (w *Worker) MetricsHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /metrics", func(rw http.ResponseWriter, _ *http.Request) {
		rw.Header().Set("Content-Type", promtext.ContentType)
		_, _ = rw.Write(w.stats.metrics())
	})
	return mux
}

// metrics renders s in the Prometheus text format.
func (s *stats) metrics() []byte {
	var b bytes.Buffer
	promtext.Family(&b, "mr_worker_tasks_total", "counter", "Task attempts finished, by kind and result.")
	for k, kind := range kinds {
		fmt.Fprintf(&b, "mr_worker_tasks_total{kind=%q,result=\"ok\"} %d\n", kind, s.ok[k].Load())
		fmt.Fprintf(&b, "mr_worker_tasks_total{kind=%q,result=\"failed\"} %d\n", kind, s.failed[k].Load())
	}
	promtext.Family(&b, "mr_worker_task_running", "gauge", "Task attempts running, by kind.")
	for k, kind := range kinds {
		fmt.Fprintf(&b, "mr_worker_task_running{kind=%q} %d\n", kind, s.running[k].Load())
	}
	for _, m := range []struct {
		name, help string
		v          *atomic.Uint64
	}{
		{"mr_worker_map_input_bytes_total", "Bytes of input splits mapped, counted when each map task ends.", &s.mapInputBytes},
		{"mr_worker_map_output_bytes_total", "Bytes of map output written.", &s.mapOutputBytes},
		{"mr_worker_shuffle_fetched_bytes_total", "Bytes of map output fetched from workers.", &s.fetchedBytes},
		{"mr_worker_reduce_output_bytes_total", "Bytes of keys and values reduced into the output, counted when each reduce task ends.", &s.outputBytes},
		{"mr_worker_registrations_total", "Registrations with the coordinator.", &s.registrations},
		{"mr_worker_pruned_jobs_total", "Ended jobs whose map output was deleted.", &s.prunedJobs},
		{"mr_worker_pruned_bytes_total", "Bytes of map output deleted after their job ended.", &s.prunedBytes},
		{"mr_worker_disk_waits_total", "Times the worker stopped taking tasks because its disk was nearly full.", &s.diskWaits},
	} {
		promtext.Family(&b, m.name, "counter", m.help)
		fmt.Fprintf(&b, "%s %d\n", m.name, m.v.Load())
	}
	promtext.Family(&b, "mr_worker_disk_free_bytes", "gauge", "Free bytes on the work directory's file system, at the last task request.")
	fmt.Fprintf(&b, "mr_worker_disk_free_bytes %d\n", s.diskFree.Load())
	return b.Bytes()
}
