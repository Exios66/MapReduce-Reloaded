package wire

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	pb "github.com/exios66/mapreduce-reloaded/go/gen/mapreduce/v1"
	"github.com/exios66/mapreduce-reloaded/go/internal/formats"
	"github.com/exios66/mapreduce-reloaded/go/pkg/api"

	_ "github.com/exios66/mapreduce-reloaded/go/examples/wordcount"
)

func init() {
	p, _ := api.Lookup("wordcount")
	p.Partitioner = api.PartitionFunc(func([]byte, uint32) uint32 { return 0 })
	api.Register("test.partitioned", p)
}

func TestJobSpec(t *testing.T) {
	m := &pb.JobSpec{
		Program:        "wordcount",
		Inputs:         []string{"file:///data/a.txt", "rel/*.txt"},
		Output:         "file:///out",
		SplitSizeBytes: 100,
		NumReduceTasks: 4,
		UseCombiner:    true,
		Params:         map[string]string{"k": "v"},

		OutputFormat:      "kv",
		IntermediateCodec: "zstd",
		OutputCodec:       "snappy",
	}
	spec, err := JobSpec(m)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Inputs[0] != filepath.FromSlash("/data/a.txt") || spec.Inputs[1] != "rel/*.txt" ||
		spec.Output != filepath.FromSlash("/out") || spec.SplitSize != 100 || spec.R != 4 ||
		spec.Params["k"] != "v" || spec.Program.Combiner == nil ||
		spec.OutputFormat != "kv" || spec.IntermediateCodec != "zstd" || spec.OutputCodec != "snappy" {
		t.Fatalf("got %+v", spec)
	}

	m.UseCombiner = false
	if spec, _ := JobSpec(m); spec.Program.Combiner != nil {
		t.Fatal("combiner kept with use_combiner false")
	}
}

// A job with program_source runs that job file; program only names it.
func TestJobFile(t *testing.T) {
	src, err := os.ReadFile("../../../examples/wordcount.go")
	if err != nil {
		t.Fatal(err)
	}
	m := &pb.JobSpec{
		Program: "wordcount.go", ProgramSource: string(src), Inputs: []string{"in"}, Output: "o",
		NumReduceTasks: 1, UseCombiner: true,
	}
	spec, err := JobSpec(m)
	if err != nil || spec.Program.Mapper == nil || spec.Program.Combiner == nil {
		t.Fatalf("got %+v, %v", spec.Program, err)
	}
	m.UseCombiner = false
	if spec, _ := JobSpec(m); spec.Program.Combiner != nil {
		t.Fatal("combiner kept with use_combiner false")
	}
	m.ProgramSource = "package job"
	if _, err := JobSpec(m); err == nil || !strings.Contains(err.Error(), "no Map") {
		t.Fatalf("a job file without Map: got %v", err)
	}
}

// Source takes precedence even when the display name names a registered
// program. Disabling optional callbacks in one request must not mutate the
// cached program used by the next request.
func TestJobFileOverridesAndCacheIsolation(t *testing.T) {
	src, err := os.ReadFile("../../../examples/wordcount.go")
	if err != nil {
		t.Fatal(err)
	}
	m := &pb.JobSpec{
		Program: "wordcount", ProgramSource: string(src) + "\nfunc Partition(key []byte, r uint32) uint32 { return r-1 }\n",
		Inputs: []string{"in"}, Output: "out", NumReduceTasks: 3,
	}
	for _, tc := range []struct {
		name, partitioner string
		combiner, custom  bool
	}{
		{"disabled", "hash", false, false},
		{"source defaults", "", true, true},
		{"disable only combiner", "", false, true},
		{"disable only partitioner", "hash", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m.UseCombiner, m.Partitioner = tc.combiner, tc.partitioner
			spec, err := JobSpec(m)
			if err != nil {
				t.Fatal(err)
			}
			if (spec.Program.Combiner != nil) != tc.combiner || (spec.Program.Partitioner != nil) != tc.custom {
				t.Fatalf("program options=%+v", spec.Program)
			}
			if tc.custom && spec.Program.Partitioner.Partition([]byte("key"), 3) != 2 {
				t.Fatal("custom partitioner was not used")
			}
		})
	}
	m.ProgramSource = "package job" // must not fall back to the registered wordcount
	if _, err := JobSpec(m); err == nil || !strings.Contains(err.Error(), "wire: wordcount: job file: it has no Map") {
		t.Fatalf("invalid source not identified: %v", err)
	}
}

func TestPartitioner(t *testing.T) {
	m := &pb.JobSpec{Program: "test.partitioned", Inputs: []string{"in"}, Output: "o", NumReduceTasks: 1}
	for name, custom := range map[string]bool{"": true, "hash": false} {
		m.Partitioner = name
		if spec, err := JobSpec(m); err != nil || (spec.Program.Partitioner != nil) != custom {
			t.Errorf("partitioner %q: got %+v, %v; want the program's own: %v", name, spec.Program.Partitioner, err, custom)
		}
	}
}

func TestJobSpecErrors(t *testing.T) {
	for want, m := range map[string]*pb.JobSpec{
		"unknown program":     {Program: "nope"},
		"unknown partitioner": {Program: "wordcount", Partitioner: "range"},
		"unsupported URI":     {Program: "wordcount", Inputs: []string{"gs://b/k"}, Output: "o", NumReduceTasks: 1},
		"R must be":           {Program: "wordcount", Inputs: []string{"in"}, Output: "o"},
		"unknown format":      {Program: "wordcount", Inputs: []string{"in"}, Output: "o", NumReduceTasks: 1, OutputFormat: "parquet"},
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
	for _, uri := range []string{"gs://b/k", "s3:b/k", "file://host/a.txt"} {
		if _, err := Split(&pb.InputSplit{Uri: uri}); err == nil {
			t.Fatalf("%s accepted", uri)
		}
	}
	s3 := formats.Split{Path: "s3://b/in/a.txt", Start: 10, End: 25}
	if m, err := InputSplit(s3); err != nil || m.Uri != s3.Path {
		t.Fatalf("InputSplit = %v, %v", m, err)
	} else if got, err := Split(m); err != nil || got != s3 {
		t.Fatalf("Split = %+v, %v", got, err)
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
