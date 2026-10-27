package wire

import (
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	pb "github.com/exios66/mapreduce-reloaded/go/gen/mapreduce/v1"
)

// FuzzSplit converts any split a coordinator might send. Either it is
// rejected, or it describes a byte range of a file: 0 <= Start <= End
// (#152).
func FuzzSplit(f *testing.F) {
	f.Add("file:///data/in.txt", uint64(0), uint64(10))
	f.Add("s3://bucket/key", uint64(1<<40), uint64(5))
	f.Add("file:///x", uint64(1<<63), uint64(1))
	f.Add("file:///x", uint64(1<<62), uint64(1<<62))
	f.Fuzz(func(t *testing.T, uri string, offset, length uint64) {
		s, err := Split(&pb.InputSplit{Uri: uri, Offset: offset, Length: length})
		if err == nil && (s.Start < 0 || s.End < s.Start) {
			t.Fatalf("Split(%q, %d, %d) = [%d, %d)", uri, offset, length, s.Start, s.End)
		}
	})
}

// FuzzJobSpec converts any JobSpec message without a job file, which the
// jobfile package's tests cover, and must not panic.
func FuzzJobSpec(f *testing.F) {
	for _, m := range []*pb.JobSpec{
		{Program: "wordcount", Inputs: []string{"/in"}, Output: "/out", NumReduceTasks: 4},
		{Program: "grep", Partitioner: "hash", Params: map[string]string{"pattern": "x"}},
		{Program: "nope", Partitioner: "range"},
	} {
		b, err := proto.Marshal(m)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		var m pb.JobSpec
		if proto.Unmarshal(b, &m) != nil {
			return
		}
		m.ProgramSource = ""
		_, _ = JobSpec(&m)
	})
}

// FuzzTaskID converts any attempt ID: map and reduce kinds give the same
// index and attempt, and every other kind is rejected.
func FuzzTaskID(f *testing.F) {
	f.Add(int32(1), uint32(3), uint32(2))
	f.Add(int32(7), uint32(0), uint32(0))
	f.Fuzz(func(t *testing.T, kind int32, index, attempt uint32) {
		k := pb.TaskKind(kind)
		id, err := TaskID(&pb.TaskAttemptId{Task: &pb.TaskId{Kind: k, Index: index}, Attempt: attempt})
		known := k == pb.TaskKind_TASK_KIND_MAP || k == pb.TaskKind_TASK_KIND_REDUCE
		if (err == nil) != known {
			t.Fatalf("TaskID(kind %v): err %v", k, err)
		}
		if known && (id.Index != index || id.Attempt != attempt) {
			t.Fatalf("TaskID(%d, %d) = %+v", index, attempt, id)
		}
	})
}

// FuzzURIPath: a local path's URI names it again, made absolute.
func FuzzURIPath(f *testing.F) {
	for _, p := range []string{"in.txt", "/data/in.txt", "a b/%20#?.txt", "../x", ""} {
		f.Add(p)
	}
	f.Fuzz(func(t *testing.T, p string) {
		if strings.Contains(p, "://") || strings.ContainsRune(p, 0) {
			return // a URI is passed through; NUL is no path
		}
		uri, err := URI(p)
		if err != nil {
			return
		}
		got, err := Path(uri)
		if err != nil {
			t.Fatalf("Path(URI(%q) = %q): %v", p, uri, err)
		}
		want, _ := filepath.Abs(p)
		if got != want {
			t.Fatalf("Path(URI(%q)) = %q, want %q", p, got, want)
		}
	})
}
