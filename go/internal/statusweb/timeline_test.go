package statusweb

import (
	"testing"
	"time"

	pb "github.com/exios66/mapreduce-reloaded/go/gen/mapreduce/v1"
	"github.com/exios66/mapreduce-reloaded/go/internal/coordinator"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestTimelineBounds(t *testing.T) {
	for _, tc := range []struct {
		name        string
		submitted   *timestamppb.Timestamp
		finished    *timestamppb.Timestamp
		started     time.Time
		ended       time.Time
		left, width float64
		seconds     string
	}{
		{"missing submission", nil, nil, at.Add(-2 * time.Second), time.Time{}, 0, 100, "2.0 s"},
		{"past finish", timestamppb.New(at.Add(-10 * time.Second)), timestamppb.New(at), at.Add(-2 * time.Second), at.Add(time.Second), 80, 20, "3.0 s"},
		{"clock backwards", timestamppb.New(at.Add(-10 * time.Second)), nil, at.Add(-2 * time.Second), at.Add(-3 * time.Second), 80, 0, "0.0 s"},
		{"future start", timestamppb.New(at.Add(-10 * time.Second)), nil, at.Add(time.Second), time.Time{}, 100, 0, "0.0 s"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			j := &pb.GetJobStatusResponse{JobId: "j", SubmittedAt: tc.submitted, FinishedAt: tc.finished}
			bars := timeline(j, at, map[string][]coordinator.AttemptRange{"j": {{Started: tc.started, Ended: tc.ended}}})
			if len(bars) != 1 {
				t.Fatalf("bars = %v", bars)
			}
			b := bars[0]
			if b.Left != tc.left || b.Width != tc.width || b.Seconds != tc.seconds {
				t.Errorf("bar = %+v; want left %v, width %v, seconds %s", b, tc.left, tc.width, tc.seconds)
			}
		})
	}
}

func TestTimelineUnknownStart(t *testing.T) {
	j := &pb.GetJobStatusResponse{JobId: "j"}
	if got := timeline(j, at, map[string][]coordinator.AttemptRange{"j": {{}}}); len(got) != 0 {
		t.Fatalf("unknown start rendered as %v", got)
	}
}

func TestTimelineOutcomes(t *testing.T) {
	m := pb.TaskKind_TASK_KIND_MAP
	won := at.Add(-4 * time.Second)
	rs := []coordinator.AttemptRange{
		{Kind: m, Index: 0, Attempt: 0, Started: at.Add(-8 * time.Second), Ended: won, Outcome: coordinator.OutcomeSuperseded},
		{Kind: m, Index: 0, Attempt: 1, Started: at.Add(-6 * time.Second), Ended: won, Ok: true, Outcome: coordinator.OutcomeCompleted},
		{Kind: m, Index: 1, Attempt: 0, Started: at.Add(-8 * time.Second), Ended: won, Outcome: coordinator.OutcomeLost},
		{Kind: m, Index: 2, Attempt: 0, Started: at.Add(-2 * time.Second)},
	}
	for _, tc := range []struct {
		name     string
		finished *timestamppb.Timestamp
		states   []string
	}{
		{"running job", nil, []string{"superseded", "completed", "lost", "running"}},
		{"finished job", timestamppb.New(at), []string{"superseded", "completed", "lost", "abandoned"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			j := &pb.GetJobStatusResponse{JobId: "j", SubmittedAt: timestamppb.New(at.Add(-10 * time.Second)), FinishedAt: tc.finished}
			bars := timeline(j, at, map[string][]coordinator.AttemptRange{"j": rs})
			for i, b := range bars {
				if b.State != tc.states[i] {
					t.Errorf("bar %d state = %s; want %s", i, b.State, tc.states[i])
				}
			}
			if b := bars[0]; b.Width != 40 || b.Seconds != "4.0 s" || b.Trace != "map" {
				t.Errorf("superseded bar = %+v; want a plain bar to the winner's end", b)
			}
			if b := bars[2]; b.Trace != "map failed" {
				t.Errorf("lost bar trace = %q; want it outlined as failed", b.Trace)
			}
		})
	}
}
