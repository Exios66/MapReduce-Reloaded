package local

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/exios66/mapreduce-reloaded/go/examples/wordcount"
	"github.com/exios66/mapreduce-reloaded/go/internal/storage"
	"github.com/exios66/mapreduce-reloaded/go/internal/storage/s3test"
	"github.com/exios66/mapreduce-reloaded/go/internal/task"
	"github.com/exios66/mapreduce-reloaded/go/pkg/api"
)

// Word count with its input and output in S3 matches the expected files,
// and leaves nothing else in the output.
func TestWordCountS3(t *testing.T) {
	ctx := t.Context()
	bucket := s3test.Start(t)
	if err := storage.MkdirAll(ctx, storage.Join(bucket, "corpus")); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(corpus, "*.txt"))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		w, err := storage.Create(ctx, storage.Join(bucket, "corpus", filepath.Base(f)))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(b); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
	}

	out := storage.Join(bucket, "out")
	spec := api.JobSpec{Program: wordcount.Program, Inputs: []string{storage.Join(bucket, "corpus", "*.txt")},
		Output: out, SplitSize: corpusSize / 5, R: 4}
	if _, err := Run(spec, t.TempDir()); err != nil {
		t.Fatal(err)
	}

	want, _ := filepath.Glob(filepath.Join(corpus, "expected", "*"))
	got, err := storage.List(ctx, storage.Join(out, "*"))
	if err != nil || len(got) != len(want) {
		t.Fatalf("output %q, %v; want %d files", got, err, len(want))
	}
	if left, err := storage.List(ctx, storage.Join(out, task.TempDir, "*")); err != nil || len(left) > 0 {
		t.Errorf("left in %s: %q, %v", task.TempDir, left, err)
	}
	for _, w := range want {
		wb, err := os.ReadFile(w)
		if err != nil {
			t.Fatal(err)
		}
		r, err := storage.Open(ctx, storage.Join(out, filepath.Base(w)), 0)
		if err != nil {
			t.Fatal(err)
		}
		gb, err := io.ReadAll(r)
		if err := errors.Join(err, r.Close()); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(gb, wb) {
			t.Errorf("%s differs from the expected output", filepath.Base(w))
		}
	}
}
