package task

import (
	"errors"
	"io/fs"
	"iter"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/exios66/mapreduce-reloaded/go/examples/wordcount"
	"github.com/exios66/mapreduce-reloaded/go/internal/formats"
	"github.com/exios66/mapreduce-reloaded/go/pkg/api"
)

var errBoom = errors.New("boom")

// setup writes an input file and returns a word count spec over it, with
// output in a new directory, and the input's single split.
func setup(t *testing.T, text string) (api.JobSpec, formats.Split) {
	t.Helper()
	in := filepath.Join(t.TempDir(), "in.txt")
	if err := os.WriteFile(in, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	spec := api.JobSpec{
		Program: wordcount.Program,
		Inputs:  []string{in},
		Output:  filepath.Join(t.TempDir(), "out"),
		R:       2,
	}
	return spec, formats.Split{Path: in, End: int64(len(text))}
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestMapThenReduce(t *testing.T) {
	spec, split := setup(t, "a b a\nc\n")
	dir := t.TempDir()
	sizes, counts, err := Map(spec, api.TaskID{Kind: api.MapTask, Attempt: 3}, split, dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"map-00000-r-00000.kv", "map-00000-r-00001.kv"}; !slices.Equal(dirNames(t, dir), want) {
		t.Fatalf("map wrote %v, want %v", dirNames(t, dir), want)
	}
	for r, size := range sizes {
		info, err := os.Stat(MapOutput(dir, 0, r))
		if err != nil || info.Size() != size {
			t.Fatalf("partition %d: reported %d bytes, file has %v (%v)", r, size, info.Size(), err)
		}
	}
	if counts[MapInputRecords] != 2 || counts[MapOutputRecords] != 4 {
		t.Fatalf("map counters %v", counts)
	}

	total := int64(0)
	for r := range spec.R {
		sources, closeSources, err := OpenSources([]string{MapOutput(dir, 0, int(r))})
		if err != nil {
			t.Fatal(err)
		}
		counts, err := Reduce(spec, api.TaskID{Kind: api.ReduceTask, Index: r}, sources)
		if err := errors.Join(err, closeSources()); err != nil {
			t.Fatal(err)
		}
		total += counts[ReduceOutputRecords]
	}
	if total != 3 || len(dirNames(t, spec.Output)) != 2 {
		t.Fatalf("%d records in %v, want 3 in two part files", total, dirNames(t, spec.Output))
	}
}

// A failed attempt leaves no files, and does not disturb the output of an
// attempt that already succeeded.
func TestFailedAttemptLeavesNoFiles(t *testing.T) {
	spec, split := setup(t, "a b\n")
	dir := t.TempDir()
	failing := spec
	failing.Program.Mapper = api.MapFunc(func(api.Context, []byte, []byte, api.Emitter) error { return errBoom })
	if _, _, err := Map(failing, api.TaskID{Kind: api.MapTask}, split, dir); !errors.Is(err, errBoom) {
		t.Fatalf("Map error = %v", err)
	}
	if names := dirNames(t, dir); len(names) != 0 {
		t.Fatalf("failed map left %v", names)
	}

	// The first reduce attempt succeeds; a failing retry must leave its
	// output in place.
	if _, err := Reduce(spec, api.TaskID{Kind: api.ReduceTask}, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Map(spec, api.TaskID{Kind: api.MapTask}, split, dir); err != nil {
		t.Fatal(err)
	}
	sources, closeSources, err := OpenSources([]string{MapOutput(dir, 0, 0)})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = closeSources() }()
	failing.Program.Reducer = api.ReduceFunc(func(_ api.Context, key []byte, _ iter.Seq[[]byte], out api.Emitter) error {
		return errors.Join(out.Emit(key, nil), errBoom) // a partial output, then failure
	})
	if _, err := Reduce(failing, api.TaskID{Kind: api.ReduceTask, Attempt: 1}, sources); !errors.Is(err, errBoom) {
		t.Fatalf("Reduce error = %v", err)
	}
	if names := dirNames(t, spec.Output); !slices.Equal(names, []string{"part-r-00000"}) {
		t.Fatalf("output is %v, want only the first attempt's file", names)
	}
	if data, err := os.ReadFile(filepath.Join(spec.Output, "part-r-00000")); err != nil || len(data) != 0 {
		t.Fatalf("first attempt's output became %q (%v)", data, err)
	}
}

func TestContext(t *testing.T) {
	spec, split := setup(t, "x\n")
	spec.Params = map[string]string{"k": "v"}
	id := api.TaskID{Kind: api.MapTask, Attempt: 2}
	spec.Program.Mapper = api.MapFunc(func(ctx api.Context, _, _ []byte, _ api.Emitter) error {
		if ctx.Param("k") != "v" || ctx.Task() != id || ctx.Logger() == nil {
			return errBoom
		}
		ctx.Count("user", 5)
		return nil
	})
	_, counts, err := Map(spec, id, split, t.TempDir())
	if err != nil || counts["user"] != 5 {
		t.Fatalf("counts %v, err %v", counts, err)
	}
}

func TestOpenSourcesMissing(t *testing.T) {
	spec, _ := setup(t, "")
	if _, _, err := OpenSources([]string{spec.Inputs[0], "missing"}); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("got %v, want a not-exist error", err)
	}
}

// The program's partitioner, not the hash, decides where each key goes.
func TestCustomPartitioner(t *testing.T) {
	spec, split := setup(t, "a b a\nc\n")
	spec.Program.Partitioner = api.PartitionFunc(func([]byte, uint32) uint32 { return 1 })
	dir := t.TempDir()
	if _, _, err := Map(spec, api.TaskID{Kind: api.MapTask}, split, dir); err != nil {
		t.Fatal(err)
	}
	for r := range spec.R {
		sources, closeSources, err := OpenSources([]string{MapOutput(dir, 0, int(r))})
		if err != nil {
			t.Fatal(err)
		}
		counts, err := Reduce(spec, api.TaskID{Kind: api.ReduceTask, Index: r}, sources)
		if err := errors.Join(err, closeSources()); err != nil {
			t.Fatal(err)
		}
		if want := int64(3 * r); counts[ReduceOutputRecords] != want {
			t.Errorf("partition %d has %d keys, want %d", r, counts[ReduceOutputRecords], want)
		}
	}
}
