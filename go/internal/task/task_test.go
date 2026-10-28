package task

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"iter"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/exios66/mapreduce-reloaded/go/examples/wordcount"
	"github.com/exios66/mapreduce-reloaded/go/internal/formats"
	"github.com/exios66/mapreduce-reloaded/go/pkg/api"
)

var (
	errBoom = errors.New("boom")
	quiet   = slog.New(slog.DiscardHandler)
)

// setup writes an input file and returns a word count spec over it, with
// output in a new directory set up for the job, and the input's single
// split.
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
	if err := Setup(t.Context(), spec.Output); err != nil {
		t.Fatal(err)
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
	sizes, counts, err := Map(t.Context(), spec, api.TaskID{Kind: api.MapTask, Attempt: 3}, split, dir, nil, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"map-00000-r-00000.kv", "map-00000-r-00001.kv"}; !slices.Equal(dirNames(t, dir), want) {
		t.Fatalf("map wrote %v, want %v", dirNames(t, dir), want)
	}
	written := int64(0)
	for r, size := range sizes {
		info, err := os.Stat(MapOutput(dir, 0, r))
		if err != nil || info.Size() != size {
			t.Fatalf("partition %d: reported %d bytes, file has %v (%v)", r, size, info.Size(), err)
		}
		written += size
	}
	if counts[MapInputRecords] != 2 || counts[MapInputBytes] != 8 || counts[MapOutputRecords] != 4 ||
		counts[MapOutputBytes] != written {
		t.Fatalf("map counters %v", counts)
	}

	total, read := int64(0), int64(0) // the combiner leaves a, b and c
	for r := range spec.R {
		sources, closeSources, err := OpenSources([]string{MapOutput(dir, 0, int(r))})
		if err != nil {
			t.Fatal(err)
		}
		counts, err := Reduce(t.Context(), spec, api.TaskID{Kind: api.ReduceTask, Index: r}, sources, nil, quiet)
		if err := errors.Join(err, closeSources()); err != nil {
			t.Fatal(err)
		}
		total += counts[ReduceOutputRecords]
		read += counts[ReduceInputRecords]
	}
	if err := Cleanup(t.Context(), spec.Output); err != nil {
		t.Fatal(err)
	}
	if want := []string{"part-r-00000", "part-r-00001"}; total != 3 || read != 3 || !slices.Equal(dirNames(t, spec.Output), want) {
		t.Fatalf("%d records read, %d in %v; want 3 in %v", read, total, dirNames(t, spec.Output), want)
	}
	// An attempt that runs after the job has finished fails, and leaves
	// nothing behind.
	if _, err := Reduce(t.Context(), spec, api.TaskID{Kind: api.ReduceTask, Attempt: 1}, nil, nil, quiet); err == nil || len(dirNames(t, spec.Output)) != 2 {
		t.Fatalf("late attempt: %v; output %v", err, dirNames(t, spec.Output))
	}
}

// A cancelled attempt, such as a backup whose twin finished first, stops
// and leaves no files.
func TestCancelledAttemptStops(t *testing.T) {
	spec, split := setup(t, "a b\n")
	dir := t.TempDir()
	if _, _, err := Map(t.Context(), spec, api.TaskID{Kind: api.MapTask}, split, dir, nil, quiet); err != nil {
		t.Fatal(err)
	}
	sources, closeSources, err := OpenSources([]string{MapOutput(dir, 0, 0)})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = closeSources() }()

	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := Map(cancelled, spec, api.TaskID{Kind: api.MapTask, Attempt: 1}, split, t.TempDir(), nil, quiet); !errors.Is(err, context.Canceled) {
		t.Fatalf("Map error = %v, want %v", err, context.Canceled)
	}
	if _, err := Reduce(cancelled, spec, api.TaskID{Kind: api.ReduceTask}, sources, nil, quiet); !errors.Is(err, context.Canceled) {
		t.Fatalf("Reduce error = %v, want %v", err, context.Canceled)
	}
	if names := dirNames(t, spec.Output); len(names) != 1 || names[0] != TempDir {
		t.Fatalf("cancelled reduce left %v", names)
	}
}

