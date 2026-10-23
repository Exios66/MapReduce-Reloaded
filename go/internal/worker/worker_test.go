package worker

import (
	"errors"
	"fmt"
	"testing"

	"google.golang.org/protobuf/proto"

	pb "github.com/exios66/mapreduce-reloaded/go/gen/mapreduce/v1"
	"github.com/exios66/mapreduce-reloaded/go/internal/task"
)

// A failure report says which record crashed the user's code, or which map
// output could not be fetched, however deeply the error is wrapped.
func TestReportFailure(t *testing.T) {
	w := New(nil, "", nil)
	src := &pb.PartitionLocation{MapIndex: 2, WorkerAddress: "w1:1"}
	for _, tc := range []struct {
		err  error
		want *pb.TaskFailure
	}{
		{errors.New("boom"), &pb.TaskFailure{}},
		{fmt.Errorf("map: %w", &task.CrashError{Record: 7}), &pb.TaskFailure{FailedRecord: proto.Uint64(7)}},
		{fmt.Errorf("reduce: %w", &lostOutput{src, errors.New("gone")}), &pb.TaskFailure{LostOutput: src}},
	} {
		got := w.report(&pb.TaskAttemptId{}, nil, nil, tc.err).GetFailure()
		tc.want.Message = tc.err.Error()
		if !proto.Equal(got, tc.want) {
			t.Errorf("report(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}
