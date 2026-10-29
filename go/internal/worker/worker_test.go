package worker

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
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
	if _, _, err := w.runMap(t.Context(), &pb.MapTask{Job: job}, log, nil); !errors.As(err, &fatal) {
		t.Errorf("map: %v; want a fatal error", err)
	}
	if _, err := w.runReduce(t.Context(), &pb.ReduceTask{Job: job}, log, nil); !errors.As(err, &fatal) {
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

// A heartbeat reports the current attempt's progress: never above 1, never
// less than before, starting at 0 for each attempt.
func TestCurrentProgress(t *testing.T) {
	cur := new(current)
	if got := cur.running(); got != nil {
		t.Fatalf("running between attempts: %v", got)
	}
	reported := func() float32 { return cur.running()[0].GetFractionDone() }
	first, second := &pb.TaskAttemptId{Attempt: 1}, &pb.TaskAttemptId{Attempt: 2}

	_, stop := cur.start(t.Context(), first)
	if got := reported(); got != 0 {
		t.Fatalf("new attempt reports %v, want 0", got)
	}
	for _, tc := range []struct {
		fraction float64
		want     float32
	}{
		{0.25, 0.25},
		{0.1, 0.25}, // never less than before
		{math.NaN(), 0.25},
		{-1, 0.25},
		{0.5, 0.5},
		{2, 1}, // clamped
		{math.Inf(1), 1},
		{0.75, 1},
	} {
		cur.advance(tc.fraction)
		if got := reported(); got != tc.want {
			t.Errorf("after advance(%v): reported %v, want %v", tc.fraction, got, tc.want)
		}
	}
	stop()
	if got := cur.running(); got != nil {
		t.Fatalf("running after the attempt: %v", got)
	}

	_, stop = cur.start(t.Context(), second)
	defer stop()
	if got := reported(); got != 0 {
		t.Errorf("next attempt starts at %v, want 0", got)
	}
}

// A reduce task's progress is the share of its sources' bytes read, and
// stays within 1 however much is read.
func TestReduceMeter(t *testing.T) {
	srcs := []*pb.PartitionLocation{{SizeBytes: 6}, {SizeBytes: 4}}
	var got []float64
	m := newMeter(srcs, func(f float64) { got = append(got, f) })
	a := m.wrap(io.NopCloser(strings.NewReader("123456")))
	b := m.wrap(io.NopCloser(strings.NewReader("abcd")))
	buf := make([]byte, 3)
	for _, r := range []io.Reader{a, b, a, b, b} {
		_, _ = r.Read(buf)
	}
	if want := []float64{0.3, 0.6, 0.9, 1, 1}; len(got) != len(want) {
		t.Fatalf("reported %v, want %v", got, want)
	} else {
		for i := range want {
			if math.Abs(got[i]-want[i]) > 1e-9 {
				t.Fatalf("reported %v, want %v", got, want)
			}
		}
	}

	r := io.NopCloser(strings.NewReader("x"))
	for _, m := range []*meter{newMeter(srcs, nil), newMeter([]*pb.PartitionLocation{{}}, func(float64) {})} {
		if m != nil || m.wrap(r) != r {
			t.Errorf("a meter with nothing to report: %v, want a nil one that passes reads through", m)
		}
	}
}
