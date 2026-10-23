package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunLocal(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out")
	var stderr strings.Builder
	args := []string{"run", "-local", "-m", "2", "-r", "3", "wordcount", out, "../../../testdata/corpus/raven.txt"}
	if err := run(args, &stderr); err != nil {
		t.Fatal(err)
	}
	parts, _ := filepath.Glob(filepath.Join(out, "part-r-*"))
	if len(parts) != 3 {
		t.Errorf("got %d part files, want 3", len(parts))
	}
	if !strings.Contains(stderr.String(), "map.input.records\t") {
		t.Errorf("counters not printed: %q", stderr.String())
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatal(err)
	}
}

func TestRunErrors(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{nil, "usage"},
		{[]string{"run", "wordcount", "out", "in"}, "only -local"},
		{[]string{"run", "-local", "wordcount", "out"}, "usage"},
		{[]string{"run", "-local", "nope", "out", "in"}, `unknown program "nope"; registered: wordcount`},
		{[]string{"run", "-local", "-r", "4294967296", "wordcount", "out", "in"}, "too large"},
		{[]string{"run", "-bogus"}, "not defined"},
	} {
		if err := run(tc.args, new(strings.Builder)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("run(%q) = %v, want an error containing %q", tc.args, err, tc.want)
		}
	}
}