// A reduce attempt cancelled while its last group runs still fails, and
// commits no part or side file.
func TestCancelledDuringLastGroup(t *testing.T) {
	spec, split := setup(t, "a\n")
	dir := t.TempDir()
	if _, _, err := Map(t.Context(), spec, api.TaskID{Kind: api.MapTask}, split, dir, nil, quiet); err != nil {
		t.Fatal(err)
	}
	r := api.HashPartitioner.Partition([]byte("a"), spec.R)
	sources, closeSources, err := OpenSources([]string{MapOutput(dir, 0, int(r))})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = closeSources() }()

	ctx, cancel := context.WithCancel(t.Context())
	spec.Program.Reducer = api.ReduceFunc(func(c api.Context, _ []byte, _ iter.Seq[[]byte], _ api.Emitter) error {
		cancel()
		_, err := c.SideOutput("side")
		return err
	})
	if _, err := Reduce(ctx, spec, api.TaskID{Kind: api.ReduceTask, Index: r}, sources, nil, quiet); !errors.Is(err, context.Canceled) {
		t.Fatalf("Reduce error = %v, want %v", err, context.Canceled)
	}
	if names := dirNames(t, spec.Output); len(names) != 1 || names[0] != TempDir {
		t.Fatalf("cancelled reduce left %v", names)
	}
}

// A failed attempt leaves no files, and does not disturb the output of an
// attempt that already succeeded.
func TestFailedAttemptLeavesNoFiles(t *testing.T) {
	spec, split := setup(t, "a b\n")
	dir := t.TempDir()
	failing := spec
	failing.Program.Mapper = api.MapFunc(func(api.Context, []byte, []byte, api.Emitter) error { return errBoom })
	if _, _, err := Map(t.Context(), failing, api.TaskID{Kind: api.MapTask}, split, dir, nil, quiet); !errors.Is(err, errBoom) {
		t.Fatalf("Map error = %v", err)
	}
	if names := dirNames(t, dir); len(names) != 0 {
		t.Fatalf("failed map left %v", names)
	}

	// The first reduce attempt succeeds; a failing retry must leave its
	// output in place.
	if _, err := Reduce(t.Context(), spec, api.TaskID{Kind: api.ReduceTask}, nil, nil, quiet); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Map(t.Context(), spec, api.TaskID{Kind: api.MapTask}, split, dir, nil, quiet); err != nil {
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
	if _, err := Reduce(t.Context(), failing, api.TaskID{Kind: api.ReduceTask, Attempt: 1}, sources, nil, quiet); !errors.Is(err, errBoom) {
		t.Fatalf("Reduce error = %v", err)
	}
	if names := dirNames(t, spec.Output); !slices.Equal(names, []string{TempDir, "part-r-00000"}) {
		t.Fatalf("output is %v, want only the first attempt's file", names)
	}
	if names := dirNames(t, filepath.Join(spec.Output, TempDir)); len(names) != 0 {
		t.Fatalf("failed attempt left %v", names)
	}
	if data, err := os.ReadFile(filepath.Join(spec.Output, "part-r-00000")); err != nil || len(data) != 0 {
		t.Fatalf("first attempt's output became %q (%v)", data, err)
	}
}

// A map attempt that fails after writing some of its partitions, as one
// out of disk space would, leaves none of them (#183).
func TestFailedMapLeavesNoPartitions(t *testing.T) {
	spec, split := setup(t, "a b\n")
	spec.Program.Partitioner = api.PartitionFunc(func(key []byte, _ uint32) uint32 { return uint32(key[0] - 'a') })
	spec.Program.Combiner = api.ReduceFunc(func(_ api.Context, key []byte, values iter.Seq[[]byte], out api.Emitter) error {
		if string(key) == "b" { // in partition 1, written after partition 0
			return errBoom
		}
		return wordcount.Sum(nil, key, values, out)
	})
	dir := t.TempDir()
	if _, _, err := Map(t.Context(), spec, api.TaskID{Kind: api.MapTask}, split, dir, nil, quiet); !errors.Is(err, errBoom) {
		t.Fatalf("Map error = %v", err)
	}
	if names := dirNames(t, dir); len(names) != 0 {
		t.Fatalf("failed map left %v", names)
	}
}

func TestContext(t *testing.T) {
	spec, split := setup(t, "x\n")
	spec.Params = map[string]string{"k": "v"}
	id := api.TaskID{Kind: api.MapTask, Attempt: 2}
	log := quiet.With("task", id)
	spec.Program.Mapper = api.MapFunc(func(ctx api.Context, _, _ []byte, _ api.Emitter) error {
		if ctx.Param("k") != "v" || ctx.Input() != split.Path || ctx.Task() != id || ctx.Logger() != log {
			return errBoom
		}
		ctx.Count("user", 5)
		return nil
	})
	_, counts, err := Map(t.Context(), spec, id, split, t.TempDir(), nil, log)
	if err != nil || counts["user"] != 5 {
		t.Fatalf("counts %v, err %v", counts, err)
	}
}

// User code that panics on a record fails the attempt with the record's
// number; an attempt told to skip that record succeeds without it (§4.6).
func TestBadRecords(t *testing.T) {
	spec, split := setup(t, "a\nbad\nc\n")
	spec.R = 1
	mapper := spec.Program.Mapper
	spec.Program.Mapper = api.MapFunc(func(ctx api.Context, key, value []byte, out api.Emitter) error {
		if string(value) == "bad" {
			panic("bad line")
		}
		return mapper.Map(ctx, key, value, out)
	})
	spec.Program.Reducer = api.ReduceFunc(func(ctx api.Context, key []byte, values iter.Seq[[]byte], out api.Emitter) error {
		if string(key) == "c" {
			panic("bad key")
		}
		return wordcount.Program.Reducer.Reduce(ctx, key, values, out)
	})
	dir := t.TempDir()
	var crash *CrashError
	if _, _, err := Map(t.Context(), spec, api.TaskID{Kind: api.MapTask}, split, dir, nil, quiet); !errors.As(err, &crash) || crash.Record != 1 {
		t.Fatalf("map: got %v, want a crash on record 1", err)
	}
	_, counts, err := Map(t.Context(), spec, api.TaskID{Kind: api.MapTask, Attempt: 1}, split, dir, []uint64{1}, quiet)
	if err != nil || counts[SkippedRecords] != 1 || counts[MapOutputRecords] != 2 {
		t.Fatalf("map skipping record 1: counts %v, err %v", counts, err)
	}

	// The map output holds the keys a and c, so c is group 1.
	reduce := func(attempt uint32, skip []uint64) (map[string]int64, error) {
		sources, closeSources, err := OpenSources([]string{MapOutput(dir, 0, 0)})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = closeSources() }()
		return Reduce(t.Context(), spec, api.TaskID{Kind: api.ReduceTask, Attempt: attempt}, sources, skip, quiet)
	}
	if _, err := reduce(0, nil); !errors.As(err, &crash) || crash.Record != 1 {
		t.Fatalf("reduce: got %v, want a crash on record 1", err)
	}
	if counts, err := reduce(1, []uint64{1}); err != nil || counts[SkippedRecords] != 1 || counts[ReduceOutputRecords] != 1 {
		t.Fatalf("reduce skipping record 1: counts %v, err %v", counts, err)
	}
	if data, err := os.ReadFile(filepath.Join(spec.Output, "part-r-00000")); err != nil || string(data) != "a\t1\n" {
		t.Fatalf("output %q (%v)", data, err)
	}
}

