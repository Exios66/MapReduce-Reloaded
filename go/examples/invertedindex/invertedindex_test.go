package invertedindex_test

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/exios66/mapreduce-reloaded/go/examples/internal/run"
	"github.com/exios66/mapreduce-reloaded/go/examples/invertedindex"
	"github.com/exios66/mapreduce-reloaded/go/examples/wordcount"
)

var inputs = []string{"../../../testdata/corpus/*.txt"}

// Each word is listed with the corpus files it occurs in, whatever M is.
func TestInvertedIndex(t *testing.T) {
	files, _ := filepath.Glob(inputs[0])
	index := map[string][]string{}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range wordcount.Words(data) {
			if word := string(w); !slices.Contains(index[word], filepath.Base(f)) {
				index[word] = append(index[word], filepath.Base(f)) // files are globbed in order
			}
		}
	}
	var want strings.Builder
	for _, word := range slices.Sorted(maps.Keys(index)) {
		want.WriteString(word + "\t" + strings.Join(index[word], ",") + "\n")
	}
	for _, m := range []int{1, 3, 7} {
		got, err := run.Local(t, invertedindex.Program, inputs, m, nil)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want.String() {
			t.Errorf("M=%d: got %d bytes, want %d", m, len(got), want.Len())
		}
	}
}
