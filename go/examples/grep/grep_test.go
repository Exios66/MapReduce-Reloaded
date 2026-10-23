package grep_test

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/exios66/mapreduce-reloaded/go/examples/grep"
	"github.com/exios66/mapreduce-reloaded/go/internal/local"
	"github.com/exios66/mapreduce-reloaded/go/internal/split"
	"github.com/exios66/mapreduce-reloaded/go/pkg/api"
)

var inputs = []string{"../../../testdata/corpus/*.txt"}

func run(t *testing.T, m int, params map[string]string) (string, error) {
	t.Helper()
	splitSize, err := split.SizeFor(inputs, m)
	if err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	_, err = local.Run(api.JobSpec{Program: grep.Program, Inputs: inputs, Output: out, SplitSize: splitSize, R: 1, Params: params}, t.TempDir())
	return out, err
}

// The output is every line of the input that contains the pattern, in
// byte order, whatever M is.
func TestGrep(t *testing.T) {
	const pattern = "Raven"
	files, _ := filepath.Glob(inputs[0])
	var want []string
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for line := range strings.Lines(string(data)) {
			if line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"); strings.Contains(line, pattern) {
				want = append(want, line+"\t\n")
			}
		}
	}
	slices.Sort(want)
	if len(want) < 10 {
		t.Fatalf("only %d lines match %q; pick a commoner pattern", len(want), pattern)
	}
	for _, m := range []int{1, 3, 7} {
		out, err := run(t, m, map[string]string{"pattern": pattern})
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(out, "part-r-00000"))
		if err != nil {
			t.Fatal(err)
		}
		if w := strings.Join(want, ""); !bytes.Equal(got, []byte(w)) {
			t.Errorf("M=%d: got %d bytes, want the %d matching lines (%d bytes)", m, len(got), len(want), len(w))
		}
	}
}

func TestNoPattern(t *testing.T) {
	if _, err := run(t, 1, nil); err == nil || !strings.Contains(err.Error(), "no pattern") {
		t.Fatalf("got %v, want an error naming the missing pattern", err)
	}
}