// A panic outside any one record, here in the combiner, fails the attempt
// and leaves no side output.
func TestCombinerPanic(t *testing.T) {
	spec, split := setup(t, "a b a\n")
	mapper := spec.Program.Mapper
	spec.Program.Mapper = api.MapFunc(func(ctx api.Context, key, value []byte, out api.Emitter) error {
		if _, err := ctx.SideOutput("seen"); err != nil {
			return err
		}
		return mapper.Map(ctx, key, value, out)
	})
	spec.Program.Combiner = api.ReduceFunc(func(api.Context, []byte, iter.Seq[[]byte], api.Emitter) error {
		panic("bad combiner")
	})
	if _, _, err := Map(t.Context(), spec, api.TaskID{Kind: api.MapTask}, split, t.TempDir(), nil, quiet); err == nil {
		t.Fatal("map succeeded")
	}
	if names := dirNames(t, spec.Output); slices.Contains(names, "seen") {
		t.Fatalf("failed attempt committed its side output: %v", names)
	}
}

// A side output appears once, with one attempt's content, however many
// attempts fail or succeed (§4.5).
func TestSideOutput(t *testing.T) {
	spec, split := setup(t, "a b a\n")
	spec.R = 1
	dir := t.TempDir()
	if _, _, err := Map(t.Context(), spec, api.TaskID{Kind: api.MapTask}, split, dir, nil, quiet); err != nil {
		t.Fatal(err)
	}
	reduce := func(attempt uint32, fail bool) error {
		spec := spec
		spec.Program.Reducer = api.ReduceFunc(func(ctx api.Context, key []byte, _ iter.Seq[[]byte], _ api.Emitter) error {
			w, err := ctx.SideOutput(fmt.Sprintf("keys-%d", ctx.Task().Index))
			if err == nil {
				_, err = fmt.Fprintf(w, "%s\n", key)
			}
			if fail {
				err = errors.Join(err, errBoom)
			}
			return err
		})
		sources, closeSources, err := OpenSources([]string{MapOutput(dir, 0, 0)})
		if err != nil {
			return err
		}
		_, err = Reduce(t.Context(), spec, api.TaskID{Kind: api.ReduceTask, Attempt: attempt}, sources, nil, quiet)
		return errors.Join(err, closeSources())
	}

	if err := reduce(0, true); !errors.Is(err, errBoom) {
		t.Fatalf("failing attempt: %v", err)
	}
	if names := dirNames(t, spec.Output); !slices.Equal(names, []string{TempDir}) {
		t.Fatalf("failed attempt left %v in the output", names)
	}
	if names := dirNames(t, filepath.Join(spec.Output, TempDir)); len(names) != 0 {
		t.Fatalf("failed attempt left %v", names)
	}
	for attempt := range uint32(2) { // a duplicate attempt writes the same bytes
		if err := reduce(1+attempt, false); err != nil {
			t.Fatal(err)
		}
	}
	if err := Cleanup(t.Context(), spec.Output); err != nil {
		t.Fatal(err)
	}
	if names := dirNames(t, spec.Output); !slices.Equal(names, []string{"keys-0", "part-r-00000"}) {
		t.Fatalf("output is %v", names)
	}
	if data, err := os.ReadFile(filepath.Join(spec.Output, "keys-0")); err != nil || string(data) != "a\nb\n" {
		t.Fatalf("keys-0 = %q (%v)", data, err)
	}
}

