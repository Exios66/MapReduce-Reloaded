// Package run runs an example program in local mode, for the examples'
// tests.
package run

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/exios66/mapreduce-reloaded/go/internal/local"
	"github.com/exios66/mapreduce-reloaded/go/internal/split"
	"github.com/exios66/mapreduce-reloaded/go/pkg/api"
)

// Local runs p over inputs with about m map tasks, one reduce task and
// params, and returns its output file.
func Local(t *testing.T, p api.Program, inputs []string, m int, params map[string]string) ([]byte, error) {
	t.Helper()
	splitSize, err := split.SizeFor(t.Context(), inputs, m)
	if err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	spec := api.JobSpec{Program: p, Inputs: inputs, Output: out, SplitSize: splitSize, R: 1, Params: params}
	if _, err := local.Run(spec, t.TempDir()); err != nil {
		return nil, err
	}
	return os.ReadFile(filepath.Join(out, "part-r-00000"))
}

// Write writes data to a new file in a temporary directory, and returns
// its path.
func Write(t *testing.T, data string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "input")
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
