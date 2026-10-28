package worker

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"testing/iotest"

	"google.golang.org/protobuf/proto"

	pb "github.com/exios66/mapreduce-reloaded/go/gen/mapreduce/v1"
	"github.com/exios66/mapreduce-reloaded/go/internal/formats/kv"
	"github.com/exios66/mapreduce-reloaded/go/internal/task"
)

// A failure report says which record crashed the user's code, which map
// output could not be fetched, or that no attempt of the job can run,
// however deeply the error is wrapped.
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
		{fmt.Errorf("map: %w", &fatalError{errors.New("bad job")}), &pb.TaskFailure{Fatal: true}},
	} {
		got := w.report(&pb.TaskAttemptId{}, slog.New(slog.DiscardHandler), nil, nil, tc.err).GetFailure()
		tc.want.Message = tc.err.Error()
		if !proto.Equal(got, tc.want) {
			t.Errorf("report(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

// A fetched map output that turns out to be damaged is lost, as one that is
// gone, so that its map task runs again; otherwise the reduce task fails on
// it every time. One from shared storage, or an error that is not damage,
// is not.
func TestDamagedOutputIsLost(t *testing.T) {
	fetched := &pb.PartitionLocation{MapIndex: 2, WorkerAddress: "w1:1"}
	stored := &pb.PartitionLocation{MapIndex: 2, Uri: "file:///out/m2"}
	for _, tc := range []struct {
		src  *pb.PartitionLocation
		in   io.Reader
		lost bool
	}{
		{fetched, strings.NewReader("not an mrkv file at all"), true},
		{stored, strings.NewReader("not an mrkv file at all"), false},
		{fetched, iotest.ErrReader(errors.New("boom")), false},
	} {
		r := checked(kv.NewReader(tc.in), tc.src)
		for r.Next() {
		}
		var lost *lostOutput
		if got := errors.As(r.Err(), &lost) && proto.Equal(lost.src, tc.src); got != tc.lost {
			t.Errorf("%v from %v: lost %v, want %v", r.Err(), tc.src, got, tc.lost)
		}
	}
}

// A job that cannot be loaded, such as a job file that does not compile,
// fails its map and reduce attempts as fatal (#176).
func TestBadJobIsFatal(t *testing.T) {
	w := New(nil, "", nil)
	job := &pb.JobSpec{Program: "wc.go", ProgramSource: "package main\n\nfunc Map("}
	log := slog.New(slog.DiscardHandler)
	var fatal *fatalError
	if _, _, err := w.runMap(t.Context(), &pb.MapTask{Job: job}, log); !errors.As(err, &fatal) {
		t.Errorf("map: %v; want a fatal error", err)
	}
	if _, err := w.runReduce(t.Context(), &pb.ReduceTask{Job: job}, log); !errors.As(err, &fatal) {
		t.Errorf("reduce: %v; want a fatal error", err)
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
