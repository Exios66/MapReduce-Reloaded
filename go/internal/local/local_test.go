package local

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/exios66/mapreduce-reloaded/go/examples/wordcount"
	"github.com/exios66/mapreduce-reloaded/go/pkg/api"
)

const corpus = "../../../testdata/corpus"

// Word count over the corpus must match the expected files from
// scripts/make-corpus-expected.py byte for byte, whatever M is.
func TestWordCountGolden(t *testing.T) {
	inputs := []string{filepath.Join(corpus, "*.txt")}
	for _, m := range []int{1, 3, 7} {
		splitSize, err := SplitSize(inputs, m)
		if err != nil {
			t.Fatal(err)
		}
		out := t.TempDir()
		counts, err := Run(api.JobSpec{
			Program:   wordcount.Program,
			Inputs:    inputs,
			Output:    out,
			SplitSize: splitSize,
			R:         4,
		}, t.TempDir())
		if err != nil {
			t.Fatalf("M=%d: %v", m, err)
		}
		assertSameFiles(t, filepath.Join(corpus, "expected"), out)
		if counts[MapOutputRecords] != 59372 || counts[ReduceOutputRecords] != 11786 {
			t.Errorf("M=%d: counters %v", m, counts)
		}
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

// Without a combiner the output is the same; only the map output is larger.
func TestWithoutCombiner(t *testing.T) {
	p := wordcount.Program
	p.Combiner = nil
	out := t.TempDir()
	_, err := Run(api.JobSpec{
		Program: p,
		Inputs:  []string{filepath.Join(corpus, "*.txt")},
		Output:  out,
		R:       4,
	}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	assertSameFiles(t, filepath.Join(corpus, "expected"), out)
}

func TestErrors(t *testing.T) {
	valid := api.JobSpec{Program: wordcount.Program, Output: t.TempDir(), R: 1}
	for _, tc := range []struct {
		name   string
		inputs []string
		mapper api.Mapper
		want   string
	}{
		{"no match", []string{filepath.Join(t.TempDir(), "*.txt")}, nil, "no input matches"},
		{"bad pattern", []string{"["}, nil, "syntax error"},
		{"mapper error", []string{filepath.Join(corpus, "raven.txt")}, failMapper, "map task 0: boom"},
	} {
		spec := valid
		spec.Inputs = tc.inputs
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
