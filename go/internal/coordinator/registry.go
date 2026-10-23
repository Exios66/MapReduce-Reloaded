package coordinator

import (
	"fmt"
	"slices"
	"time"

	pb "github.com/exios66/mapreduce-reloaded/go/gen/mapreduce/v1"
)

// Worker is a registered worker.
type Worker struct {
	ID       string
	Info     *pb.WorkerInfo // shuffle address, host, zone and task slots
	LastSeen time.Time
}

// Registry tracks the live workers. A worker that misses heartbeats for too
// long is expired and forgotten (§3.3); if it comes back, it registers again
// under a new ID, so nothing it reports under the old one is trusted.
type Registry struct {
	workers map[string]*Worker
	prefix  string
	next    int
}

// NewRegistry returns an empty registry whose worker IDs are prefix
// followed by a number.
func NewRegistry(prefix string) *Registry {
	return &Registry{workers: map[string]*Worker{}, prefix: prefix}
}

// Register adds a worker seen at now and returns its new ID.
func (r *Registry) Register(info *pb.WorkerInfo, now time.Time) string {
	r.next++
	id := fmt.Sprintf("%s%d", r.prefix, r.next)
	r.workers[id] = &Worker{ID: id, Info: info, LastSeen: now}
	return id
}

// Heartbeat records that worker id was seen at now. It returns false if the
// worker is unknown, which tells it to register again.
func (r *Registry) Heartbeat(id string, now time.Time) bool {
	w, ok := r.workers[id]
	if ok {
		w.LastSeen = now
	}
	return ok
}

// Lookup returns the live worker with the given ID.
func (r *Registry) Lookup(id string) (Worker, bool) {
	w, ok := r.workers[id]
	if !ok {
		return Worker{}, false
	}
	return *w, true
}

// Expire removes the workers not seen for longer than timeout and returns
// their IDs, sorted.
func (r *Registry) Expire(now time.Time, timeout time.Duration) []string {
	var expired []string
	for id, w := range r.workers {
		if now.Sub(w.LastSeen) > timeout {
			expired = append(expired, id)
			delete(r.workers, id)
		}
	}
	slices.Sort(expired)
	return expired
}

// Len returns the number of live workers.
func (r *Registry) Len() int { return len(r.workers) }
