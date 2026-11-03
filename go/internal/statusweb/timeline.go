package statusweb

import (
	"fmt"
	"time"

	pb "github.com/exios66/mapreduce-reloaded/go/gen/mapreduce/v1"
	"github.com/exios66/mapreduce-reloaded/go/internal/coordinator"
)

// attemptBar is one attempt's row of the status page's task timeline: its
// label, its place in the job's window (left and width, in percent), and
// the trace's classes, which the stylesheet colors.
type attemptBar struct {
	Label   string
	Left    float64
	Width   float64
	Backup  bool
	Trace   string // "map" or "reduce", with "open" while the attempt runs and "failed" if it did not complete
	Seconds string
}

// timeline lays the job's attempts out over the job's run: from its
// submission (or the first attempt's start) to its finish, or to now.
// Bars keep their proportions, so the drawn timeline is to scale. now is
// the status's At, for a job without a finish time.
func timeline(j *pb.GetJobStatusResponse, now time.Time, attempts map[string][]coordinator.AttemptRange) []attemptBar {
	rs := attempts[j.GetJobId()]
	if len(rs) == 0 {
		return nil
	}
	start := j.GetSubmittedAt().AsTime()
	if first := rs[0].Started; start.IsZero() || first.Before(start) {
		start = first
	}
	stop := j.GetFinishedAt().AsTime()
	if stop.IsZero() || !stop.After(start) {
		stop = now
	}
	if stop.IsZero() || !stop.After(start) {
		stop = start.Add(time.Second) // no window at all
	}
	window := float64(stop.Sub(start))
	out := make([]attemptBar, 0, len(rs))
	for _, a := range rs {
		end := a.Ended
		open := end.IsZero()
		if open {
			end = stop
		}
		kind := kindName(a.Kind)
		trace := kind
		switch {
		case open:
			trace += " open"
		case !a.Ok:
			trace += " failed"
		}
		out = append(out, attemptBar{
			Label:   fmt.Sprintf("%s %d #%d %s", kind, a.Index, a.Attempt, a.Worker),
			Left:    clampPct(float64(a.Started.Sub(start)) / window * 100),
			Width:   clampPct(float64(end.Sub(a.Started)) / window * 100),
			Backup:  a.Backup,
			Trace:   trace,
			Seconds: fmt.Sprintf("%.1f s", end.Sub(a.Started).Seconds()),
		})
	}
	return out
}

// clampPct keeps a percentage in 0..100.
func clampPct(p float64) float64 {
	return max(0, min(p, 100))
}