func TestSideOutputNames(t *testing.T) {
	spec, _ := setup(t, "")
	ctx := newContext(t.Context(), spec, api.TaskID{Kind: api.MapTask}, nil, quiet)
	for _, name := range []string{"", ".", "..", ".hidden", "_SUCCESS", "part-r-00000", "a/b", "../x"} {
		if _, err := ctx.SideOutput(name); err == nil {
			t.Errorf("SideOutput(%q) succeeded", name)
		}
	}
	w, err := ctx.SideOutput("ok")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w, "x"); err != nil {
		t.Fatal(err)
	}
	ctx.commitSide(new(error))
	if data, err := os.ReadFile(filepath.Join(ctx.output, "ok")); err != nil || string(data) != "x" {
		t.Fatalf("ok = %q (%v)", data, err)
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
	if _, _, err := Map(t.Context(), spec, api.TaskID{Kind: api.MapTask}, split, dir, nil, quiet); err != nil {
		t.Fatal(err)
	}
	for r := range spec.R {
		sources, closeSources, err := OpenSources([]string{MapOutput(dir, 0, int(r))})
		if err != nil {
			t.Fatal(err)
		}
		counts, err := Reduce(t.Context(), spec, api.TaskID{Kind: api.ReduceTask, Index: r}, sources, nil, quiet)
		if err := errors.Join(err, closeSources()); err != nil {
			t.Fatal(err)
		}
		if want := int64(3 * r); counts[ReduceOutputRecords] != want {
			t.Errorf("partition %d has %d keys, want %d", r, counts[ReduceOutputRecords], want)
		}
	}
}

// The output directory is a literal name, whatever characters it has: the
// check must still see the part files in it.
func TestCheckOutputLiteralDirectory(t *testing.T) {
	for _, name := range []string{"out", "out[ab]", "out*", "out?", `out\x`} {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), name)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := CheckOutput(t.Context(), dir); err != nil {
				t.Fatalf("empty directory: %v", err)
			}
			if err := os.WriteFile(filepath.Join(dir, "part-r-00000"), nil, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := CheckOutput(t.Context(), dir); err == nil {
				t.Fatal("a directory with output passed the check")
			}
		})
	}
}
