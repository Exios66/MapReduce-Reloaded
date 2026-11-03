package coordinator

import (
	"time"

	pb "github.com/exios66/mapreduce-reloaded/go/gen/mapreduce/v1"
)

// maxAttempts is the most attempts Status keeps for one job: enough for any
// job the commands can submit, with room for backups and failures.
const maxAttempts = 512

// AttemptRange is one attempt of one task of one job, for the status page's
// task timeline: when the coordinator started it on its worker, and when it
// heard that it ended. The times come from the coordinator's clock, as
// Status's. Costs start when the coordinator accepts the assignment, not
// when the worker starts running it (§3.3).
type AttemptRange struct {
	Kind    pb.TaskKind
	Index   uint32
	Attempt uint32
	Worker  string
	Started time.Time
	Ended   time.Time // zero while the attempt is running
	Backup  bool      // started while another attempt of the task was running (§3.6)
	Ok      bool      // the coordinator accepted its completion report
}

// attempt records that the coordinator assigned an attempt at t now. Once a
// job reaches maxAttempts attempts, history drops its oldest attempt: the
// page's timeline loses its left edge instead of wrongly shifting.
func (j *job) attempt(t Task, attempt uint32, worker string, now time.Time, backup bool) {
	if len(j.attempts) == maxAttempts {
		copy(j.attempts, j.attempts[1:])
		j.attempts = j.attempts[:maxAttempts-1]
	}
	j.attempts = append(j.attempts, AttemptRange{
		Kind: t.Kind, Index: t.Index, Attempt: attempt, Worker: worker,
		Started: now, Backup: backup,
	})
}

// attemptEnded records the outcome of the job's open attempt at
// (t, attempt), which reports arrive in order (§4.9). A stale or invalid
// report has already been rejected by the time this is called. An attempt
// whose worker is lost without a report keeps its open end: the next
// attempt of the same task says the original task ran again.
func (j *job) attemptEnded(t Task, attempt uint32, at time.Time, ok bool) {
	for i := len(j.attempts) - 1; i >= 0; i-- {
		a := &j.attempts[i]
		if a.Kind == t.Kind && a.Index == t.Index && a.Attempt == attempt && a.Ended.IsZero() {
			a.Ended, a.Ok = at, ok
			return
		}
	}
}
