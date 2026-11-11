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
