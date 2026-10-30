package papermap

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// When a PR changes python/ alone, CI's go job runs only make
// go-python-test (and py-e2e), not every Go package (docs/ci.md). That is
// sound only while no other Go test reads python/ or runs a Python
// interpreter; they all pass with python/ deleted and no Python on the
// PATH. A Go test that starts to must be added to go-python-test.
func TestOnlyGoPythonTestPackagesReadPython(t *testing.T) {
	reads := regexp.MustCompile(`python/|"python|MR_PYTHON|python3`)
	var got []string
	err := filepath.WalkDir(filepath.Join(root, "go"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return err
		}
		src, err := os.ReadFile(path)
		if err == nil && reads.Match(src) {
			dir, _ := filepath.Rel(root, filepath.Dir(path))
			if !slices.Contains(got, dir) {
				got = append(got, dir)
			}
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(got)
	if want := []string{"go/cmd/mr", "go/internal/papermap"}; !slices.Equal(got, want) {
		t.Errorf("Go tests that read python/ are in %q, want %q: add the new package to make go-python-test and to this list", got, want)
	}
	makefile, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	target := regexp.MustCompile(`(?m)^go-python-test:\n\t(.*)$`).FindSubmatch(makefile)
	if target == nil {
		t.Fatal("the Makefile has no go-python-test target")
	}
	for _, pkg := range []string{"./cmd/mr", "./internal/papermap"} {
		if !strings.Contains(string(target[1]), pkg) {
			t.Errorf("make go-python-test does not test %s", pkg)
		}
	}
}
