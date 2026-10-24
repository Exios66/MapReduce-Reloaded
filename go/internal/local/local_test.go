package local

import (
	"bytes"
	"errors"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/exios66/mapreduce-reloaded/go/examples/wordcount"
	"github.com/exios66/mapreduce-reloaded/go/internal/combine"
	"github.com/exios66/mapreduce-reloaded/go/internal/split"
	"github.com/exios66/mapreduce-reloaded/go/internal/task"
	"github.com/exios66/mapreduce-reloaded/go/pkg/api"
)

const corpus = "../../../testdata/corpus"

// corpusSize is the size of the corpus's text files, in bytes.
const corpusSize = 337637

var corpusInputs = []string{filepath.Join(corpus, "*.txt")}

// runJob runs p over the corpus with about m map tasks and r reduce tasks,
// and returns the output directory and the counters.
func runJob(t *testing.T, p api.Program, m int, r uint32) (string, map[string]int64) {
	t.Helper()
	splitSize, err := split.SizeFor(t.Context(), corpusInputs, m)
	if err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	spec := api.JobSpec{Program: p, Inputs: corpusInputs, Output: out, SplitSize: splitSize, R: r}
	counts, err := Run(spec, t.TempDir())
	if err != nil {
		t.Fatalf("M=%d: %v", m, err)
	}
	return out, counts
}

// Word count over the corpus must match the expected files from
// scripts/make-corpus-expected.py byte for byte, whatever M is.
func TestWordCountGolden(t *testing.T) {
	for _, m := range []int{1, 3, 7} {
		out, counts := runJob(t, wordcount.Program, m, 4)
		assertSameFiles(t, filepath.Join(corpus, "expected"), out)
		if counts[task.MapInputBytes] != corpusSize || counts[task.MapOutputRecords] != 59372 ||
			counts[task.ReduceInputRecords] != counts[combine.OutputRecords] || counts[task.ReduceOutputRecords] != 11786 {
			t.Errorf("M=%d: counters %v", m, counts)
		}
	}
}

// Without a combiner the output is the same; only the map output is larger.
func TestWithoutCombiner(t *testing.T) {
	p := wordcount.Program
	p.Combiner = nil
	out, _ := runJob(t, p, 3, 4)
	assertSameFiles(t, filepath.Join(corpus, "expected"), out)
}

// A key's values reach the reducer in input order, whatever M is. The
// reducer here concatenates them, so any reordering changes the output.
func TestValueOrderIndependentOfM(t *testing.T) {
	p := api.Program{
		Mapper: api.MapFunc(func(_ api.Context, offset, line []byte, out api.Emitter) error {
			if len(line) == 0 {
				return nil
			}
			return out.Emit(line[:1], offset)
		}),
		Reducer: api.ReduceFunc(func(_ api.Context, key []byte, values iter.Seq[[]byte], out api.Emitter) error {
			var joined []byte
			for v := range values {
				joined = append(append(joined, v...), ' ')
			}
			return out.Emit(key, joined)
		}),
	}
	want, _ := runJob(t, p, 1, 2)
	for _, m := range []int{3, 7} {
		got, _ := runJob(t, p, m, 2)
		assertSameFiles(t, want, got)
	}
}

func assertSameFiles(t *testing.T, wantDir, gotDir string) {
	t.Helper()
	want, _ := filepath.Glob(filepath.Join(wantDir, "*"))
	got, _ := filepath.Glob(filepath.Join(gotDir, "*"))
	if len(got) != len(want) {
		t.Fatalf("got %d output files, want %d", len(got), len(want))
	}
	for _, w := range want {
		name := filepath.Base(w)
		wb, err := os.ReadFile(w)
		if err != nil {
			t.Fatal(err)
		}
		gb, err := os.ReadFile(filepath.Join(gotDir, name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(gb, wb) {
			t.Errorf("%s differs from the expected output", name)
		}
	}
}

func TestErrors(t *testing.T) {
	raven := []string{filepath.Join(corpus, "raven.txt")}
	empty, used := filepath.Join(t.TempDir(), "empty.txt"), t.TempDir()
	for _, f := range []string{empty, filepath.Join(used, "part-r-00000")} {
		if err := os.WriteFile(f, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	valid := api.JobSpec{Program: wordcount.Program, Output: t.TempDir(), R: 1}
	for _, tc := range []struct {
		name   string
		inputs []string
		output string
		mapper api.Mapper
		want   string
	}{
		{"no match", []string{filepath.Join(t.TempDir(), "*.txt")}, "", nil, "no input matches"},
		{"empty input", []string{empty}, "", nil, "input is empty"},
		{"output in use", raven, used, nil, "already holds job output"},
		{"mapper error", raven, "", failMapper, "map task 0: boom"},
	} {
		spec := valid
		spec.Inputs = tc.inputs
		if tc.output != "" {
			spec.Output = tc.output
		}
		if tc.mapper != nil {
			spec.Program.Mapper = tc.mapper
		}
		if _, err := Run(spec, t.TempDir()); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want an error containing %q", tc.name, err, tc.want)
		}
	}
	if _, err := Run(api.JobSpec{}, t.TempDir()); err == nil {
		t.Error("an invalid spec ran")
	}
}

var failMapper = api.MapFunc(func(api.Context, []byte, []byte, api.Emitter) error {
	return errors.New("boom")
})
