package task

import (
	"errors"
	"io"
	"slices"
	"testing"

	"github.com/exios66/mapreduce-reloaded/go/internal/storage"
	"github.com/exios66/mapreduce-reloaded/go/internal/storage/s3test"
	"github.com/exios66/mapreduce-reloaded/go/pkg/api"
)

// With output in S3, duplicate attempts of a reduce task commit one file,
// and an attempt after Cleanup fails without writing anything (ADR 0013).
func TestDuplicateAttemptsS3(t *testing.T) {
	ctx := t.Context()
	spec, split := setup(t, "a b a\nc\n")
	spec.Output = storage.Join(s3test.Start(t), "out")
	spec.R = 1
	if err := Setup(ctx, spec.Output); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if _, _, err := Map(ctx, spec, api.TaskID{Kind: api.MapTask}, split, dir, nil, quiet); err != nil {
		t.Fatal(err)
	}
	reduce := func(attempt uint32) error {
		sources, closeSources, err := OpenSources([]string{MapOutput(dir, 0, 0)})
		if err != nil {
			t.Fatal(err)
		}
		_, err = Reduce(ctx, spec, api.TaskID{Kind: api.ReduceTask, Attempt: attempt}, sources, nil, quiet)
		return errors.Join(err, closeSources())
	}
	for attempt := range uint32(2) {
		if err := reduce(attempt); err != nil {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
	}
	if err := Cleanup(ctx, spec.Output); err != nil {
		t.Fatal(err)
	}
	if err := reduce(2); err == nil {
		t.Error("an attempt after Cleanup succeeded")
	}

	part := storage.Join(spec.Output, "part-r-00000")
	if names, err := storage.List(ctx, storage.Join(spec.Output, "*")); err != nil || !slices.Equal(names, []string{part}) {
		t.Errorf("output %q, %v; want only %s", names, err, part)
	}
	if left, err := storage.List(ctx, storage.Join(spec.Output, TempDir, "*")); err != nil || len(left) > 0 {
		t.Errorf("left in %s: %q, %v", TempDir, left, err)
	}
	r, err := storage.Open(ctx, part, 0)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(r)
	if err := errors.Join(err, r.Close()); err != nil || string(b) != "a\t2\nb\t1\nc\t1\n" {
		t.Errorf("part file = %q, %v", b, err)
	}
}
