package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/exios66/mapreduce-reloaded/go/internal/formats/kv"
)

// The example jobs of testdata/examples/ (#153): each program's inputs,
// relative to the repository, and parameters. Word count has its own golden
// output, testdata/corpus/expected/, which TestRunLocalJobFile checks.
const (
	repo     = "../../.."
	examples = repo + "/testdata/examples"
)

type example struct {
	Inputs []string          `json:"inputs"`
	Params map[string]string `json:"params"`
}

func exampleJobs(t *testing.T) map[string]example {
	t.Helper()
	data, err := os.ReadFile(examples + "/jobs.json")
	if err != nil {
		t.Fatal(err)
	}
	var jobs map[string]example
	if err := json.Unmarshal(data, &jobs); err != nil {
		t.Fatal(err)
	}
	jobs["wordcount"] = example{Inputs: []string{"testdata/corpus/*.txt"}}
	for _, job := range jobs { // absolute, since Python runs in another directory
		for i, in := range job.Inputs {
			if job.Inputs[i], err = filepath.Abs(filepath.Join(repo, in)); err != nil {
				t.Fatal(err)
			}
		}
	}
	return jobs
}

// args returns the arguments, after "run" and its local flag, that run job
// with m map and r reduce tasks, spelled so both the Go and the Python
// command accept them.
func (job example) args(program, out string, m, r int, format string) []string {
	args := []string{"-m", fmt.Sprint(m), "-r", fmt.Sprint(r), "--output-format", format}
	for _, name := range slices.Sorted(maps.Keys(job.Params)) {
		args = append(args, "--param", name+"="+job.Params[name])
	}
	args = append(args, program, out)
	for _, in := range job.Inputs {
		args = append(args, in)
	}
	return args
}

func runGo(t *testing.T, job example, program string, m, r int, format string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "out")
	args := append([]string{"run", "-local"}, job.args(program, out, m, r, format)...)
	if err := run(args, new(strings.Builder), new(strings.Builder)); err != nil {
		t.Fatal(err)
	}
	return out
}

// parts returns the contents of dir's part-r-* files, by name.
func parts(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	names, err := filepath.Glob(filepath.Join(dir, "part-r-*"))
	if err != nil {
		t.Fatal(err)
	}
	files := make(map[string][]byte)
	for _, name := range names {
		if files[filepath.Base(name)], err = os.ReadFile(name); err != nil {
			t.Fatal(err)
		}
	}
	return files
}

// asText rewrites kv files as the text format would have written their
// records.
func asText(t *testing.T, files map[string][]byte) map[string][]byte {
	t.Helper()
	text := make(map[string][]byte)
	for name, data := range files {
		var b bytes.Buffer
		r := kv.NewReader(bytes.NewReader(data))
		for r.Next() {
			fmt.Fprintf(&b, "%s\t%s\n", r.Key(), r.Value())
		}
		if err := r.Err(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		text[name] = b.Bytes()
	}
	return text
}

func compareParts(t *testing.T, got, want map[string][]byte) {
	t.Helper()
	if !slices.Equal(slices.Sorted(maps.Keys(got)), slices.Sorted(maps.Keys(want))) {
		t.Fatalf("output files %v, want %v", slices.Sorted(maps.Keys(got)), slices.Sorted(maps.Keys(want)))
	}
	for name := range want {
		if !bytes.Equal(got[name], want[name]) {
			t.Errorf("%s differs: got %d bytes, want %d", name, len(got[name]), len(want[name]))
		}
	}
}

// Every example but word count gives the output that
// scripts/make-examples-expected.py computes from the programs'
// descriptions, for any M, with R = 1 and 4, as text and as kv.
func TestExamplesMatchExpected(t *testing.T) {
	for program, job := range exampleJobs(t) {
		if program == "wordcount" {
			continue
		}
		for _, r := range []int{1, 4} {
			want := parts(t, filepath.Join(examples, program, "expected", fmt.Sprintf("r%d", r)))
			for _, m := range []int{1, 3, 7} {
				t.Run(fmt.Sprintf("%s/m=%d/r=%d", program, m, r), func(t *testing.T) {
					t.Parallel()
					compareParts(t, parts(t, runGo(t, job, program, m, r, "text")), want)
				})
			}
			t.Run(fmt.Sprintf("%s/kv/r=%d", program, r), func(t *testing.T) {
				t.Parallel()
				compareParts(t, asText(t, parts(t, runGo(t, job, program, 3, r, "kv"))), want)
			})
		}
	}
}

// python returns the Python interpreter to compare with: $MR_PYTHON, which
// make sets to its PYTHON and which must work, or else the repository's
// virtual environment or python3, if either can import the package. With
// none, the test is skipped.
func python(t *testing.T) string {
	t.Helper()
	works := func(py string) bool {
		cmd := exec.Command(py, "-c", "import mapreduce_reloaded.__main__")
		cmd.Dir = repo + "/python"
		return cmd.Run() == nil
	}
	if py := os.Getenv("MR_PYTHON"); py != "" {
		if !works(py) {
			t.Fatalf("MR_PYTHON=%s cannot import mapreduce_reloaded; install python/ with its dependencies", py)
		}
		return py
	}
	for _, py := range []string{repo + "/python/.venv/bin/python", "python3"} {
		if works(py) {
			return py
		}
	}
	t.Skip("no Python that can import mapreduce_reloaded; set MR_PYTHON")
	return ""
}

// Go and Python give the same bytes for every example, as text and as
// uncompressed kv: the project's claim that the two are interchangeable.
func TestExamplesMatchPython(t *testing.T) {
	py := python(t)
	for program, job := range exampleJobs(t) {
		for _, format := range []string{"text", "kv"} {
			for _, r := range []int{1, 4} {
				t.Run(fmt.Sprintf("%s/%s/r=%d", program, format, r), func(t *testing.T) {
					t.Parallel()
					out := filepath.Join(t.TempDir(), "out")
					args := append([]string{"-m", "mapreduce_reloaded", "run", "--local"}, job.args(program, out, 3, r, format)...)
					cmd := exec.Command(py, args...)
					cmd.Dir = repo + "/python"
					if b, err := cmd.CombinedOutput(); err != nil {
						t.Fatalf("python: %v\n%s", err, b)
					}
					compareParts(t, parts(t, runGo(t, job, program, 3, r, format)), parts(t, out))
				})
			}
		}
	}
}
