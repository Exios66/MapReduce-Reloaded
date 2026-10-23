package worker

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"strings"
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
		got := w.report(&pb.TaskAttemptId{}, slog.New(slog.DiscardHandler), nil, nil, tc.err).GetFailure()
		tc.want.Message = tc.err.Error()
		if !proto.Equal(got, tc.want) {
			t.Errorf("report(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

// Messages about an attempt, the worker's and its user code's, carry the
// attempt's job and task.
func TestAttemptLogger(t *testing.T) {
	var b bytes.Buffer
	w := New(nil, "", nil)
	w.logger = slog.New(slog.NewJSONHandler(&b, nil))
	w.attemptLogger(&pb.TaskAttemptId{
		Task:    &pb.TaskId{JobId: "job-1", Kind: pb.TaskKind_TASK_KIND_REDUCE, Index: 3},
		Attempt: 1,
	}).Info("x")
	want := `"job":"job-1","task":{"kind":"reduce","index":3,"attempt":1}}`
	if !strings.HasSuffix(strings.TrimSpace(b.String()), want) {
		t.Fatalf("got %s, want suffix %s", b.String(), want)
	}
}
