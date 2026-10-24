package distsort_test

import (
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/exios66/mapreduce-reloaded/go/examples/distsort"
	"github.com/exios66/mapreduce-reloaded/go/examples/internal/run"
	"github.com/exios66/mapreduce-reloaded/go/internal/local"
	"github.com/exios66/mapreduce-reloaded/go/internal/split"
	"github.com/exios66/mapreduce-reloaded/go/pkg/api"
)

// The output files, concatenated in order, are the input lines sorted.
func TestSort(t *testing.T) {
	var alphabet []byte // every byte that may appear in a line
	for c := range 256 {
		if c != '\n' && c != '\r' {
			alphabet = append(alphabet, byte(c))
		}
	}
	rng := rand.New(rand.NewPCG(1, 2))
	var lines []string
	for range 2000 {
		b := make([]byte, 1+rng.IntN(12))
		for i := range b {
			b[i] = alphabet[rng.IntN(len(alphabet))]
		}
		lines = append(lines, string(b))
	}
	in := run.Write(t, strings.Join(lines, "\n")+"\n")
	splitSize, err := split.SizeFor(t.Context(), []string{in}, 5)
	if err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	if _, err := local.Run(api.JobSpec{Program: distsort.Program, Inputs: []string{in}, Output: out, SplitSize: splitSize, R: 4}, t.TempDir()); err != nil {
		t.Fatal(err)
	}

	var got strings.Builder
	parts, _ := filepath.Glob(filepath.Join(out, "part-r-*"))
	for _, p := range parts {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if len(data) == 0 {
			t.Errorf("%s is empty; the ranges should spread the keys", filepath.Base(p))
		}
		got.Write(data)
	}
	slices.Sort(lines)
	if want := strings.Join(lines, "\t\n") + "\t\n"; got.String() != want {
		t.Fatal("the concatenated output is not the input sorted")
	}
}

func TestPartition(t *testing.T) {
	for _, tc := range []struct {
		key  string
		want uint32
	}{{"", 0}, {"\x00", 0}, {"\x3f\xff", 0}, {"\x40", 1}, {"\xbf", 2}, {"\xff", 3}} {
		if got := distsort.Partition([]byte(tc.key), 4); got != tc.want {
			t.Errorf("Partition(%q, 4) = %d, want %d", tc.key, got, tc.want)
		}
	}
}
