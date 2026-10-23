package wire

import (
	"path/filepath"
	"strings"
	"testing"

	pb "github.com/exios66/mapreduce-reloaded/go/gen/mapreduce/v1"
	"github.com/exios66/mapreduce-reloaded/go/internal/formats"
	"github.com/exios66/mapreduce-reloaded/go/pkg/api"

	_ "github.com/exios66/mapreduce-reloaded/go/examples/wordcount"
)

func TestJobSpec(t *testing.T) {
	m := &pb.JobSpec{
		Program:        "wordcount",
		Inputs:         []string{"file:///data/a.txt", "rel/*.txt"},
		Output:         "file:///out",
		SplitSizeBytes: 100,
		NumReduceTasks: 4,
		UseCombiner:    true,
		Params:         map[string]string{"k": "v"},
	}
	spec, err := JobSpec(m)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Inputs[0] != filepath.FromSlash("/data/a.txt") || spec.Inputs[1] != "rel/*.txt" ||
		spec.Output != filepath.FromSlash("/out") || spec.SplitSize != 100 || spec.R != 4 ||
		spec.Params["k"] != "v" || spec.Program.Combiner == nil {
		t.Fatalf("got %+v", spec)
	}

	m.UseCombiner = false
	if spec, _ := JobSpec(m); spec.Program.Combiner != nil {
		t.Fatal("combiner kept with use_combiner false")
	}
}

func TestJobSpecErrors(t *testing.T) {
	for want, m := range map[string]*pb.JobSpec{
		"unknown program":     {Program: "nope"},
		"unknown partitioner": {Program: "wordcount", Partitioner: "range"},
		"unsupported URI":     {Program: "wordcount", Inputs: []string{"s3://b/k"}, Output: "o", NumReduceTasks: 1},
		"R must be":           {Program: "wordcount", Inputs: []string{"in"}, Output: "o"},
	} {
		if _, err := JobSpec(m); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("got %v, want an error containing %q", err, want)
		}
	}
}

func TestSplitRoundTrip(t *testing.T) {
	s := formats.Split{Path: "in/a.txt", Start: 10, End: 25}
	m, err := InputSplit(s)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(m.Uri, "file:///") || m.Offset != 10 || m.Length != 15 {
		t.Fatalf("InputSplit = %v", m)
	}
	got, err := Split(m)
	abs, _ := filepath.Abs(s.Path)
	if err != nil || got != (formats.Split{Path: abs, Start: 10, End: 25}) {
		t.Fatalf("Split = %+v, %v", got, err)
	}
	if _, err := Split(&pb.InputSplit{Uri: "s3://b/k"}); err == nil {
		t.Fatal("s3 URI accepted")
	}
	if _, err := Path("file://%zz"); err == nil {
		t.Fatal("malformed URI accepted")
	}
}

func TestTaskID(t *testing.T) {
	got, err := TaskID(&pb.TaskAttemptId{
		Task:    &pb.TaskId{Kind: pb.TaskKind_TASK_KIND_REDUCE, Index: 3},
		Attempt: 2,
	})
	if err != nil || got != (api.TaskID{Kind: api.ReduceTask, Index: 3, Attempt: 2}) {
		t.Fatalf("TaskID = %+v, %v", got, err)
	}
	if got, _ := TaskID(&pb.TaskAttemptId{Task: &pb.TaskId{Kind: pb.TaskKind_TASK_KIND_MAP}}); got.Kind != api.MapTask {
		t.Fatalf("map kind = %v", got.Kind)
	}
	if _, err := TaskID(&pb.TaskAttemptId{}); err == nil {
		t.Fatal("unspecified kind accepted")
	}
}
